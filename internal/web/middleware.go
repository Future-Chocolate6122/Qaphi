package web

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"qaphi/internal/msg"
)

type middleware func(http.Handler) http.Handler

// chain aplica los middleware en orden: el primero es el más externo.
func chain(h http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// contentSecurityPolicy permite solo recursos propios: sin scripts ni
// estilos en línea, sin marcos y con formularios restringidos al mismo origen.
// Las imágenes admiten data: para el favicon SVG generado.
const contentSecurityPolicy = "default-src 'none'; " +
	"script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; form-action 'self'; " +
	"frame-ancestors 'none'; base-uri 'none'"

// securityHeaders añade las cabeceras de seguridad a todas las respuestas.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// recoverPanics convierte un pánico en un handler en un error 500 registrado
// en el log, sin tumbar el proceso.
func recoverPanics(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler { // aborto deliberado: lo gestiona net/http
						panic(v)
					}
					log.Error("pánico en un handler HTTP", "ruta", r.URL.Path, "pánico", v, "pila", string(debug.Stack()))
					http.Error(w, msg.T("http.internal"), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder captura el código de estado para el log de peticiones.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// logRequests registra cada petición en nivel debug. Solo se registra la ruta,
// nunca la query string, que podría contener secretos.
func logRequests(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !log.Enabled(r.Context(), slog.LevelDebug) {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			log.Debug("petición HTTP",
				"método", r.Method, "ruta", r.URL.Path, "estado", rec.status,
				"duración", time.Since(start).Round(time.Microsecond), "remoto", r.RemoteAddr)
		})
	}
}
