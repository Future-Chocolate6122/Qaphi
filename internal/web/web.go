// Package web contiene el servidor HTTP: rutas, middleware, plantillas, CSS
// y JavaScript embebidos en el binario.
//
// Las páginas se generan en el idioma configurado (las plantillas se compilan
// una vez por idioma al arrancar). Login, setup y formularios funcionan sin
// JavaScript; las páginas de estado usan JavaScript para actualizarse en vivo.
package web

import (
	"bytes"
	"context"
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
	"github.com/future-chocolate6122/qaphi/internal/netcfg"
	"github.com/future-chocolate6122/qaphi/internal/settings"
	"github.com/future-chocolate6122/qaphi/internal/state"
	"github.com/future-chocolate6122/qaphi/internal/store"
	"github.com/future-chocolate6122/qaphi/internal/theme"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// StateTracker es lo que la web necesita del estado en memoria.
type StateTracker interface {
	Snapshot() map[int64]state.CheckState
	Get(id int64) (state.CheckState, bool)
	SetCheck(c model.Check)
	Remove(id int64)
	Uptime(id int64) []float64
	PendingResults(id int64) []model.Result
	PendingHourly(id int64) []model.Hourly
	SetAggregatesOnly(on bool)
}

// CheckScheduler es lo que la web necesita del planificador.
type CheckScheduler interface {
	Set(c model.Check)
	Remove(id int64)
	Trigger(id int64)
}

// Network es lo que la web necesita del gestor de red.
type Network interface {
	Current() netcfg.Config
	Locked() bool
	Pending() (cfg netcfg.Config, deadline time.Time, inPlace, ok bool)
	Apply(ctx context.Context, cfg netcfg.Config) (inPlace bool, err error)
	Confirm(r *http.Request) error
	Cancel() error
	TakeReverted() bool
}

// Certs es lo que la web necesita del certificado HTTPS.
type Certs interface {
	Info() (netcfg.CertInfo, bool)
	Regenerate(ctx context.Context) error
}

// Deps son las dependencias que el servidor web recibe de la aplicación.
type Deps struct {
	Log        *slog.Logger
	Store      store.Store
	Settings   *settings.Service
	Admin      *auth.Admin
	Sessions   *auth.Sessions
	SetupToken *auth.SetupToken // nil si ya hay contraseña
	Limiter    *auth.Limiter
	Tracker    StateTracker
	Scheduler  CheckScheduler
	Network    Network // nil: sin ajustes de red (tests)
	Certs      Certs
	Now        func() time.Time // nil = time.Now
}

// Server es el manejador HTTP de Qaphi.
type Server struct {
	Deps
	pages   map[msg.Lang]map[string]*template.Template
	static  *staticFiles
	themes  map[string]theme.Compiled
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
	s := &Server{Deps: deps, static: static, themes: theme.Compile()}

	s.pages = map[msg.Lang]map[string]*template.Template{}
	for _, l := range msg.Languages() {
		lang := l.Code
		pages, err := loadPages(templateFS, template.FuncMap{
			"t":         func(key string) string { return msg.T(lang, key) },
			"tf":        func(key string, args ...any) string { return msg.F(lang, key, args...) },
			"asset":     static.url,
			"component": func(string, any) (template.HTML, error) { return "", nil }, // se redefine por página
		})
		if err != nil {
			return nil, err
		}
		s.pages[lang] = pages
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", static))
	mux.HandleFunc("GET /theme/{file}", s.handleTheme)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/home", http.StatusSeeOther)
	})

	// Autenticación.
	mux.HandleFunc("GET /setup", s.handleSetupForm)
	mux.HandleFunc("POST /setup", s.handleSetup)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.Handle("POST /logout", s.requireAuth(http.HandlerFunc(s.handleLogout)))

	// Páginas de estado (públicas o con login, según el ajuste) y sus datos.
	mux.Handle("GET /home", s.statusAuth(s.handleHome))
	mux.Handle("GET /home/state.json", s.statusAuth(s.handleHomeState))
	mux.Handle("GET /check/{id}", s.statusAuth(s.handleCheckPage))
	mux.Handle("GET /check/{id}/data.json", s.statusAuth(s.handleCheckData))

	// Pulsos de los heartbeats: siempre abiertos, autenticados por su token.
	mux.HandleFunc("GET /hb/{token}", s.handleHeartbeat)
	mux.HandleFunc("POST /hb/{token}", s.handleHeartbeat)
	mux.HandleFunc("HEAD /hb/{token}", s.handleHeartbeat)

	// Checks.
	mux.Handle("GET /admin", s.requireAuth(http.HandlerFunc(s.handleAdmin)))
	mux.Handle("GET /admin/checks/new", s.requireAuth(http.HandlerFunc(s.handleCheckNew)))
	mux.Handle("POST /admin/checks", s.requireAuth(http.HandlerFunc(s.handleCheckCreate)))
	mux.Handle("GET /admin/checks/{id}", s.requireAuth(http.HandlerFunc(s.handleCheckEdit)))
	mux.Handle("POST /admin/checks/{id}", s.requireAuth(http.HandlerFunc(s.handleCheckUpdate)))
	mux.Handle("GET /admin/checks/{id}/delete", s.requireAuth(http.HandlerFunc(s.handleCheckDeleteForm)))
	mux.Handle("POST /admin/checks/{id}/delete", s.requireAuth(http.HandlerFunc(s.handleCheckDelete)))

	// Ajustes.
	mux.Handle("GET /admin/settings", s.requireAuth(http.HandlerFunc(s.handleSettings)))
	mux.Handle("POST /admin/settings", s.requireAuth(http.HandlerFunc(s.handleSettingsSave)))
	mux.Handle("POST /admin/settings/data", s.requireAuth(http.HandlerFunc(s.handleDataSave)))
	mux.Handle("POST /admin/settings/network", s.requireAuth(http.HandlerFunc(s.handleNetworkApply)))
	mux.Handle("GET /admin/settings/network/confirm", s.requireAuth(http.HandlerFunc(s.handleNetworkConfirmForm)))
	mux.Handle("POST /admin/settings/network/confirm", s.requireAuth(http.HandlerFunc(s.handleNetworkConfirm)))
	mux.Handle("POST /admin/settings/network/cancel", s.requireAuth(http.HandlerFunc(s.handleNetworkCancel)))
	mux.Handle("POST /admin/settings/cert", s.requireAuth(http.HandlerFunc(s.handleCertRegenerate)))

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

// langCookie guarda el idioma elegido en /setup antes de que exista el ajuste.
const langCookie = "qaphi_lang"

// lang devuelve el idioma de la petición: el ajuste global si ya existe; si
// no (antes del setup), el elegido en /setup o el del navegador.
func (s *Server) lang(r *http.Request) msg.Lang {
	if ui := s.Settings.UI(); ui.LangSet {
		return ui.Lang
	}
	if c, err := r.Cookie(langCookie); err == nil {
		if l, ok := msg.Parse(c.Value); ok {
			return l
		}
	}
	return msg.Negotiate(r.Header.Get("Accept-Language"))
}

// pageData es lo que reciben todas las páginas.
type pageData struct {
	Lang     msg.Lang
	Title    string
	LoggedIn bool
	CSRF     string // token para los formularios de la página
	Flash    string // mensaje de confirmación
	Alert    string // aviso destacado (p. ej. cambio de red deshecho)
	Active   string // sección activa del menú
	ThemeCSS string // URL de la hoja de estilos del tema
	Live     string // URL de datos en vivo (activa live.js)
	Body     any    // datos propios de la página
}

// render ejecuta la plantilla en un buffer antes de escribir nada, para no
// enviar una página a medias si la plantilla falla.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, data pageData) {
	data.Lang = s.lang(r)
	t, ok := s.pages[data.Lang][name]
	if !ok {
		s.Log.Error("unknown template", "template", name)
		http.Error(w, msg.T(data.Lang, "http.internal"), http.StatusInternalServerError)
		return
	}
	if sess, ok := sessionFrom(r); ok {
		data.LoggedIn = true
		if data.CSRF == "" {
			data.CSRF = sess.CSRF
		}
	}
	data.ThemeCSS = s.themeURL(s.Settings.UI().Theme)
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.Log.Error("template execution failed", "template", name, "path", r.URL.Path, "error", err)
		http.Error(w, msg.T(data.Lang, "http.internal"), http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store") // las páginas tienen datos de sesión o de estado
	h.Set("Content-Language", string(data.Lang))
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// T traduce una clave al idioma de la petición.
func (s *Server) T(r *http.Request, key string) string { return msg.T(s.lang(r), key) }

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, key string) {
	s.render(w, r, status, "error", pageData{Title: s.T(r, "http.error"), Body: s.T(r, key)})
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("request failed", "path", r.URL.Path, "error", err)
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
	base, err := template.New("").Funcs(funcs).ParseFS(fsys, "templates/*.html", "templates/components/*.html")
	if err != nil {
		return nil, fmt.Errorf("load shared templates: %w", err)
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
			return nil, fmt.Errorf("load template %s: %w", f, err)
		}
		// «component» ejecuta la plantilla de un componente visual por su
		// nombre: así las páginas recorren la lista de componentes sin
		// conocerlos uno a uno.
		t.Funcs(template.FuncMap{"component": func(name string, data any) (template.HTML, error) {
			var b bytes.Buffer
			if err := t.ExecuteTemplate(&b, name, data); err != nil {
				return "", err
			}
			return template.HTML(b.String()), nil
		}})
		pages[strings.TrimSuffix(path.Base(f), ".html")] = t
	}
	return pages, nil
}

// ---------------------------------------------------------------- temas

func (s *Server) themeURL(id string) string {
	c, ok := s.themes[id]
	if !ok {
		id, c = theme.DefaultID, s.themes[theme.DefaultID]
	}
	return "/theme/" + id + ".css?v=" + c.ETag
}

// handleTheme sirve la hoja de estilos generada de un tema.
func (s *Server) handleTheme(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutSuffix(r.PathValue("file"), ".css")
	c, found := s.themes[id]
	if !ok || !found {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/css; charset=utf-8")
	h.Set("ETag", `"`+c.ETag+`"`)
	if r.URL.Query().Get("v") == c.ETag {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(c.CSS))
}
