package state

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

type fakeStore struct {
	mu        sync.Mutex
	batches   []store.Batch
	fail      bool
	nextID    int64
	opened    []model.Incident
	closed    map[int64]time.Time
	totals    map[int64]model.Hourly
	totalsReq []time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{closed: map[int64]time.Time{}, totals: map[int64]model.Hourly{}}
}

func (f *fakeStore) WriteBatch(_ context.Context, b store.Batch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("disco lleno")
	}
	f.batches = append(f.batches, b)
	return nil
}

func (f *fakeStore) OpenIncident(_ context.Context, i *model.Incident) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	i.ID = f.nextID
	f.opened = append(f.opened, *i)
	return nil
}

func (f *fakeStore) CloseIncident(_ context.Context, id int64, end time.Time, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed[id] = end
	return nil
}

func (f *fakeStore) HourlyTotals(_ context.Context, since time.Time) (map[int64]model.Hourly, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.totalsReq = append(f.totalsReq, since)
	return f.totals, nil
}

func (f *fakeStore) counts() (batches, results, hourly int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.batches {
		results += len(b.Results)
		hourly += len(b.Hourly)
	}
	return len(f.batches), results, hourly
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var t0 = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

func obs(id int64, i int, ok bool) Observation {
	return Observation{CheckID: id, At: t0.Add(time.Duration(i) * time.Minute), OK: ok, Latency: 10 * time.Millisecond,
		Message: msg.New("check.msg.refused")}
}

// TestConfirmacion: tabla de transiciones con umbral 3.
func TestConfirmacion(t *testing.T) {
	st := newFakeStore()
	tr := New(st, quiet(), nil)
	tr.SetCheck(model.Check{ID: 1, FailThreshold: 3})
	steps := []struct {
		ok      bool
		to      model.Status
		changed bool
		opened  bool
		closed  bool
	}{
		{true, model.StatusUp, true, false, false},
		{false, model.StatusPending, true, false, false},
		{false, model.StatusPending, false, false, false},
		{false, model.StatusDown, true, true, false}, // tercer fallo: caído
		{false, model.StatusDown, false, false, false},
		{true, model.StatusUp, true, false, true},
		{false, model.StatusPending, true, false, false},
		{true, model.StatusUp, true, false, false}, // fallo aislado: sin incidencia
	}
	for i, s := range steps {
		tran := tr.Record(obs(1, i, s.ok))
		if tran.To != s.to || tran.Changed != s.changed || (tran.Opened != nil) != s.opened || (tran.Closed != nil) != s.closed {
			t.Fatalf("paso %d: %+v", i, tran)
		}
	}
	if len(st.opened) != 1 || !st.opened[0].Start.Equal(t0.Add(time.Minute)) {
		t.Fatalf("la incidencia debe empezar en el primer fallo de la racha: %+v", st.opened)
	}
	if end := st.closed[1]; !end.Equal(t0.Add(5 * time.Minute)) {
		t.Fatalf("fin de la incidencia = %v", end)
	}
	cs, _ := tr.Get(1)
	if len(cs.Recent) != len(steps) || cs.Recent[3].State != model.StatusDown || cs.Recent[1].State != model.StatusPending {
		t.Fatalf("pulsos recientes = %+v", cs.Recent)
	}
}

func TestUmbralUnoYLento(t *testing.T) {
	tr := New(newFakeStore(), quiet(), nil)
	tr.SetCheck(model.Check{ID: 1, FailThreshold: 1, DegradedAfter: 50 * time.Millisecond})
	if tran := tr.Record(obs(1, 0, false)); tran.To != model.StatusDown || tran.Opened == nil {
		t.Fatalf("umbral 1: %+v", tran)
	}
	slow := obs(1, 1, true)
	slow.Latency = 80 * time.Millisecond
	if tran := tr.Record(slow); tran.To != model.StatusDegraded || tran.Closed == nil {
		t.Fatalf("lento: %+v", tran)
	}
	warn := obs(1, 2, true)
	warn.Degraded = true
	warn.CertExpiry = t0.Add(72 * time.Hour)
	if tran := tr.Record(warn); tran.To != model.StatusDegraded {
		t.Fatalf("aviso: %+v", tran)
	}
	if cs, _ := tr.Get(1); !cs.CertExpiry.Equal(t0.Add(72 * time.Hour)) {
		t.Error("no se guardó la caducidad del certificado")
	}
	if tran := tr.Record(obs(1, 3, true)); tran.To != model.StatusUp {
		t.Fatalf("rápido: %+v", tran)
	}
}

func TestSinPermisos(t *testing.T) {
	tr := New(newFakeStore(), quiet(), nil)
	tr.SetCheck(model.Check{ID: 1, FailThreshold: 1})
	np := obs(1, 0, false)
	np.NoPerm = true
	if tran := tr.Record(np); tran.To != model.StatusNoPerm || tran.Opened != nil {
		t.Fatalf("sin permisos: %+v", tran)
	}
	if hs := tr.PendingHourly(1); len(hs) != 0 {
		t.Error("los pulsos sin permisos no cuentan para el uptime")
	}
}

func TestRestore(t *testing.T) {
	tr := New(newFakeStore(), quiet(), nil)
	checks := []model.Check{{ID: 1, FailThreshold: 3}, {ID: 2, Paused: true}, {ID: 3}, {ID: 4, FailThreshold: 3}}
	recent := map[int64][]model.Result{
		1: {{CheckID: 1, At: t0, OK: true, State: model.StatusUp}, {CheckID: 1, At: t0.Add(time.Minute), OK: false, State: model.StatusPending}},
		2: {{CheckID: 2, At: t0, OK: true, State: model.StatusUp}},
		4: {{CheckID: 4, At: t0, OK: false, State: model.StatusDown}},
	}
	open := map[int64]model.Incident{4: {ID: 7, CheckID: 4, Start: t0.Add(-time.Hour)}}
	tr.Restore(checks, recent, open)

	want := map[int64]model.Status{1: model.StatusPending, 2: model.StatusPaused, 3: model.StatusUnknown, 4: model.StatusDown}
	for id, s := range want {
		if st, _ := tr.Get(id); st.Status != s {
			t.Errorf("check %d: %s, se esperaba %s", id, st.Status, s)
		}
	}
	// Check 1 llevaba un fallo: dos más lo dan por caído.
	tr.Record(obs(1, 2, false))
	if tran := tr.Record(obs(1, 3, false)); tran.To != model.StatusDown {
		t.Fatalf("tras restaurar, la cuenta de fallos debe continuar: %+v", tran)
	}
	// Check 4 tenía una incidencia abierta: al recuperarse se cierra esa.
	if tran := tr.Record(obs(4, 1, true)); tran.Closed == nil || tran.Closed.ID != 7 {
		t.Fatalf("cierre de la incidencia restaurada: %+v", tran)
	}
	// Al reanudar un pausado, su estado es desconocido hasta la próxima
	// comprobación; los pulsos anteriores se conservan.
	tr.SetCheck(model.Check{ID: 2})
	if st, _ := tr.Get(2); st.Status != model.StatusUnknown || len(st.Recent) != 1 {
		t.Errorf("reanudado: %s", st.Status)
	}
	tr.Remove(1)
	if _, ok := tr.Get(1); ok {
		t.Error("Remove no borró el estado")
	}
}

func TestVolcadoYAgregados(t *testing.T) {
	st := newFakeStore()
	tr := New(st, quiet(), func() time.Time { return t0.Add(90 * time.Minute) })
	tr.SetCheck(model.Check{ID: 1, FailThreshold: 1})
	for i := 0; i < 4; i++ {
		tr.Record(obs(1, i*20, i != 2)) // a las 10:00, 10:20, 10:40 (fallo) y 11:00
	}
	if hs := tr.PendingHourly(1); len(hs) != 2 {
		t.Fatalf("agregados pendientes = %+v", hs)
	}
	if len(tr.PendingResults(1)) != 4 {
		t.Fatal("resultados pendientes")
	}
	// Uptime antes de volcar: 1 caído de 4.
	if up := tr.Uptime(1); up[0] != 0.75 {
		t.Fatalf("uptime = %v", up)
	}
	if err := tr.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, r, h := st.counts()
	if b != 1 || r != 4 || h != 2 {
		t.Fatalf("lotes=%d resultados=%d agregados=%d", b, r, h)
	}
	var h10 model.Hourly
	for _, x := range st.batches[0].Hourly {
		if x.Hour.Equal(t0) {
			h10 = x
		}
	}
	if h10.Total != 3 || h10.Down != 1 || h10.LatCount != 2 || h10.LatMin != 10*time.Millisecond {
		t.Fatalf("agregado de las 10:00 = %+v", h10)
	}
	if len(st.totalsReq) != len(UptimeWindows) {
		t.Error("tras volcar debe refrescarse el uptime")
	}
	if tr.Pending() != 0 || len(tr.PendingHourly(1)) != 0 {
		t.Fatal("quedan pendientes tras volcar")
	}
	// Sin datos: -1.
	if up := tr.Uptime(99); up[0] != -1 {
		t.Errorf("uptime sin datos = %v", up)
	}
}

func TestSoloAgregados(t *testing.T) {
	st := newFakeStore()
	tr := New(st, quiet(), nil)
	tr.AggregatesOnly.Store(true)
	tr.Record(obs(1, 0, true))
	if tr.Pending() != 0 {
		t.Fatal("en modo solo agregados no se guardan resultados en bruto")
	}
	if cs, _ := tr.Get(1); len(cs.Recent) != 1 {
		t.Fatal("los pulsos recientes siguen en memoria")
	}
	tr.Flush(context.Background())
	if _, r, h := st.counts(); r != 0 || h != 1 {
		t.Fatalf("resultados=%d agregados=%d", r, h)
	}
}

func TestVolcadoFallidoConserva(t *testing.T) {
	st := newFakeStore()
	st.fail = true
	tr := New(st, quiet(), nil)
	tr.Record(obs(1, 0, true))
	tr.Record(obs(1, 1, true))
	if err := tr.Flush(context.Background()); err == nil {
		t.Fatal("se esperaba error")
	}
	tr.Record(obs(1, 2, true))
	if tr.Pending() != 3 || len(tr.PendingHourly(1)) != 1 || tr.PendingHourly(1)[0].Total != 3 {
		t.Fatalf("pendientes = %d, agregados = %+v", tr.Pending(), tr.PendingHourly(1))
	}
	st.fail = false
	if err := tr.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, r, _ := st.counts(); r != 3 || !st.batches[0].Results[0].At.Equal(t0) {
		t.Fatalf("orden o cantidad incorrectos: %+v", st.batches)
	}
}

func TestPulsosRecientesAcotados(t *testing.T) {
	tr := New(newFakeStore(), quiet(), nil)
	for i := 0; i < RecentBeats+30; i++ {
		tr.Record(obs(1, i, true))
	}
	cs, _ := tr.Get(1)
	if len(cs.Recent) != RecentBeats || !cs.Recent[RecentBeats-1].At.Equal(t0.Add(time.Duration(RecentBeats+29)*time.Minute)) {
		t.Fatalf("pulsos recientes: %d", len(cs.Recent))
	}
}

func TestVolcadoAnticipado(t *testing.T) {
	st := newFakeStore()
	tr := New(st, quiet(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.RunFlusher(ctx, time.Hour)
	for i := 0; i < softLimit; i++ {
		tr.Record(Observation{CheckID: 1, At: t0.Add(time.Duration(i) * time.Second), OK: true})
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, r, _ := st.counts(); r == softLimit {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("al llenarse el buffer no se adelantó el volcado")
}

// TestPausaCierraIncidencia: pausar un check caído cierra su incidencia, tanto
// en caliente como al arrancar con una incidencia abierta de un check pausado.
func TestPausaCierraIncidencia(t *testing.T) {
	st := newFakeStore()
	pausedAt := t0.Add(10 * time.Minute)
	tr := New(st, quiet(), func() time.Time { return pausedAt })
	tr.SetCheck(model.Check{ID: 1, FailThreshold: 1})
	if tran := tr.Record(obs(1, 0, false)); tran.Opened == nil {
		t.Fatalf("debía abrir incidencia: %+v", tran)
	}
	tr.SetCheck(model.Check{ID: 1, FailThreshold: 1, Paused: true})
	if end, ok := st.closed[1]; !ok || !end.Equal(pausedAt) {
		t.Fatalf("la pausa debe cerrar la incidencia en %v: %v", pausedAt, st.closed)
	}
	cs, _ := tr.Get(1)
	if cs.Status != model.StatusPaused || cs.Incident != nil || cs.Fails != 0 {
		t.Fatalf("estado tras pausar = %+v", cs)
	}
	// Al reanudar, un fallo vuelve a abrir una incidencia nueva.
	tr.SetCheck(model.Check{ID: 1, FailThreshold: 1})
	if tran := tr.Record(obs(1, 20, false)); tran.Opened == nil || !tran.Opened.Start.Equal(t0.Add(20*time.Minute)) {
		t.Fatalf("tras reanudar = %+v", tran)
	}

	st2 := newFakeStore()
	tr2 := New(st2, quiet(), func() time.Time { return pausedAt })
	tr2.Restore([]model.Check{{ID: 7, Paused: true}}, nil,
		map[int64]model.Incident{7: {ID: 42, CheckID: 7, Start: t0}})
	if end, ok := st2.closed[42]; !ok || !end.Equal(pausedAt) {
		t.Fatalf("al arrancar debe cerrar la incidencia del check pausado: %v", st2.closed)
	}
	if cs, _ := tr2.Get(7); cs.Status != model.StatusPaused || cs.Incident != nil {
		t.Fatalf("estado restaurado = %+v", cs)
	}
}
