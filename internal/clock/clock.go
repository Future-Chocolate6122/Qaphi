// Package clock comprueba que la hora del sistema es plausible antes de
// registrar resultados. Placas como la NanoPi no tienen reloj de hardware: al
// arrancar, la hora puede ser la de 1970 o la del último apagado hasta que NTP
// sincroniza, y registrar con esa hora falsearía incidencias y uptime.
package clock

import (
	"log/slog"
	"sync"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/version"
)

// floor es la fecha mínima absoluta que se considera válida si no se conoce
// la fecha de compilación.
var floor = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// buildMargin tolera un reloj ligeramente atrasado respecto a la fecha del
// commit con el que se compiló.
const buildMargin = 24 * time.Hour

// MinValid devuelve la hora mínima que se considera válida: la fecha de
// compilación del binario (menos un margen) o, si no se conoce, una fecha
// fija.
func MinValid() time.Time {
	if bt := version.BuildTime(); !bt.IsZero() {
		if m := bt.Add(-buildMargin); m.After(floor) {
			return m
		}
	}
	return floor
}

// Guard decide si la hora actual es válida y registra en el log los cambios
// (de válida a no válida y viceversa) una sola vez cada uno.
type Guard struct {
	log *slog.Logger
	min time.Time
	now func() time.Time

	mu      sync.Mutex
	invalid bool
}

// NewGuard crea un Guard. now permite inyectar el reloj en los tests (nil =
// time.Now).
func NewGuard(log *slog.Logger, min time.Time, now func() time.Time) *Guard {
	if now == nil {
		now = time.Now
	}
	return &Guard{log: log, min: min, now: now}
}

// Valid indica si la hora actual es posterior a la mínima válida.
func (g *Guard) Valid() bool {
	now := g.now()
	ok := !now.Before(g.min)
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case !ok && !g.invalid:
		g.invalid = true
		g.log.Warn("la hora del sistema no es válida (¿NTP sin sincronizar?): no se registran resultados hasta que se corrija",
			"hora", now.UTC().Format(time.RFC3339), "mínima", g.min.UTC().Format(time.RFC3339))
	case ok && g.invalid:
		g.invalid = false
		g.log.Info("la hora del sistema ya es válida: se reanudan los checks", "hora", now.UTC().Format(time.RFC3339))
	}
	return ok
}
