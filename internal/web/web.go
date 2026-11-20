// Package web contiene el servidor HTTP: rutas, middleware, plantillas y
// ficheros estáticos embebidos en el binario.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"qaphi/internal/msg"
	"qaphi/internal/version"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Deps son las dependencias que el servidor web recibe de la aplicación.
type Deps struct {
	Log *slog.Logger
}

// Server es el manejador HTTP de Qaphi.
type Server struct {
	log     *slog.Logger
	pages   map[string]*template.Template
	static  *staticFiles
	handler http.Handler
}

// New construye el servidor con sus rutas y middleware.
func New(deps Deps) (*Server, error) {
	static, err := loadStatic(staticFS, "static")
	if err != nil {
		return nil, err
	}
	s := &Server{log: deps.Log, static: static}

	s.pages, err = loadPages(templateFS, template.FuncMap{
		"t":     msg.T,
		"asset": static.url,
	})
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.Handle("GET /static/", http.StripPrefix("/static/", static))

	s.handler = chain(mux,
		recoverPanics(s.log),
		securityHeaders,
		logRequests(s.log),
	)
	return s, nil
}

// ServeHTTP implementa http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// handleIndex muestra la página provisional de la fase 0. En la fase 1, «/»
// pasará a redirigir a /home.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "index", map[string]any{
		"Version": version.Get().Version,
	})
}

// render ejecuta la plantilla en un buffer antes de escribir nada, para no
// enviar una página a medias si la plantilla falla.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	var buf bytes.Buffer
	t, ok := s.pages[name]
	if !ok {
		s.log.Error("plantilla inexistente", "plantilla", name)
		http.Error(w, msg.T("http.internal"), http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.log.Error("error al generar la página", "plantilla", name, "ruta", r.URL.Path, "error", err)
		http.Error(w, msg.T("http.internal"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// loadPages carga las plantillas. Las de templates/*.html (diseño común y
// fragmentos) se comparten; cada fichero de templates/pages/ es una página
// independiente que define el bloque "content" y se indexa por su nombre sin
// extensión. Así cada página puede definir sus bloques sin chocar con otras.
func loadPages(fsys fs.FS, funcs template.FuncMap) (map[string]*template.Template, error) {
	base, err := template.New("").Funcs(funcs).ParseFS(fsys, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("cargar plantillas comunes: %w", err)
	}
	files, err := fs.Glob(fsys, "templates/pages/*.html")
	if err != nil {
		return nil, err
	}
	pages := make(map[string]*template.Template, len(files))
	for _, f := range files {
		t, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := t.ParseFS(fsys, f); err != nil {
			return nil, fmt.Errorf("cargar plantilla %s: %w", f, err)
		}
		pages[strings.TrimSuffix(path.Base(f), ".html")] = t
	}
	return pages, nil
}
