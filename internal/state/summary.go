package state

import (
	"fmt"
	"strings"

	"github.com/future-chocolate6122/qaphi/internal/model"
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

// String devuelve el resumen de una línea: «4/4 (todos up)», «3/4 (1 caído)».
func (s Summary) String() string {
	if s.Total == 0 {
		return "0/0 (sin checks activos)"
	}
	var parts []string
	if s.Down > 0 {
		parts = append(parts, plural(s.Down, "caído", "caídos"))
	}
	if s.Unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d sin datos", s.Unknown))
	}
	detail := "todos up"
	if len(parts) > 0 {
		detail = strings.Join(parts, ", ")
	}
	return fmt.Sprintf("%d/%d (%s)", s.Up, s.Total, detail)
}

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

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
