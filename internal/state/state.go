// Package state mantiene en memoria el estado actual de cada check y el
// buffer de resultados pendientes de escribir.
//
//   - Confirmación: un check pasa a «pendiente» con el primer fallo y a
//     «caído» al llegar a su umbral de fallos consecutivos; vuelve a
//     operativo con el primer éxito.
//   - Lento: un resultado correcto que supera el umbral de lentitud del
//     check (o que trae un aviso, como un certificado a punto de caducar).
//   - Incidencias: se abren al confirmarse la caída (con el inicio en el
//     primer fallo de la racha) y se cierran al recuperarse. Se escriben al
//     momento.
//   - Resultados y agregados por hora se acumulan en memoria y se vuelcan en
//     lote (una transacción cada intervalo de volcado) para reducir las
//     escrituras en almacenamiento delicado.
package state

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

// Store es la parte del almacenamiento que necesita el estado.
type Store interface {
	WriteBatch(ctx context.Context, b store.Batch) error
	OpenIncident(ctx context.Context, i *model.Incident) error
	CloseIncident(ctx context.Context, id int64, end time.Time, lastError string) error
	HourlyTotals(ctx context.Context, since time.Time) (map[int64]model.Hourly, error)
}

// RecentBeats es cuántos pulsos recientes de cada check se guardan en
// memoria (barra de pulsos).
const RecentBeats = 100

// Observation es el resultado de una ejecución, tal como lo entrega el
// planificador.
type Observation struct {
	CheckID    int64
	At         time.Time
	OK         bool
	Degraded   bool // correcto pero con aviso (p. ej. certificado a punto de caducar)
	NoPerm     bool // el sistema no permite la comprobación
	Latency    time.Duration
	Message    msg.Msg
	CertExpiry time.Time // caducidad del certificado TLS, si se conoce
}

// CheckState es el estado actual de un check.
type CheckState struct {
	Status     model.Status
	Last       model.Result // válido si HasLast
	HasLast    bool
	Since      time.Time // momento del último cambio de estado (cero si desconocido)
	Fails      int       // fallos consecutivos
	Incident   *model.Incident
	Recent     []model.Result // últimos pulsos, del más antiguo al más reciente
	CertExpiry time.Time
}

// Transition describe el cambio de estado provocado por un resultado.
type Transition struct {
	Changed bool
	From    model.Status
	To      model.Status
	Opened  *model.Incident // incidencia abierta con este resultado
	Closed  *model.Incident // incidencia cerrada con este resultado
}

// Límites del buffer: al superar softLimit se adelanta el volcado; si los
// volcados fallan (disco lleno…), por encima de hardLimit se descartan los
// resultados más antiguos para acotar la memoria.
const (
	softLimit = 2000
	hardLimit = 20000
)

type checkMeta struct {
	threshold     int
	degradedAfter time.Duration
	paused        bool
}

type hourKey struct {
	check int64
	hour  int64
}

// Tracker es el estado en memoria y el buffer de resultados.
type Tracker struct {
	log *slog.Logger
	st  Store
	now func() time.Time

	mu      sync.RWMutex
	meta    map[int64]checkMeta
	states  map[int64]*CheckState
	pending []model.Result
	hourly  map[hourKey]*model.Hourly
	totals  map[time.Duration]map[int64]model.Hourly // uptime: agregados guardados por ventana

	flushNow chan struct{}
	flushMu  sync.Mutex

	// AggregatesOnly desactiva el guardado de resultados en bruto.
	AggregatesOnly atomic.Bool

	flushed atomic.Int64
	dropped atomic.Int64
}

// Ventanas de uptime.
var UptimeWindows = []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}

// New crea un Tracker vacío. now puede ser nil (time.Now).
func New(st Store, log *slog.Logger, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{
		log: log, st: st, now: now,
		meta:     map[int64]checkMeta{},
		states:   map[int64]*CheckState{},
		hourly:   map[hourKey]*model.Hourly{},
		totals:   map[time.Duration]map[int64]model.Hourly{},
		flushNow: make(chan struct{}, 1),
	}
}

func metaOf(c model.Check) checkMeta {
	return checkMeta{threshold: c.Threshold(), degradedAfter: c.DegradedAfter, paused: c.Paused}
}

