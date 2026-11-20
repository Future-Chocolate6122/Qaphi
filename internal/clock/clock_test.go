package clock

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestGuard(t *testing.T) {
	min := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(1970, 1, 1, 0, 0, 10, 0, time.UTC)
	var logs strings.Builder
	g := NewGuard(slog.New(slog.NewTextHandler(&logs, nil)), min, func() time.Time { return now })

	steps := []struct {
		at        time.Time
		want      bool
		wantWarns int
		wantInfos int
	}{
		{time.Date(1970, 1, 1, 0, 0, 10, 0, time.UTC), false, 1, 0},
		{time.Date(1970, 1, 1, 0, 1, 10, 0, time.UTC), false, 1, 0}, // no se repite el aviso
		{min, true, 1, 1},
		{min.Add(time.Hour), true, 1, 1},
		{min.Add(-time.Second), false, 2, 1}, // salto atrás: nuevo aviso
	}
	for i, s := range steps {
		now = s.at
		if got := g.Valid(); got != s.want {
			t.Fatalf("paso %d: Valid = %v, se esperaba %v", i, got, s.want)
		}
		if w := strings.Count(logs.String(), "level=WARN"); w != s.wantWarns {
			t.Fatalf("paso %d: avisos = %d, se esperaban %d", i, w, s.wantWarns)
		}
		if n := strings.Count(logs.String(), "level=INFO"); n != s.wantInfos {
			t.Fatalf("paso %d: infos = %d, se esperaban %d", i, n, s.wantInfos)
		}
	}
}

func TestMinValid(t *testing.T) {
	if MinValid().Before(floor) {
		t.Fatalf("MinValid = %v, anterior al mínimo absoluto %v", MinValid(), floor)
	}
}
