package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"qaphi/internal/auth"
	_ "qaphi/internal/checks" // registra los tipos de check
	"qaphi/internal/model"
	"qaphi/internal/state"
	"qaphi/internal/store/sqlite"
)

const testPassword = "una contraseña de prueba"

type fakeScheduler struct {
	mu      sync.Mutex
	set     []model.Check
	removed []int64
}

func (f *fakeScheduler) Set(c model.Check) { f.mu.Lock(); f.set = append(f.set, c); f.mu.Unlock() }
func (f *fakeScheduler) Remove(id int64) {
	f.mu.Lock()
	f.removed = append(f.removed, id)
	f.mu.Unlock()
}

type env struct {
	t       *testing.T
	srv     *httptest.Server
	client  *http.Client
	store   *sqlite.Store
	token   *auth.SetupToken
	sched   *fakeScheduler
	tracker *state.Tracker
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	admin, err := auth.NewAdmin(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := &env{t: t, store: st, token: auth.NewSetupToken(), sched: &fakeScheduler{}, tracker: state.New(st, log)}
	s, err := New(Deps{
		Log: log, Store: st, Admin: admin, Sessions: auth.NewSessions(st, nil),
		SetupToken: e.token, Limiter: auth.DefaultLimiter(), Tracker: e.tracker, Scheduler: e.sched,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.srv = httptest.NewServer(s)
	t.Cleanup(e.srv.Close)
	jar, _ := cookiejar.New(nil)
	e.client = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return e
}

type resp struct {
	status   int
	location string
	body     string
	header   http.Header
}

func (e *env) do(method, path string, form url.Values, hdr map[string]string) resp {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, location: res.Header.Get("Location"), body: string(b), header: res.Header}
}

func (e *env) get(path string) resp { return e.do(http.MethodGet, path, nil, nil) }

func (e *env) post(path string, form url.Values) resp { return e.do(http.MethodPost, path, form, nil) }

var csrfRe = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// csrfFrom obtiene el token CSRF del formulario de una página.
func (e *env) csrfFrom(path string) string {
	e.t.Helper()
	r := e.get(path)
	m := csrfRe.FindStringSubmatch(r.body)
	if m == nil {
		e.t.Fatalf("no hay token CSRF en %s (estado %d)", path, r.status)
	}
	return m[1]
}

func expect(t *testing.T, r resp, status int, location string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("estado = %d, se esperaba %d (cuerpo: %.300s)", r.status, status, r.body)
	}
	if location != "" && r.location != location {
		t.Fatalf("Location = %q, se esperaba %q", r.location, location)
	}
}

// setup completa la configuración inicial y deja la sesión iniciada.
func (e *env) setup() {
	e.t.Helper()
	csrf := e.csrfFrom("/setup")
	expect(e.t, e.post("/setup", url.Values{"csrf": {csrf}, "token": {e.token.String()}, "password": {testPassword}, "confirm": {testPassword}}),
		http.StatusSeeOther, "/admin?m=welcome")
}

func TestSetup(t *testing.T) {
	e := newEnv(t)
	// Sin contraseña, todo lleva a /setup (salvo los estáticos).
	for _, p := range []string{"/", "/home", "/admin", "/login"} {
		expect(t, e.get(p), http.StatusSeeOther, "/setup")
	}
	expect(t, e.get("/static/style.css"), http.StatusOK, "")

	csrf := e.csrfFrom("/setup")
	form := func(token, pw, confirm string) url.Values {
		return url.Values{"csrf": {csrf}, "token": {token}, "password": {pw}, "confirm": {confirm}}
	}
	r := e.post("/setup", form("AAAA-BBBB-CCCC-DDDD", testPassword, testPassword))
	expect(t, r, http.StatusUnauthorized, "")
	if !strings.Contains(r.body, "token de configuración no es correcto") {
		t.Error("falta el mensaje de token incorrecto")
	}
	expect(t, e.post("/setup", form(e.token.String(), "corta", "corta")), http.StatusBadRequest, "")
	expect(t, e.post("/setup", form(e.token.String(), testPassword, testPassword+"x")), http.StatusBadRequest, "")
	// Sin CSRF.
	expect(t, e.post("/setup", url.Values{"token": {e.token.String()}, "password": {testPassword}, "confirm": {testPassword}}), http.StatusForbidden, "")

	expect(t, e.post("/setup", form(strings.ToLower(e.token.String()), testPassword, testPassword)), http.StatusSeeOther, "/admin?m=welcome")
	// Ya con sesión: la administración es accesible y /setup ha dejado de existir.
	r = e.get("/admin?m=welcome")
	expect(t, r, http.StatusOK, "")
	if !strings.Contains(r.body, "Contraseña guardada") {
		t.Error("falta el mensaje de bienvenida")
	}
	expect(t, e.get("/setup"), http.StatusNotFound, "")
	if e.token.Check(e.token.String()) {
		t.Error("el token de configuración debe quedar inutilizado")
	}
}

func TestSetupLimiteDeIntentos(t *testing.T) {
	e := newEnv(t)
	csrf := e.csrfFrom("/setup")
	bad := url.Values{"csrf": {csrf}, "token": {"MALO"}, "password": {testPassword}, "confirm": {testPassword}}
	for i := 0; i < 5; i++ {
		e.post("/setup", bad)
	}
	good := url.Values{"csrf": {csrf}, "token": {e.token.String()}, "password": {testPassword}, "confirm": {testPassword}}
	expect(t, e.post("/setup", good), http.StatusTooManyRequests, "")
}

func TestLogin(t *testing.T) {
	e := newEnv(t)
	e.setup()
	expect(t, e.post("/logout", url.Values{"csrf": {e.csrfFrom("/admin")}}), http.StatusSeeOther, "/login")
	expect(t, e.get("/home"), http.StatusSeeOther, "/login?next=%2Fhome")

	csrf := e.csrfFrom("/login?next=/admin")
	r := e.post("/login", url.Values{"csrf": {csrf}, "password": {"incorrecta"}, "next": {"/admin"}})
	expect(t, r, http.StatusUnauthorized, "")
	if !strings.Contains(r.body, "Contraseña incorrecta") {
		t.Error("falta el mensaje de error")
	}
	// Redirección abierta: «next» externo se ignora.
	expect(t, e.post("/login", url.Values{"csrf": {csrf}, "password": {testPassword}, "next": {"//malo.example"}}), http.StatusSeeOther, "/home")
	expect(t, e.get("/home"), http.StatusOK, "")
	// Con sesión, /login lleva a la página pedida.
	expect(t, e.get("/login?next=/admin"), http.StatusSeeOther, "/admin")
}

func TestLoginLimiteDeIntentos(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.post("/logout", url.Values{"csrf": {e.csrfFrom("/admin")}})
	csrf := e.csrfFrom("/login")
	for i := 0; i < 5; i++ {
		e.post("/login", url.Values{"csrf": {csrf}, "password": {"mal"}})
	}
	r := e.post("/login", url.Values{"csrf": {csrf}, "password": {testPassword}})
	expect(t, r, http.StatusTooManyRequests, "")
	if !strings.Contains(r.body, "Demasiados intentos") {
		t.Error("falta el mensaje de bloqueo")
	}
}

func TestCookiesDeSesion(t *testing.T) {
	e := newEnv(t)
	csrf := e.csrfFrom("/setup")
	r := e.post("/setup", url.Values{"csrf": {csrf}, "token": {e.token.String()}, "password": {testPassword}, "confirm": {testPassword}})
	var found bool
	for _, c := range r.header.Values("Set-Cookie") {
		if strings.HasPrefix(c, sessionCookie+"=") {
			found = true
			for _, attr := range []string{"HttpOnly", "SameSite=Strict", "Path=/"} {
				if !strings.Contains(c, attr) {
					t.Errorf("la cookie de sesión no tiene %s: %s", attr, c)
				}
			}
			if strings.Contains(c, "Secure") {
				t.Errorf("por HTTP no debe llevar Secure: %s", c)
			}
		}
	}
	if !found {
		t.Fatal("no se creó la cookie de sesión")
	}
}

func TestCSRFEnAdmin(t *testing.T) {
	e := newEnv(t)
	e.setup()
	form := url.Values{"type": {"tcp"}, "name": {"x"}, "interval": {"60s"}, "p_host": {"127.0.0.1"}, "p_port": {"22"}}
	expect(t, e.post("/admin/checks", form), http.StatusForbidden, "")
	form.Set("csrf", "otro-token-cualquiera")
	expect(t, e.post("/admin/checks", form), http.StatusForbidden, "")
	form.Set("csrf", e.csrfFrom("/admin"))
	expect(t, e.do(http.MethodPost, "/admin/checks", form, map[string]string{"Origin": "https://malo.example"}), http.StatusForbidden, "")
	expect(t, e.do(http.MethodPost, "/admin/checks", form, map[string]string{"Sec-Fetch-Site": "cross-site"}), http.StatusForbidden, "")
	expect(t, e.post("/admin/checks", form), http.StatusSeeOther, "/admin?m=created")
}

func TestCRUDChecks(t *testing.T) {
	e := newEnv(t)
	e.setup()
	ctx := context.Background()

	// Elegir tipo y formulario generado a partir de la declaración.
	r := e.get("/admin/checks/new")
	expect(t, r, http.StatusOK, "")
	for _, s := range []string{"type=http", "type=tcp"} {
		if !strings.Contains(r.body, s) {
			t.Errorf("falta el tipo %s", s)
		}
	}
	r = e.get("/admin/checks/new?type=http")
	for _, s := range []string{`name="p_url"`, `name="p_expected_status"`, `name="interval"`, `value="200-299"`} {
		if !strings.Contains(r.body, s) {
			t.Errorf("el formulario no contiene %s", s)
		}
	}

	csrf := e.csrfFrom("/admin")
	form := url.Values{
		"csrf": {csrf}, "type": {"http"}, "name": {"Web"}, "group": {"Servicios"}, "interval": {"30s"},
		"p_url": {"ftp://no-vale"}, "p_method": {"GET"}, "p_expected_status": {"200"}, "p_timeout": {"10s"},
		"p_follow_redirects": {"false", "true"}, "p_insecure_tls": {"false"},
	}
	r = e.post("/admin/checks", form)
	expect(t, r, http.StatusUnprocessableEntity, "")
	if !strings.Contains(r.body, "http:// o https://") || !strings.Contains(r.body, `value="Web"`) {
		t.Error("el formulario con errores debe mostrar el error y conservar lo escrito")
	}
	form.Set("p_url", "https://ejemplo.org")
	form.Set("p_timeout", "40s") // mayor que el intervalo
	r = e.post("/admin/checks", form)
	expect(t, r, http.StatusUnprocessableEntity, "")
	if !strings.Contains(r.body, "intervalo debe ser mayor") {
		t.Error("falta el error de intervalo frente a tiempo máximo")
	}
	form.Set("p_timeout", "10s")
	expect(t, e.post("/admin/checks", form), http.StatusSeeOther, "/admin?m=created")

	cs, _ := e.store.ListChecks(ctx)
	if len(cs) != 1 {
		t.Fatalf("checks = %v", cs)
	}
	c := cs[0]
	if c.Name != "Web" || c.Group != "Servicios" || c.Interval != 30*time.Second || c.Params["url"] != "https://ejemplo.org" ||
		c.Params["follow_redirects"] != "true" || c.Params["insecure_tls"] != "false" {
		t.Fatalf("check guardado = %+v", c)
	}
	if len(e.sched.set) != 1 || e.sched.set[0].ID != c.ID {
		t.Fatal("el planificador no recibió el check nuevo")
	}

	// /home y /admin lo muestran.
	r = e.get("/home")
	if !strings.Contains(r.body, "Web") || !strings.Contains(r.body, "https://ejemplo.org") || !strings.Contains(r.body, "0/1 (1 sin datos)") {
		t.Errorf("/home no muestra el check o el resumen: %.500s", r.body)
	}
	if !strings.Contains(r.body, `http-equiv="refresh"`) {
		t.Error("/home debe recargarse sola")
	}
	e.tracker.Record(model.Result{CheckID: c.ID, At: time.Now(), OK: true, Latency: 12 * time.Millisecond, Message: "HTTP 200"})
	r = e.get("/home")
	if !strings.Contains(r.body, "1/1 (todos up)") || !strings.Contains(r.body, "12 ms") || !strings.Contains(r.body, "Operativo") {
		t.Errorf("/home no refleja el resultado: %.800s", r.body)
	}

	// Editar.
	path := "/admin/checks/" + itoa(c.ID)
	r = e.get(path)
	if !strings.Contains(r.body, `value="https://ejemplo.org"`) || !strings.Contains(r.body, `value="Web"`) {
		t.Error("el formulario de edición no tiene los valores actuales")
	}
	form.Set("name", "Web editado")
	form.Set("paused", "true")
	expect(t, e.post(path, form), http.StatusSeeOther, "/admin?m=updated")
	c2, _ := e.store.GetCheck(ctx, c.ID)
	if c2.Name != "Web editado" || !c2.Paused {
		t.Fatalf("check editado = %+v", c2)
	}
	if st, _ := e.tracker.Get(c.ID); st.Status != model.StatusPaused {
		t.Errorf("el estado debe pasar a pausado: %s", st.Status)
	}

	// Borrar.
	expect(t, e.get(path+"/delete"), http.StatusOK, "")
	expect(t, e.post(path+"/delete", url.Values{"csrf": {csrf}}), http.StatusSeeOther, "/admin?m=deleted")
	if cs, _ := e.store.ListChecks(ctx); len(cs) != 0 {
		t.Fatal("el check no se borró")
	}
	if len(e.sched.removed) != 1 {
		t.Fatal("el planificador no supo del borrado")
	}
	expect(t, e.get(path), http.StatusNotFound, "")
	expect(t, e.get("/admin/checks/abc"), http.StatusNotFound, "")
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/setup", "/static/style.css", "/no-existe"} {
		r := e.get(p)
		for h, want := range map[string]string{
			"X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "same-origin",
		} {
			if got := r.header.Get(h); got != want {
				t.Errorf("%s: %s = %q", p, h, got)
			}
		}
		if csp := r.header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("%s: CSP = %q", p, csp)
		}
	}
}

