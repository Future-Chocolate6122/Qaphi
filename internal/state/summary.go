package state

import (
	"strings"

	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

// Summary es el resumen global del estado.
type Summary struct {
	Total    int // checks activos (no pausados)
	Up       int // operativos: incluye los lentos y los pendientes
	Down     int
	Degraded int // lentos (cuentan también en Up)
	Pending  int // con fallos sin confirmar (cuentan también en Up)
	NoPerm   int
	Unknown  int
	Paused   int
}

// Summarize resume el estado de los checks indicados.
func Summarize(checks []model.Check, snap map[int64]CheckState) Summary {
	var s Summary
	for _, c := range checks {
		st, ok := snap[c.ID]
		status := model.StatusUnknown
		if ok {
			status = st.Status
		}
		if c.Paused {
			status = model.StatusPaused
		}
		switch status {
		case model.StatusPaused:
			s.Paused++
			continue
		case model.StatusUp:
			s.Up++
		case model.StatusDegraded:
			s.Up++
			s.Degraded++
		case model.StatusPending:
			s.Up++
			s.Pending++
		case model.StatusDown:
			s.Down++
		case model.StatusNoPerm:
			s.NoPerm++
		default:
			s.Unknown++
		}
		s.Total++
	}
	return s
}

// In devuelve el resumen de una línea en el idioma l: «4/4 (todos up)»,
// «3/4 (1 caído)», «4/4 (1 lento)».
func (s Summary) In(l msg.Lang) string {
	if s.Total == 0 {
		return msg.T(l, "summary.none")
	}
	var parts []string
	switch {
	case s.Down == 1:
		parts = append(parts, msg.F(l, "summary.down.one", s.Down))
	case s.Down > 1:
		parts = append(parts, msg.F(l, "summary.down.many", s.Down))
	}
	if s.Pending > 0 {
		parts = append(parts, msg.F(l, "summary.pending", s.Pending))
	}
	if s.Degraded > 0 {
		parts = append(parts, msg.F(l, "summary.degraded", s.Degraded))
	}
	if s.NoPerm > 0 {
		parts = append(parts, msg.F(l, "summary.noperm", s.NoPerm))
	}
	if s.Unknown > 0 {
		parts = append(parts, msg.F(l, "summary.unknown", s.Unknown))
	}
	detail := msg.T(l, "summary.all_up")
	if len(parts) > 0 {
		detail = strings.Join(parts, ", ")
	}
	return msg.F(l, "summary.format", s.Up, s.Total, detail)
}

// String devuelve el resumen en inglés (consola, logs).
func (s Summary) String() string { return s.In(msg.EN) }

// Level es el nivel global: down, degraded, up o unknown.
func (s Summary) Level() model.Status {
	switch {
	case s.Down > 0:
		return model.StatusDown
	case s.Degraded > 0 || s.Pending > 0:
		return model.StatusDegraded
	case s.Total == 0 || s.Up == 0:
		return model.StatusUnknown
	default:
		return model.StatusUp
	}
}
