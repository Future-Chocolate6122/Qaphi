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
	"time"

	"github.com/future-chocolate6122/qaphi/internal/auth"
	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/state"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// StateTracker es lo que la web necesita del estado en memoria.
type StateTracker interface {
	Snapshot() map[int64]state.CheckState
	SetPaused(id int64, paused bool)
	Remove(id int64)
}

// CheckScheduler es lo que la web necesita del planificador.
type CheckScheduler interface {
	Set(c model.Check)
	Remove(id int64)
}

// Deps son las dependencias que el servidor web recibe de la aplicación.
type Deps struct {
	Log        *slog.Logger
	Store      store.Store
	Admin      *auth.Admin
	Sessions   *auth.Sessions
	SetupToken *auth.SetupToken // nil si ya hay contraseña
	Limiter    *auth.Limiter
	Tracker    StateTracker
	Scheduler  CheckScheduler
	Now        func() time.Time // nil = time.Now
}

// Server es el manejador HTTP de Qaphi.
type Server struct {
	Deps
	pages   map[string]*template.Template
	static  *staticFiles
	handler http.Handler
}

// New construye el servidor con sus rutas y middleware.
func New(deps Deps) (*Server, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Limiter == nil {
		deps.Limiter = auth.DefaultLimiter()
	}
	static, err := loadStatic(staticFS, "static")
	if err != nil {
		return nil, err
	}
	s := &Server{Deps: deps, static: static}

	s.pages, err = loadPages(templateFS, template.FuncMap{
		"t":     msg.T,
		"tf":    msg.F,
		"asset": static.url,
	})
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", static))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/home", http.StatusSeeOther)
	})

	// Autenticación.
	mux.HandleFunc("GET /setup", s.handleSetupForm)
	mux.HandleFunc("POST /setup", s.handleSetup)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.Handle("POST /logout", s.requireAuth(http.HandlerFunc(s.handleLogout)))

	// Página de estado (en la fase 2 podrá ser pública).
	mux.Handle("GET /home", s.requireAuth(http.HandlerFunc(s.handleHome)))

	// Administración.
	mux.Handle("GET /admin", s.requireAuth(http.HandlerFunc(s.handleAdmin)))
	mux.Handle("GET /admin/checks/new", s.requireAuth(http.HandlerFunc(s.handleCheckNew)))
	mux.Handle("POST /admin/checks", s.requireAuth(http.HandlerFunc(s.handleCheckCreate)))
	mux.Handle("GET /admin/checks/{id}", s.requireAuth(http.HandlerFunc(s.handleCheckEdit)))
	mux.Handle("POST /admin/checks/{id}", s.requireAuth(http.HandlerFunc(s.handleCheckUpdate)))
	mux.Handle("GET /admin/checks/{id}/delete", s.requireAuth(http.HandlerFunc(s.handleCheckDeleteForm)))
	mux.Handle("POST /admin/checks/{id}/delete", s.requireAuth(http.HandlerFunc(s.handleCheckDelete)))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.renderError(w, r, http.StatusNotFound, "http.notfound")
	})

	s.handler = chain(mux,
		recoverPanics(s.Log),
		securityHeaders,
		logRequests(s.Log),
		limitBody,
		s.setupGate,
	)
	return s, nil
}

// ServeHTTP implementa http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// pageData es lo que reciben todas las páginas.
type pageData struct {
	Title    string
	LoggedIn bool
	CSRF     string // token para los formularios de la página
	Flash    string // mensaje de confirmación
	Active   string // sección activa del menú
	Refresh  int    // segundos para la recarga automática (0 = sin recarga)
	Body     any    // datos propios de la página
}

// render ejecuta la plantilla en un buffer antes de escribir nada, para no
// enviar una página a medias si la plantilla falla.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, data pageData) {
	t, ok := s.pages[name]
	if !ok {
		s.Log.Error("plantilla inexistente", "plantilla", name)
		http.Error(w, msg.T("http.internal"), http.StatusInternalServerError)
		return
	}
	if sess, ok := sessionFrom(r); ok {
		data.LoggedIn = true
		if data.CSRF == "" {
			data.CSRF = sess.CSRF
		}
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.Log.Error("error al generar la página", "plantilla", name, "ruta", r.URL.Path, "error", err)
		http.Error(w, msg.T("http.internal"), http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store") // las páginas tienen datos de sesión o de estado
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, key string) {
	s.render(w, r, status, "error", pageData{Title: msg.T("http.error"), Body: msg.T(key)})
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("error al atender una petición", "ruta", r.URL.Path, "error", err)
	s.renderError(w, r, http.StatusInternalServerError, "http.internal")
}

// redirect hace una redirección 303 (tras un POST, el navegador pide la
// nueva página con GET).
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
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