// Restore inicializa el estado al arrancar a partir de los checks, sus
// últimos resultados y sus incidencias abiertas.
func (t *Tracker) Restore(checks []model.Check, recent map[int64][]model.Result, open map[int64]model.Incident) {
	var toClose []*model.Incident
	defer func() {
		for _, inc := range toClose {
			t.writeIncident(nil, inc)
		}
	}()
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range checks {
		t.meta[c.ID] = metaOf(c)
		st := &CheckState{Status: model.StatusUnknown}
		if rs := recent[c.ID]; len(rs) > 0 {
			st.Recent = append([]model.Result(nil), rs...)
			st.Last, st.HasLast = rs[len(rs)-1], true
			st.Status = st.Last.State
			for i := len(rs) - 1; i >= 0 && !rs[i].OK && rs[i].State != model.StatusNoPerm; i-- {
				st.Fails++
			}
		}
		if inc, ok := open[c.ID]; ok {
			inc := inc
			st.Incident = &inc
			st.Status = model.StatusDown
			if st.Fails < t.meta[c.ID].threshold {
				st.Fails = t.meta[c.ID].threshold
			}
		}
		if c.Paused {
			st.Status = model.StatusPaused
			if inc := pauseIncident(st, t.now()); inc != nil {
				toClose = append(toClose, inc)
			}
		}
		t.states[c.ID] = st
	}
}

// pauseIncident cierra la incidencia abierta de un check que se pausa: el
// tiempo en pausa no cuenta como caída. Devuelve la incidencia cerrada, que
// hay que guardar fuera del cerrojo.
func pauseIncident(st *CheckState, end time.Time) *model.Incident {
	st.Fails = 0
	if st.Incident == nil {
		return nil
	}
	inc := *st.Incident
	if end.Before(inc.Start) {
		end = inc.Start
	}
	inc.End = end
	st.Incident = nil
	return &inc
}

// SetCheck actualiza los umbrales y la pausa de un check (alta o edición).
// Pausar un check cierra su incidencia abierta.
func (t *Tracker) SetCheck(c model.Check) {
	var closed *model.Incident
	defer func() {
		if closed != nil {
			t.writeIncident(nil, closed)
		}
	}()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.meta[c.ID] = metaOf(c)
	st, ok := t.states[c.ID]
	if !ok {
		st = &CheckState{Status: model.StatusUnknown}
		t.states[c.ID] = st
	}
	switch {
	case c.Paused:
		st.Status = model.StatusPaused
		closed = pauseIncident(st, t.now())
	case st.Status == model.StatusPaused:
		// Al reanudar, el estado es desconocido hasta la próxima comprobación.
		st.Status = model.StatusUnknown
	case st.HasLast:
		st.Status = st.Last.State
	default:
		st.Status = model.StatusUnknown
	}
}

// Remove olvida un check (borrado). Sus datos pendientes se descartarán al
// volcar, porque el almacenamiento ignora los de checks inexistentes.
func (t *Tracker) Remove(id int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.states, id)
	delete(t.meta, id)
	for k := range t.hourly {
		if k.check == id {
			delete(t.hourly, k)
		}
	}
}

