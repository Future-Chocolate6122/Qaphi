package state

import (
	"strings"

	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

// Summary es el resumen global del estado.
type Summary struct {
	Total   int // checks activos (no pausados)
	Up      int
	Down    int
	Unknown int
	Paused  int
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
		case model.StatusDown:
			s.Down++
		default:
			s.Unknown++
		}
		s.Total++
	}
	return s
}

// In devuelve el resumen de una línea en el idioma l: «4/4 (todos up)»,
// «3/4 (1 caído)», «1/3 (1 caído, 1 sin datos)».
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

// Level es el nivel global: "up", "down" o "unknown".
func (s Summary) Level() model.Status {
	switch {
	case s.Down > 0:
		return model.StatusDown
	case s.Total == 0 || s.Unknown == s.Total:
		return model.StatusUnknown
	default:
		return model.StatusUp
	}
}
