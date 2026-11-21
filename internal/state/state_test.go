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
)

type fakeWriter struct {
	mu      sync.Mutex
	batches [][]model.Result
	fail    bool
}

func (f *fakeWriter) InsertResults(_ context.Context, rs []model.Result) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("disco lleno")
	}
	f.batches = append(f.batches, append([]model.Result(nil), rs...))
	return nil
}

func (f *fakeWriter) count() (batches, results int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.batches {
		results += len(b)
	}
	return len(f.batches), results
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestTransiciones(t *testing.T) {
	tr := New(&fakeWriter{}, quiet())
	t0 := time.Unix(1_800_000_000, 0)
	steps := []struct {
		ok      bool
		changed bool
		from    model.Status
		to      model.Status
	}{
		{true, true, model.StatusUnknown, model.StatusUp},
		{true, false, model.StatusUp, model.StatusUp},
		{false, true, model.StatusUp, model.StatusDown},
		{false, false, model.StatusDown, model.StatusDown},
		{true, true, model.StatusDown, model.StatusUp},
	}
	for i, s := range steps {
		at := t0.Add(time.Duration(i) * time.Minute)
		got := tr.Record(model.Result{CheckID: 1, At: at, OK: s.ok})
		if got != (Transition{Changed: s.changed, From: s.from, To: s.to}) {
			t.Fatalf("paso %d: %+v", i, got)
		}
		st, _ := tr.Get(1)
		if s.changed && !st.Since.Equal(at) {
			t.Fatalf("paso %d: Since = %v, se esperaba %v", i, st.Since, at)
		}
	}
}

func TestRestoreYPausa(t *testing.T) {
	tr := New(&fakeWriter{}, quiet())
	checks := []model.Check{{ID: 1}, {ID: 2, Paused: true}, {ID: 3}}
	last := map[int64]model.Result{1: {CheckID: 1, OK: false}, 2: {CheckID: 2, OK: true}}
	tr.Restore(checks, last)

	want := map[int64]model.Status{1: model.StatusDown, 2: model.StatusPaused, 3: model.StatusUnknown}
	for id, s := range want {
		if st, _ := tr.Get(id); st.Status != s {
			t.Errorf("check %d: %s, se esperaba %s", id, st.Status, s)
		}
	}
	tr.SetPaused(2, false)
	if st, _ := tr.Get(2); st.Status != model.StatusUp {
		t.Errorf("tras reanudar: %s", st.Status)
	}
	tr.SetPaused(3, true)
	tr.SetPaused(3, false)
	if st, _ := tr.Get(3); st.Status != model.StatusUnknown {
		t.Errorf("reanudar sin resultados: %s", st.Status)
	}
	tr.Remove(1)
	if _, ok := tr.Get(1); ok {
		t.Error("Remove no borró el estado")
	}
	if len(tr.Snapshot()) != 2 {
		t.Errorf("Snapshot = %v", tr.Snapshot())
	}
}

func TestFlushEnLote(t *testing.T) {
	w := &fakeWriter{}
	tr := New(w, quiet())
	for i := 0; i < 10; i++ {
		tr.Record(model.Result{CheckID: 1, At: time.Unix(int64(i), 0), OK: true})
	}
	if b, _ := w.count(); b != 0 {
		t.Fatal("no debe escribirse nada antes del volcado")
	}
	if err := tr.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, r := w.count(); b != 1 || r != 10 {
		t.Fatalf("lotes=%d resultados=%d, se esperaba 1 lote de 10", b, r)
	}
	if tr.Pending() != 0 {
		t.Fatal("quedan pendientes tras volcar")
	}
	if err := tr.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := w.count(); b != 1 {
		t.Fatal("un volcado vacío no debe escribir")
	}
}

func TestFlushFallidoConserva(t *testing.T) {
	w := &fakeWriter{fail: true}
	tr := New(w, quiet())
	tr.Record(model.Result{CheckID: 1, At: time.Unix(1, 0)})
	tr.Record(model.Result{CheckID: 1, At: time.Unix(2, 0)})
	if err := tr.Flush(context.Background()); err == nil {
		t.Fatal("se esperaba error")
	}
	tr.Record(model.Result{CheckID: 1, At: time.Unix(3, 0)})
	if tr.Pending() != 3 {
		t.Fatalf("pendientes = %d, se esperaban 3", tr.Pending())
	}
	w.fail = false
	if err := tr.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, r := w.count(); r != 3 || !w.batches[0][0].At.Equal(time.Unix(1, 0)) {
		t.Fatalf("orden o cantidad incorrectos: %+v", w.batches)
	}
}

func TestFlushAnticipado(t *testing.T) {
	w := &fakeWriter{}
	tr := New(w, quiet())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.RunFlusher(ctx, time.Hour)
	for i := 0; i < softLimit; i++ {
		tr.Record(model.Result{CheckID: 1, At: time.UnixMilli(int64(i))})
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, r := w.count(); r == softLimit {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("al llenarse el buffer no se adelantó el volcado")
}