// Record registra un resultado: calcula el nuevo estado, abre o cierra
// incidencias y lo añade al buffer.
func (t *Tracker) Record(o Observation) Transition {
	t.mu.Lock()
	meta, ok := t.meta[o.CheckID]
	if !ok {
		meta = checkMeta{threshold: model.DefaultFailThreshold}
		t.meta[o.CheckID] = meta
	}
	st, ok := t.states[o.CheckID]
	if !ok {
		st = &CheckState{Status: model.StatusUnknown}
		t.states[o.CheckID] = st
	}
	from := st.Status
	var to model.Status
	var opened, closed *model.Incident

	switch {
	case o.NoPerm:
		to = model.StatusNoPerm
	case o.OK:
		st.Fails = 0
		to = model.StatusUp
		if o.Degraded || (meta.degradedAfter > 0 && o.Latency > meta.degradedAfter) {
			to = model.StatusDegraded
		}
		if st.Incident != nil {
			inc := *st.Incident
			inc.End = o.At
			closed = &inc
			st.Incident = nil
		}
	default:
		st.Fails++
		to = model.StatusPending
		if st.Fails >= meta.threshold || from == model.StatusDown {
			to = model.StatusDown
			if st.Incident == nil {
				start := o.At
				// El inicio es el primer fallo de la racha.
				// La racha mide st.Fails (la pausa la reinicia).
				for i, n := len(st.Recent)-1, 1; i >= 0 && n < st.Fails && !st.Recent[i].OK && st.Recent[i].State != model.StatusNoPerm; i, n = i-1, n+1 {
					start = st.Recent[i].At
				}
				inc := model.Incident{CheckID: o.CheckID, Start: start, LastError: o.Message.Encode()}
				st.Incident = &inc
				opened = &inc
			} else {
				st.Incident.LastError = o.Message.Encode()
			}
		}
	}

	r := model.Result{CheckID: o.CheckID, At: o.At, OK: o.OK, State: to, Latency: o.Latency, Message: o.Message.Encode()}
	st.Last, st.HasLast, st.Status = r, true, to
	if !o.CertExpiry.IsZero() {
		st.CertExpiry = o.CertExpiry
	}
	if from != to {
		st.Since = o.At
	}
	st.Recent = append(st.Recent, r)
	if len(st.Recent) > RecentBeats {
		st.Recent = append(st.Recent[:0:0], st.Recent[len(st.Recent)-RecentBeats:]...)
	}

	if !t.AggregatesOnly.Load() {
		t.pending = append(t.pending, r)
	}
	if to != model.StatusNoPerm {
		k := hourKey{o.CheckID, o.At.UTC().Truncate(time.Hour).Unix()}
		h := t.hourly[k]
		if h == nil {
			h = &model.Hourly{CheckID: o.CheckID, Hour: time.Unix(k.hour, 0).UTC()}
			t.hourly[k] = h
		}
		one := model.Hourly{Total: 1}
		if to == model.StatusDown {
			one.Down = 1
		}
		if to == model.StatusDegraded {
			one.Degraded = 1
		}
		if o.OK {
			one.LatCount, one.LatSum, one.LatMin, one.LatMax = 1, o.Latency, o.Latency, o.Latency
		}
		h.Merge(one)
	}
	n := len(t.pending)
	t.mu.Unlock()

	// Las incidencias se escriben al momento (fuera del cerrojo).
	if opened != nil {
		t.writeIncident(opened, nil)
	}
	if closed != nil {
		t.writeIncident(nil, closed)
	}
	if n >= softLimit {
		select {
		case t.flushNow <- struct{}{}:
		default:
		}
	}
	return Transition{Changed: from != to, From: from, To: to, Opened: opened, Closed: closed}
}

func (t *Tracker) writeIncident(opened, closed *model.Incident) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if opened != nil {
		if err := t.st.OpenIncident(ctx, opened); err != nil {
			t.log.Error("could not save incident", "check_id", opened.CheckID, "error", err)
			return
		}
		// Guardar el ID en la incidencia abierta del estado.
		t.mu.Lock()
		if st, ok := t.states[opened.CheckID]; ok && st.Incident != nil && st.Incident.Start.Equal(opened.Start) {
			st.Incident.ID = opened.ID
		}
		t.mu.Unlock()
	}
	if closed != nil && closed.ID != 0 {
		if err := t.st.CloseIncident(ctx, closed.ID, closed.End, closed.LastError); err != nil {
			t.log.Error("could not close incident", "check_id", closed.CheckID, "error", err)
		}
	}
}

// Get devuelve el estado de un check.
func (t *Tracker) Get(id int64) (CheckState, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, ok := t.states[id]
	if !ok {
		return CheckState{Status: model.StatusUnknown}, false
	}
	return copyState(st), true
}

func copyState(st *CheckState) CheckState {
	c := *st
	c.Recent = append([]model.Result(nil), st.Recent...)
	if st.Incident != nil {
		inc := *st.Incident
		c.Incident = &inc
	}
	return c
}

// Snapshot devuelve una copia del estado de todos los checks.
func (t *Tracker) Snapshot() map[int64]CheckState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[int64]CheckState, len(t.states))
	for id, st := range t.states {
		out[id] = copyState(st)
	}
	return out
}

