package settings

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/store/sqlite"
)

func TestZonasValidas(t *testing.T) {
	if len(Timezones()) < 300 {
		t.Fatalf("solo %d zonas", len(Timezones()))
	}
	for _, z := range Timezones() {
		if _, err := time.LoadLocation(z); err != nil {
			t.Errorf("la zona %q no se puede cargar: %v", z, err)
		}
	}
}

func TestAjustes(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	s, err := Load(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	ui := s.UI()
	if ui.LangSet || ui.Lang != msg.EN || ui.Theme != "auto" || ui.Timezone != "" || s.Location() != time.Local {
		t.Fatalf("valores por defecto = %+v", ui)
	}

	if err := s.SetLanguage(ctx, msg.FR); err != nil {
		t.Fatal(err)
	}
	if ui := s.UI(); !ui.LangSet || ui.Lang != msg.FR {
		t.Fatalf("tras SetLanguage: %+v", ui)
	}

	for _, bad := range []struct {
		l      msg.Lang
		th, tz string
	}{
		{"it", "auto", ""}, {msg.ES, "rosa", ""}, {msg.ES, "dark", "Marte/Olympus"},
	} {
		if err := s.SetUI(ctx, bad.l, bad.th, bad.tz); err == nil {
			t.Errorf("SetUI(%v) debería fallar", bad)
		}
	}
	if err := s.SetUI(ctx, msg.DE, "dark", "Europe/Madrid"); err != nil {
		t.Fatal(err)
	}
	if s.Location().String() != "Europe/Madrid" {
		t.Errorf("zona = %v", s.Location())
	}

	// Persisten tras recargar.
	s2, err := Load(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if ui := s2.UI(); ui.Lang != msg.DE || ui.Theme != "dark" || ui.Timezone != "Europe/Madrid" || !ui.LangSet {
		t.Fatalf("tras recargar: %+v", ui)
	}
	// Volver a la zona del sistema.
	if err := s2.SetUI(ctx, msg.DE, "light", ""); err != nil {
		t.Fatal(err)
	}
	if s2.Location() != time.Local {
		t.Error("zona vacía = la del sistema")
	}
	if SystemTimezone() == "" {
		t.Error("SystemTimezone vacío")
	}
}

func TestAjustesDeDatos(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, _ := Load(ctx, st)
	if s.Data() != DefaultData() {
		t.Fatalf("por defecto = %+v", s.Data())
	}
	bad := DefaultData()
	bad.RawHours = 0
	if err := s.SetData(ctx, bad); err == nil {
		t.Error("retención de 0 h aceptada")
	}
	want := Data{Public: true, RawHours: 24, AggregateDays: 30, AggregatesOnly: true, SessionTTL: 12 * time.Hour}
	if err := s.SetData(ctx, want); err != nil {
		t.Fatal(err)
	}
	s2, _ := Load(ctx, st)
	if s2.Data() != want {
		t.Fatalf("tras recargar = %+v", s2.Data())
	}
	// auth lee la caducidad de sesión de la misma clave.
	if v, _, _ := st.GetSetting(ctx, "session.ttl"); v != "12h0m0s" {
		t.Errorf("session.ttl = %q", v)
	}
}
