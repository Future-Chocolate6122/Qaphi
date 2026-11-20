package logging

import (
	"log/slog"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		opts     Options
		wantTime bool
		wantJSON bool
	}{
		{"texto con hora", Options{Level: slog.LevelInfo}, true, false},
		{"texto bajo journald", Options{Level: slog.LevelInfo, OmitTime: true}, false, false},
		{"json con hora", Options{Level: slog.LevelInfo, Format: "json"}, true, true},
		{"json bajo journald", Options{Level: slog.LevelInfo, Format: "json", OmitTime: true}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sb strings.Builder
			New(&sb, tt.opts).Info("hola", "time", "atributo propio")
			out := sb.String()
			hasTime := strings.HasPrefix(out, "time=") || strings.HasPrefix(out, `{"time":`)
			if hasTime != tt.wantTime {
				t.Errorf("marca de tiempo = %v, se esperaba %v: %s", hasTime, tt.wantTime, out)
			}
			if strings.HasPrefix(out, "{") != tt.wantJSON {
				t.Errorf("formato incorrecto: %s", out)
			}
			// Un atributo del usuario llamado "time" no debe suprimirse.
			if !strings.Contains(out, "atributo propio") {
				t.Errorf("se suprimió un atributo propio: %s", out)
			}
		})
	}
}

func TestNivel(t *testing.T) {
	var sb strings.Builder
	l := New(&sb, Options{Level: slog.LevelWarn})
	l.Info("no")
	l.Warn("sí")
	if strings.Contains(sb.String(), "msg=no") || !strings.Contains(sb.String(), "msg=sí") {
		t.Fatalf("filtrado por nivel incorrecto: %s", sb.String())
	}
}

func TestUnderJournald(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if UnderJournald(getenv) {
		t.Fatal("sin JOURNAL_STREAM no debe detectarse journald")
	}
	env["JOURNAL_STREAM"] = "8:12345"
	if !UnderJournald(getenv) {
		t.Fatal("con JOURNAL_STREAM debe detectarse journald")
	}
}

func TestOmitTimeConWith(t *testing.T) {
	var sb strings.Builder
	New(&sb, Options{OmitTime: true}).With("módulo", "web").WithGroup("g").Info("hola", "k", 1)
	if out := sb.String(); strings.HasPrefix(out, "time=") || !strings.Contains(out, "módulo=web") || !strings.Contains(out, "g.k=1") {
		t.Fatalf("salida inesperada: %s", out)
	}
}
