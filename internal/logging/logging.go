// Package logging construye el logger de la aplicación (log/slog a stdout).
package logging

import (
	"io"
	"log/slog"
)

// New devuelve un logger en formato texto (por defecto) o JSON. El formato de
// texto es legible directamente en journald.
func New(w io.Writer, level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h)
}