// PendingResults devuelve los resultados de un check aún no volcados.
func (t *Tracker) PendingResults(id int64) []model.Result {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []model.Result
	for _, r := range t.pending {
		if r.CheckID == id {
			out = append(out, r)
		}
	}
	return out
}

// PendingHourly devuelve los agregados de un check aún no volcados.
func (t *Tracker) PendingHourly(id int64) []model.Hourly {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []model.Hourly
	for k, h := range t.hourly {
		if k.check == id {
			out = append(out, *h)
		}
	}
	return out
}

// Pending devuelve cuántos resultados esperan a ser volcados.
func (t *Tracker) Pending() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.pending)
}

// Uptime devuelve el uptime de un check en cada ventana (UptimeWindows), como
// fracción entre 0 y 1; -1 si no hay datos.
func (t *Tracker) Uptime(id int64) []float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	now := t.now().UTC().Truncate(time.Hour)
	out := make([]float64, len(UptimeWindows))
	for i, w := range UptimeWindows {
		total := t.totals[w][id]
		since := now.Add(-w + time.Hour).Unix()
		for k, h := range t.hourly {
			if k.check == id && k.hour >= since {
				total.Merge(*h)
			}
		}
		if total.Total == 0 {
			out[i] = -1
		} else {
			out[i] = 1 - float64(total.Down)/float64(total.Total)
		}
	}
	return out
}

// RefreshUptime vuelve a leer del almacenamiento los agregados de cada
// ventana de uptime. Se llama al arrancar y tras cada volcado.
func (t *Tracker) RefreshUptime(ctx context.Context) error {
	now := t.now().UTC().Truncate(time.Hour)
	totals := map[time.Duration]map[int64]model.Hourly{}
	for _, w := range UptimeWindows {
		m, err := t.st.HourlyTotals(ctx, now.Add(-w+time.Hour))
		if err != nil {
			return err
		}
		totals[w] = m
	}
	t.mu.Lock()
	t.totals = totals
	t.mu.Unlock()
	return nil
}

// Flush vuelca los resultados y agregados pendientes en una sola
// transacción. Si falla, los conserva para el siguiente intento.
func (t *Tracker) Flush(ctx context.Context) error {
	t.flushMu.Lock()
	defer t.flushMu.Unlock()

	t.mu.Lock()
	batch := store.Batch{Results: t.pending}
	hourly := t.hourly
	t.pending = nil
	t.hourly = map[hourKey]*model.Hourly{}
	t.mu.Unlock()
	for _, h := range hourly {
		batch.Hourly = append(batch.Hourly, *h)
	}
	if len(batch.Results) == 0 && len(batch.Hourly) == 0 {
		return nil
	}

	start := time.Now()
	if err := t.st.WriteBatch(ctx, batch); err != nil {
		t.mu.Lock()
		t.pending = append(batch.Results, t.pending...)
		if over := len(t.pending) - hardLimit; over > 0 {
			t.pending = append([]model.Result(nil), t.pending[over:]...)
			t.dropped.Add(int64(over))
			t.log.Warn("result buffer full: dropping oldest results", "dropped", over)
		}
		for k, h := range hourly {
			if cur := t.hourly[k]; cur != nil {
				h.Merge(*cur)
			}
			t.hourly[k] = h
		}
		t.mu.Unlock()
		return err
	}
	t.flushed.Add(int64(len(batch.Results)))
	t.log.Debug("results flushed", "results", len(batch.Results), "hourly", len(batch.Hourly),
		"duration", time.Since(start).Round(time.Millisecond))
	if err := t.RefreshUptime(ctx); err != nil {
		t.log.Warn("could not refresh uptime", "error", err)
	}
	return nil
}

// RunFlusher vuelca el buffer cada interval (o antes si se llena) hasta que
// ctx se cancela. El volcado final de la parada lo hace el llamante con Flush.
func (t *Tracker) RunFlusher(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-t.flushNow:
		}
		if err := t.Flush(ctx); err != nil && ctx.Err() == nil {
			t.log.Error("could not flush results; will retry", "error", err, "pending", t.Pending())
		}
	}
}

// SetAggregatesOnly activa o desactiva el modo «solo agregados».
func (t *Tracker) SetAggregatesOnly(on bool) { t.AggregatesOnly.Store(on) }
