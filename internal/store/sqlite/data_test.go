package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"qaphi/internal/model"
	"qaphi/internal/module"
	"qaphi/internal/store"
)

func TestChecksCRUD(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	c := model.Check{Name: "Web", Type: "http", Group: "Servicios", Interval: 90 * time.Second,
		Params: module.Values{"url": "https://ejemplo.org", "timeout": "10s"}}
	if err := s.CreateCheck(ctx, &c); err != nil {
		t.Fatal(err)
	}
	if c.ID == 0 || c.CreatedAt.IsZero() {
		t.Fatalf("no se asignaron ID o fechas: %+v", c)
	}
	got, err := s.GetCheck(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Web" || got.Interval != 90*time.Second || got.Params["url"] != "https://ejemplo.org" || got.Paused {
		t.Fatalf("check leído = %+v", got)
	}

	got.Name, got.Paused, got.Params["timeout"] = "Web 2", true, "5s"
	if err := s.UpdateCheck(ctx, &got); err != nil {
		t.Fatal(err)
	}
	b := model.Check{Name: "Aaa", Type: "tcp", Group: "Red", Interval: time.Minute}
	if err := s.CreateCheck(ctx, &b); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListChecks(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListChecks = %v, %v", list, err)
	}
	if list[0].Group != "Red" || list[1].Name != "Web 2" || !list[1].Paused || list[1].Params["timeout"] != "5s" {
		t.Fatalf("orden o contenido incorrecto: %+v", list)
	}

	if err := s.DeleteCheck(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCheck(ctx, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetCheck tras borrar: %v", err)
	}
	if err := s.DeleteCheck(ctx, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteCheck de inexistente: %v", err)
	}
	missing := model.Check{ID: 999, Name: "x", Type: "tcp", Interval: time.Minute}
	if err := s.UpdateCheck(ctx, &missing); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateCheck de inexistente: %v", err)
	}
}

func TestResults(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	a := model.Check{Name: "A", Type: "tcp", Interval: time.Minute}
	b := model.Check{Name: "B", Type: "tcp", Interval: time.Minute}
	s.CreateCheck(ctx, &a)
	s.CreateCheck(ctx, &b)

	t0 := time.UnixMilli(1_800_000_000_000)
	batch := []model.Result{
		{CheckID: a.ID, At: t0, OK: true, Latency: 1500 * time.Microsecond, Message: "ok"},
		{CheckID: a.ID, At: t0.Add(time.Minute), OK: false, Latency: 3 * time.Second, Message: "tiempo agotado"},
		{CheckID: b.ID, At: t0, OK: true, Latency: time.Millisecond},
		{CheckID: 12345, At: t0, OK: true}, // check inexistente: se descarta
	}
	if err := s.InsertResults(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertResults(ctx, nil); err != nil {
		t.Fatal(err)
	}
	last, err := s.LastResults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 2 {
		t.Fatalf("LastResults = %v", last)
	}
	ra := last[a.ID]
	if ra.OK || ra.Message != "tiempo agotado" || ra.Latency != 3*time.Second || !ra.At.Equal(t0.Add(time.Minute)) {
		t.Fatalf("último de A = %+v", ra)
	}

	// Al borrar un check se borran sus resultados.
	s.DeleteCheck(ctx, a.ID)
	last, _ = s.LastResults(ctx)
	if _, ok := last[a.ID]; ok || len(last) != 1 {
		t.Fatalf("tras borrar A: %v", last)
	}
}

func TestSessions(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	viva := model.Session{IDHash: []byte("viva"), CSRF: "c1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	caducada := model.Session{IDHash: []byte("vieja"), CSRF: "c2", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
	for _, x := range []model.Session{viva, caducada} {
		if err := s.CreateSession(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetSession(ctx, []byte("viva"))
	if err != nil || got.CSRF != "c1" || !got.ExpiresAt.Equal(viva.ExpiresAt) {
		t.Fatalf("GetSession = %+v, %v", got, err)
	}
	if n, err := s.DeleteExpiredSessions(ctx, now); err != nil || n != 1 {
		t.Fatalf("DeleteExpiredSessions = %d, %v", n, err)
	}
	if _, err := s.GetSession(ctx, []byte("vieja")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("la sesión caducada debería haberse borrado: %v", err)
	}
	s.DeleteSession(ctx, []byte("viva"))
	if _, err := s.GetSession(ctx, []byte("viva")); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("DeleteSession no borró")
	}
	s.CreateSession(ctx, viva)
	s.DeleteAllSessions(ctx)
	if _, err := s.GetSession(ctx, []byte("viva")); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("DeleteAllSessions no borró")
	}
}

// TestBloqueoExclusivo: una segunda apertura de la misma base de datos falla
// mientras la primera sigue abierta, y funciona cuando se cierra.
func TestBloqueoExclusivo(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "qaphi.db")
	s1, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + "-shm"); !os.IsNotExist(err) {
		t.Errorf("en modo exclusivo no debería existir el fichero -shm (err=%v)", err)
	}
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := Open(ctx2, path); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("segunda apertura: error = %v, se esperaba ErrLocked", err)
	}
	s1.Close()
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("apertura tras cerrar: %v", err)
	}
	s2.Close()
}
