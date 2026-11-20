// Package state mantiene en memoria el estado actual de cada check y el
// buffer de resultados pendientes de escribir. Los resultados se vuelcan a
// disco en lote (una transacción cada intervalo de volcado) para reducir las
// escrituras en almacenamiento delicado.
package state

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"qaphi/internal/model"
)

// ResultWriter es la parte del almacenamiento que necesita el buffer.
type ResultWriter interface {
	InsertResults(ctx context.Context, rs []model.Result) error
}

// CheckState es el estado actual de un check.
type CheckState struct {
	Status  model.Status
	Last    model.Result // válido si HasLast
	HasLast bool
	Since   time.Time // momento del último cambio de estado (cero si desconocido)
}

// Transition describe un cambio de estado provocado por un resultado.
type Transition struct {
	Changed bool
	From    model.Status
	To      model.Status
}

// Límites del buffer: al superar softLimit se adelanta el volcado; si los
// volcados fallan (disco lleno…), por encima de hardLimit se descartan los
// resultados más antiguos para acotar la memoria.
const (
	softLimit = 2000
	hardLimit = 20000
)

// Tracker es el estado en memoria y el buffer de resultados.
type Tracker struct {
	log *slog.Logger
	w   ResultWriter

	mu      sync.RWMutex
	states  map[int64]*CheckState
	pending []model.Result

	flushNow chan struct{}
	flushMu  sync.Mutex // serializa los volcados

	// Contadores (atomic.Int64 se alinea solo, también en ARM de 32 bits).
	flushed atomic.Int64
	dropped atomic.Int64
}

// New crea un Tracker vacío.
func New(w ResultWriter, log *slog.Logger) *Tracker {
	return &Tracker{
		log:      log,
		w:        w,
		states:   map[int64]*CheckState{},
		flushNow: make(chan struct{}, 1),
	}
}

// Restore inicializa el estado a partir de los checks configurados y del
// último resultado guardado de cada uno (arranque).
func (t *Tracker) Restore(checks []model.Check, last map[int64]model.Result) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range checks {
		st := &CheckState{Status: model.StatusUnknown}
		if r, ok := last[c.ID]; ok {
			st.Last, st.HasLast = r, true
			st.Status = statusOf(r)
		}
		if c.Paused {
			st.Status = model.StatusPaused
		}
		t.states[c.ID] = st
	}
}

func statusOf(r model.Result) model.Status {
	if r.OK {
		return model.StatusUp
	}
	return model.StatusDown
}

// Record registra un resultado: actualiza el estado y lo añade al buffer.
func (t *Tracker) Record(r model.Result) Transition {
	t.mu.Lock()
	st, ok := t.states[r.CheckID]
	if !ok {
		st = &CheckState{Status: model.StatusUnknown}
		t.states[r.CheckID] = st
	}
	from := st.Status
	to := statusOf(r)
	st.Last, st.HasLast, st.Status = r, true, to
	if from != to {
		st.Since = r.At
	}
	t.pending = append(t.pending, r)
	n := len(t.pending)
	t.mu.Unlock()

	if n >= softLimit {
		select {
		case t.flushNow <- struct{}{}:
		default:
		}
	}
	return Transition{Changed: from != to, From: from, To: to}
}

// SetPaused marca un check como pausado o lo devuelve a su estado según el
// último resultado.
func (t *Tracker) SetPaused(id int64, paused bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.states[id]
	if !ok {
		st = &CheckState{Status: model.StatusUnknown}
		t.states[id] = st
	}
	switch {
	case paused:
		st.Status = model.StatusPaused
	case st.HasLast:
		st.Status = statusOf(st.Last)
	default:
		st.Status = model.StatusUnknown
	}
}

// Remove olvida un check (borrado). Sus resultados pendientes se descartarán
// al volcar, porque el almacenamiento ignora los de checks inexistentes.
func (t *Tracker) Remove(id int64) {
	t.mu.Lock()
	delete(t.states, id)
	t.mu.Unlock()
}

// Get devuelve el estado de un check.
func (t *Tracker) Get(id int64) (CheckState, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, ok := t.states[id]
	if !ok {
		return CheckState{Status: model.StatusUnknown}, false
	}
	return *st, true
}

// Snapshot devuelve una copia del estado de todos los checks.
func (t *Tracker) Snapshot() map[int64]CheckState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[int64]CheckState, len(t.states))
	for id, st := range t.states {
		out[id] = *st
	}
	return out
}

// Pending devuelve cuántos resultados esperan a ser volcados.
func (t *Tracker) Pending() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.pending)
}

// Flush vuelca los resultados pendientes en una sola transacción. Si falla,
// los conserva para el siguiente intento.
func (t *Tracker) Flush(ctx context.Context) error {
	t.flushMu.Lock()
	defer t.flushMu.Unlock()

	t.mu.Lock()
	batch := t.pending
	t.pending = nil
	t.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}

	start := time.Now()
	if err := t.w.InsertResults(ctx, batch); err != nil {
		t.mu.Lock()
		t.pending = append(batch, t.pending...)
		if over := len(t.pending) - hardLimit; over > 0 {
			t.pending = append([]model.Result(nil), t.pending[over:]...)
			t.dropped.Add(int64(over))
			t.log.Warn("buffer de resultados lleno: se descartan los más antiguos", "descartados", over)
		}
		t.mu.Unlock()
		return err
	}
	t.flushed.Add(int64(len(batch)))
	t.log.Debug("resultados volcados", "cantidad", len(batch), "duración", time.Since(start).Round(time.Millisecond))
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
			t.log.Error("no se pudieron volcar los resultados; se reintentará", "error", err, "pendientes", t.Pending())
		}
	}
}