func TestStatic(t *testing.T) {
	e := newEnv(t)
	sf, err := loadStatic(staticFS, "static")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := sf.url("style.css")
	r := e.get(u)
	expect(t, r, http.StatusOK, "")
	if !strings.Contains(r.header.Get("Cache-Control"), "immutable") {
		t.Errorf("Cache-Control = %q", r.header.Get("Cache-Control"))
	}
	etag := r.header.Get("ETag")
	r = e.do(http.MethodGet, "/static/style.css", nil, map[string]string{"If-None-Match": etag})
	expect(t, r, http.StatusNotModified, "")
	expect(t, e.get("/static/nada.css"), http.StatusNotFound, "")
}

func TestRecoverPanics(t *testing.T) {
	var logs strings.Builder
	h := recoverPanics(slog.New(slog.NewTextHandler(&logs, nil)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("algo salió mal")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), "algo salió mal") {
		t.Fatalf("estado %d, log %q", rec.Code, logs.String())
	}
}

func TestLogRequestsSinQuery(t *testing.T) {
	var logs strings.Builder
	h := logRequests(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ruta?token=secreto", nil))
	if out := logs.String(); strings.Contains(out, "secreto") || !strings.Contains(out, "418") {
		t.Errorf("log = %s", out)
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"": "/home", "/admin": "/admin", "//malo.example": "/home", "https://malo.example": "/home",
		"/\\malo.example": "/home", "/a\r\nSet-Cookie: x": "/home", "admin": "/home",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, se esperaba %q", in, got, want)
		}
	}
}

func TestFormato(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "—", 400 * time.Microsecond: "0.4 ms", 12 * time.Millisecond: "12 ms", 12 * time.Second: "12.0 s",
	} {
		if got := formatLatency(d); got != want {
			t.Errorf("formatLatency(%v) = %q, se esperaba %q", d, got, want)
		}
	}
	now := time.Unix(1_800_000_000, 0)
	for d, want := range map[time.Duration]string{
		0: "ahora", 5 * time.Second: "hace 5 s", 3 * time.Minute: "hace 3 min", 5 * time.Hour: "hace 5 h", 72 * time.Hour: "hace 3 d",
	} {
		if got := ago(now, now.Add(-d)); got != want {
			t.Errorf("ago(%v) = %q, se esperaba %q", d, got, want)
		}
	}
	for d, want := range map[time.Duration]string{time.Minute: "1m", 90 * time.Minute: "1h30m", 30 * time.Second: "30s", time.Hour: "1h"} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, se esperaba %q", d, got, want)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
