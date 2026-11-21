package web

import (
	"fmt"
	"strings"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/msg"
)

// decimal adapta el separador decimal al idioma (coma salvo en inglés).
func decimal(l msg.Lang, s string) string {
	if l == msg.EN {
		return s
	}
	return strings.Replace(s, ".", ",", 1)
}

// formatLatency: «0,4 ms», «12 ms», «1,2 s».
func formatLatency(l msg.Lang, d time.Duration) string {
	switch {
	case d <= 0:
		return "—"
	case d < 10*time.Millisecond:
		return decimal(l, fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000))
	case d < 10*time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	default:
		return decimal(l, fmt.Sprintf("%.1f s", d.Seconds()))
	}
}

// ago: «hace 5 s», «5 s ago»…
func ago(l msg.Lang, now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case t.IsZero():
		return msg.T(l, "time.never")
	case d < time.Second:
		return msg.T(l, "time.now")
	case d < time.Minute:
		return msg.F(l, "time.ago.s", int64(d/time.Second))
	case d < time.Hour:
		return msg.F(l, "time.ago.m", int64(d/time.Minute))
	case d < 48*time.Hour:
		return msg.F(l, "time.ago.h", int64(d/time.Hour))
	default:
		return msg.F(l, "time.ago.d", int64(d/(24*time.Hour)))
	}
}

// Formatos de fecha por idioma.
var (
	dateTimeLayout = map[msg.Lang]string{
		msg.ES: "02/01/2006 15:04:05 MST", msg.FR: "02/01/2006 15:04:05 MST",
		msg.DE: "02.01.2006 15:04:05 MST", msg.EN: "2006-01-02 15:04:05 MST",
	}
	dateLayout = map[msg.Lang]string{
		msg.ES: "02/01/2006", msg.FR: "02/01/2006", msg.DE: "02.01.2006", msg.EN: "2006-01-02",
	}
)

// formatDateTime muestra una fecha y hora en la zona y el formato del idioma.
func formatDateTime(l msg.Lang, loc *time.Location, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(loc).Format(dateTimeLayout[l])
}

// formatDate muestra una fecha.
func formatDate(l msg.Lang, loc *time.Location, t time.Time) string {
	return t.In(loc).Format(dateLayout[l])
}

// shortDuration: «1m0s» → «1m», «1h30m0s» → «1h30m».
func shortDuration(d time.Duration) string {
	s := d.String()
	for _, suf := range []string{"m0s", "h0m"} {
		if len(s) > len(suf) && s[len(s)-len(suf):] == suf {
			s = s[:len(s)-2]
		}
	}
	return s
}
