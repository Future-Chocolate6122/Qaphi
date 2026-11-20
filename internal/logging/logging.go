// Package logging construye el logger de la aplicación (log/slog a stdout).
package logging

import (
	"context"
	"io"
	"log/slog"
	"time"
)

// Options configura el logger.
type Options struct {
	Level  slog.Level
	Format string // "text" (por defecto) o "json"
	// OmitTime suprime la marca de tiempo propia. Se activa cuando la salida
	// va a journald, que ya añade la suya: evita duplicarla y reduce el
	// volumen escrito en disco.
	OmitTime bool
}

// UnderJournald indica si la salida estándar está conectada a journald.
// systemd define JOURNAL_STREAM en ese caso. getenv permite inyectar el
// entorno en los tests (en producción, os.Getenv).
func UnderJournald(getenv func(string) string) bool {
	return getenv("JOURNAL_STREAM") != ""
}

// New devuelve un logger en formato texto o JSON.
func New(w io.Writer, o Options) *slog.Logger {
	opts := &slog.HandlerOptions{Level: o.Level}
	var h slog.Handler
	if o.Format == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	if o.OmitTime {
		h = noTimeHandler{h}
	}
	return slog.New(h)
}

// noTimeHandler pone a cero la hora del registro; los handlers de slog omiten
// la marca de tiempo cuando es cero. A diferencia de ReplaceAttr, no afecta a
// atributos propios que se llamen «time».
type noTimeHandler struct{ slog.Handler }

func (h noTimeHandler) Handle(ctx context.Context, r slog.Record) error {
	r.Time = time.Time{}
	return h.Handler.Handle(ctx, r)
}

func (h noTimeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return noTimeHandler{h.Handler.WithAttrs(attrs)}
}

func (h noTimeHandler) WithGroup(name string) slog.Handler {
	return noTimeHandler{h.Handler.WithGroup(name)}
}
