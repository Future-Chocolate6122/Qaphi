package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/checks"
	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/state"
)

// fakeChecker ejecuta fn y mide la concurrencia.
type fakeChecker struct {
	fn      func(ctx context.Context) checks.Result
	timeout time.Duration
	active  atomic.Int64
	maxSeen atomic.Int64
}

func (f *fakeChecker) Check(ctx context.Context, _ module.Values) checks.Result {
	n := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	return f.fn(ctx)
}
func (f *fakeChecker) Validate(module.Values) module.Errors { return nil }
func (f *fakeChecker) Target(module.Values) string          { return "" }
func (f *fakeChecker) Timeout(module.Values) time.Duration {
	if f.timeout > 0 {
		return f.timeout
	}
	return time.Second
}

type fakeRecorder struct {
	mu sync.Mutex
	rs []model.Result
}

func (r *fakeRecorder) Record(res model.Result) state.Transition {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rs = append(r.rs, res)
	return state.Transition{}
}

func (r *fakeRecorder) results(id int64) []model.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []model.Result
	for _, x := range r.rs {
		if x.CheckID == id {
			out = append(out, x)
		}
	}
	return out
}

type fakeClock struct{ ok atomic.Bool }

func (c *fakeClock) Valid() bool { return c.ok.Load() }

func newTest(t *testing.T, conc int, impl checks.Checker, grace ...time.Duration) (*Scheduler, *fakeRecorder, *fakeClock, context.CancelFunc, chan struct{}) {
	t.Helper()
	rec := &fakeRecorder{}
	var g time.Duration
	if len(grace) > 0 {
		g = grace[0]
	}
	clk := &fakeClock{}
	clk.ok.Store(true)
	s := New(Options{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Recorder: rec, Clock: clk, Concurrency: conc, Grace: g,
		Lookup: func(typ string) (checks.Checker, bool) {
			if typ == "fake" {
				return impl, true
			}
			return nil, false
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("Run no terminó tras cancelar")
		}
	})
	return s, rec, clk, cancel, done
}

func check(id int64, interval time.Duration) model.Check {
	return model.Check{ID: id, Name: "c", Type: "fake", Interval: interval}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no se cumplió: %s", what)
}

func ok(context.Context) checks.Result { return checks.Result{OK: true, Latency: time.Millisecond} }

func TestEjecucionPeriodica(t *testing.T) {
	s, rec, _, _, _ := newTest(t, 0, &fakeChecker{fn: ok})
	s.Load([]model.Check{check(1, 30*time.Millisecond)})
	eventually(t, "al menos 4 ejecuciones", func() bool { return len(rec.results(1)) >= 4 })
	rs := rec.results(1)
	if !rs[0].OK || rs[0].CheckID != 1 || rs[0].At.IsZero() {
		t.Fatalf("resultado mal formado: %+v", rs[0])
	}
}

func TestJitterInicial(t *testing.T) {
	s, rec, _, _, _ := newTest(t, 0, &fakeChecker{fn: ok})
	start := time.Now()
	var cs []model.Check
	for i := int64(1); i <= 20; i++ {
		cs = append(cs, check(i, 400*time.Millisecond))
	}
	s.Load(cs)
	eventually(t, "todos ejecutados una vez", func() bool {
		for i := int64(1); i <= 20; i++ {
			if len(rec.results(i)) == 0 {
				return false
			}
		}
		return true
	})
	// Con jitter, las primeras ejecuciones se reparten en el intervalo.
	var first, last time.Time
	for i := int64(1); i <= 20; i++ {
		at := rec.results(i)[0].At
		if first.IsZero() || at.Before(first) {
			first = at
		}
		if at.After(last) {
			last = at
		}
	}
	if last.Sub(first) < 50*time.Millisecond {
		t.Fatalf("las primeras ejecuciones no se repartieron (%v)", last.Sub(first))
	}
	if last.Sub(start) > 600*time.Millisecond {
		t.Fatalf("alguna primera ejecución superó el intervalo: %v", last.Sub(start))
	}
}

func TestSinSolapamiento(t *testing.T) {
	f := &fakeChecker{fn: func(ctx context.Context) checks.Result {
		time.Sleep(80 * time.Millisecond)
		return checks.Result{OK: true}
	}}
	s, rec, _, _, _ := newTest(t, 0, f)
	s.Load([]model.Check{check(1, 5*time.Millisecond)})
	eventually(t, "3 ejecuciones", func() bool { return len(rec.results(1)) >= 3 })
	if m := f.maxSeen.Load(); m != 1 {
		t.Fatalf("un check se solapó consigo mismo (máximo simultáneo %d)", m)
	}
}

