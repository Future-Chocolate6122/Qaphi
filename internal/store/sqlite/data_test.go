package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/store"
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

func TestResultadosYAgregados(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	a := model.Check{Name: "A", Type: "tcp", Interval: time.Minute, FailThreshold: 2, DegradedAfter: 300 * time.Millisecond}
	b := model.Check{Name: "B", Type: "tcp", Interval: time.Minute}
	s.CreateCheck(ctx, &a)
	s.CreateCheck(ctx, &b)
	if got, _ := s.GetCheck(ctx, a.ID); got.FailThreshold != 2 || got.DegradedAfter != 300*time.Millisecond {
		t.Fatalf("umbrales no guardados: %+v", got)
	}
	if got, _ := s.GetCheck(ctx, b.ID); got.Threshold() != model.DefaultFailThreshold {
		t.Fatalf("umbral por defecto: %+v", got)
	}

	hour := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	t0 := hour.Add(5 * time.Minute)
	batch := store.Batch{
		Results: []model.Result{
			{CheckID: a.ID, At: t0, OK: true, State: model.StatusUp, Latency: 1500 * time.Microsecond, Message: "ok"},
			{CheckID: a.ID, At: t0.Add(time.Minute), OK: false, State: model.StatusPending, Message: "fallo"},
			{CheckID: a.ID, At: t0.Add(2 * time.Minute), OK: false, State: model.StatusDown, Message: "fallo"},
			{CheckID: b.ID, At: t0, OK: true, State: model.StatusDegraded, Latency: time.Second},
			{CheckID: 12345, At: t0, OK: true}, // check inexistente: se descarta
		},
		Hourly: []model.Hourly{
			{CheckID: a.ID, Hour: hour, Total: 3, Down: 1, LatCount: 1, LatSum: 1500 * time.Microsecond, LatMin: 1500 * time.Microsecond, LatMax: 1500 * time.Microsecond},
			{CheckID: 12345, Hour: hour, Total: 1},
		},
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	// Un segundo lote para la misma hora se suma.
	if err := s.WriteBatch(ctx, store.Batch{Hourly: []model.Hourly{
		{CheckID: a.ID, Hour: hour, Total: 2, Degraded: 1, LatCount: 2, LatSum: 3 * time.Millisecond, LatMin: time.Millisecond, LatMax: 2 * time.Millisecond},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteBatch(ctx, store.Batch{}); err != nil {
		t.Fatal(err)
	}

	recent, err := s.RecentResults(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent[a.ID]) != 2 || recent[a.ID][1].State != model.StatusDown || recent[a.ID][0].State != model.StatusPending {
		t.Fatalf("últimos de A = %+v", recent[a.ID])
	}
	if len(recent[b.ID]) != 1 || recent[b.ID][0].State != model.StatusDegraded || recent[b.ID][0].Latency != time.Second {
		t.Fatalf("últimos de B = %+v", recent[b.ID])
	}
	if _, ok := recent[12345]; ok {
		t.Error("los resultados de checks inexistentes deben descartarse")
	}

	rs, _ := s.ResultsRange(ctx, a.ID, t0, t0.Add(2*time.Minute))
	if len(rs) != 2 || !rs[0].OK {
		t.Fatalf("ResultsRange = %+v", rs)
	}
	buckets, err := s.ResultBuckets(ctx, a.ID, hour, hour.Add(time.Hour), 30*time.Minute)
	if err != nil || len(buckets) != 1 || buckets[0].Total != 3 || buckets[0].Down != 1 || buckets[0].AvgLat != 1500*time.Microsecond || !buckets[0].Start.Equal(hour) {
		t.Fatalf("ResultBuckets = %+v, %v", buckets, err)
	}

	hs, _ := s.HourlyRange(ctx, a.ID, hour, hour.Add(time.Hour))
	if len(hs) != 1 {
		t.Fatalf("HourlyRange = %+v", hs)
	}
	h := hs[0]
	if h.Total != 5 || h.Down != 1 || h.Degraded != 1 || h.LatCount != 3 || h.LatMin != time.Millisecond || h.LatMax != 2*time.Millisecond ||
		h.AvgLatency() != 1500*time.Microsecond {
		t.Fatalf("agregado sumado = %+v", h)
	}
	tot, _ := s.HourlyTotals(ctx, hour)
	if tot[a.ID].Total != 5 || tot[a.ID].Down != 1 {
		t.Fatalf("HourlyTotals = %+v", tot)
	}

	// Retención.
	if n, _ := s.DeleteResultsBefore(ctx, t0.Add(time.Minute)); n != 2 {
		t.Errorf("resultados borrados = %d, se esperaban 2 (A y B en t0)", n)
	}
	if n, _ := s.DeleteHourlyBefore(ctx, hour.Add(time.Hour)); n != 1 {
		t.Errorf("agregados borrados = %d", n)
	}

	// Al borrar un check se borra todo lo suyo.
	s.DeleteCheck(ctx, a.ID)
	if recent, _ := s.RecentResults(ctx, 10); len(recent[a.ID]) != 0 {
		t.Error("quedan resultados de un check borrado")
	}
}

func TestIncidencias(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	a := model.Check{Name: "A", Type: "tcp", Interval: time.Minute}
	b := model.Check{Name: "B", Type: "tcp", Interval: time.Minute}
	s.CreateCheck(ctx, &a)
	s.CreateCheck(ctx, &b)
	t0 := time.UnixMilli(1_800_000_000_000)

	i1 := model.Incident{CheckID: a.ID, Start: t0, LastError: "e1"}
	if err := s.OpenIncident(ctx, &i1); err != nil || i1.ID == 0 {
		t.Fatalf("OpenIncident: %v %+v", err, i1)
	}
	if err := s.CloseIncident(ctx, i1.ID, t0.Add(5*time.Minute), "e2"); err != nil {
		t.Fatal(err)
	}
	i2 := model.Incident{CheckID: a.ID, Start: t0.Add(time.Hour)}
	s.OpenIncident(ctx, &i2)
	i3 := model.Incident{CheckID: b.ID, Start: t0.Add(2 * time.Hour)}
	s.OpenIncident(ctx, &i3)

	open, err := s.OpenIncidents(ctx)
	if err != nil || len(open) != 2 || open[a.ID].ID != i2.ID || !open[a.ID].Open() {
		t.Fatalf("OpenIncidents = %+v, %v", open, err)
	}
	all, _ := s.ListIncidents(ctx, 0, 10)
	if len(all) != 3 || all[0].ID != i3.ID {
		t.Fatalf("ListIncidents(todas) = %+v", all)
	}
	onlyA, _ := s.ListIncidents(ctx, a.ID, 10)
	if len(onlyA) != 2 || onlyA[1].LastError != "e2" || onlyA[1].Duration(time.Now()) != 5*time.Minute {
		t.Fatalf("ListIncidents(A) = %+v", onlyA)
	}
	// La retención solo borra incidencias cerradas.
	if n, _ := s.DeleteIncidentsBefore(ctx, t0.Add(24*time.Hour)); n != 1 {
		t.Errorf("incidencias borradas = %d, se esperaba 1", n)
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