func TestConcurrenciaMaxima(t *testing.T) {
	f := &fakeChecker{fn: func(ctx context.Context) checks.Result {
		time.Sleep(50 * time.Millisecond)
		return checks.Result{OK: true}
	}}
	s, rec, _, _, _ := newTest(t, 2, f)
	var cs []model.Check
	for i := int64(1); i <= 6; i++ {
		cs = append(cs, check(i, 10*time.Millisecond))
	}
	s.Load(cs)
	eventually(t, "todos ejecutados", func() bool {
		for i := int64(1); i <= 6; i++ {
			if len(rec.results(i)) == 0 {
				return false
			}
		}
		return true
	})
	if m := f.maxSeen.Load(); m > 2 {
		t.Fatalf("concurrencia %d supera el máximo de 2", m)
	}
}

func TestPanicoRecuperado(t *testing.T) {
	s, rec, _, _, _ := newTest(t, 0, &fakeChecker{fn: func(context.Context) checks.Result { panic("¡bum!") }})
	s.Load([]model.Check{check(1, 20*time.Millisecond)})
	eventually(t, "resultado tras el pánico", func() bool { return len(rec.results(1)) >= 2 })
	if r := rec.results(1)[0]; r.OK || r.Message != MsgPanic {
		t.Fatalf("resultado = %+v", r)
	}
}

func TestCheckQueIgnoraElContexto(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	f := &fakeChecker{timeout: 20 * time.Millisecond, fn: func(context.Context) checks.Result {
		<-block // ignora el contexto
		return checks.Result{OK: true}
	}}
	s, rec, _, _, _ := newTest(t, 0, f, 20*time.Millisecond)
	s.Load([]model.Check{check(1, 10*time.Millisecond)})
	eventually(t, "se abandona y se registra fallo", func() bool { return len(rec.results(1)) >= 2 })
	if r := rec.results(1)[0]; r.OK || r.Message != MsgTimeout {
		t.Fatalf("resultado = %+v", r)
	}
}

func TestRelojNoValido(t *testing.T) {
	s, rec, clk, _, _ := newTest(t, 0, &fakeChecker{fn: ok})
	clk.ok.Store(false)
	s.Load([]model.Check{check(1, 10*time.Millisecond)})
	time.Sleep(100 * time.Millisecond)
	if n := len(rec.results(1)); n != 0 {
		t.Fatalf("con hora no válida no debe ejecutarse nada (%d resultados)", n)
	}
	clk.ok.Store(true)
	s.signal()
	eventually(t, "se reanuda con hora válida", func() bool { return len(rec.results(1)) > 0 })
}

func TestPausaBorradoYTipoDesconocido(t *testing.T) {
	s, rec, _, _, _ := newTest(t, 0, &fakeChecker{fn: ok})
	paused := check(1, 10*time.Millisecond)
	paused.Paused = true
	unknown := check(2, 10*time.Millisecond)
	unknown.Type = "inexistente"
	s.Load([]model.Check{paused, unknown, check(3, 10*time.Millisecond)})
	eventually(t, "el activo se ejecuta", func() bool { return len(rec.results(3)) >= 3 })
	if len(rec.results(1)) != 0 || len(rec.results(2)) != 0 {
		t.Fatal("el pausado o el de tipo desconocido se ejecutaron")
	}

	s.Remove(3)
	time.Sleep(30 * time.Millisecond)
	n := len(rec.results(3))
	time.Sleep(80 * time.Millisecond)
	if len(rec.results(3)) > n {
		t.Fatal("un check borrado siguió ejecutándose")
	}

	paused.Paused = false
	s.Set(paused)
	eventually(t, "al reanudar se ejecuta", func() bool { return len(rec.results(1)) > 0 })
}

func TestParadaEsperaEjecuciones(t *testing.T) {
	var finished atomic.Bool
	f := &fakeChecker{timeout: time.Second, fn: func(ctx context.Context) checks.Result {
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
		}
		finished.Store(true)
		return checks.Result{OK: true}
	}}
	s, _, _, cancel, done := newTest(t, 0, f)
	s.Set(check(1, time.Hour))
	eventually(t, "ejecución en curso", func() bool { return f.active.Load() == 1 })
	cancel()
	<-done
	if !finished.Load() {
		t.Fatal("Run terminó sin esperar a la ejecución en curso")
	}
}
