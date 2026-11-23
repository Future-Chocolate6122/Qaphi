package web

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
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

	"github.com/future-chocolate6122/qaphi/internal/auth"
	_ "github.com/future-chocolate6122/qaphi/internal/checks" // registra los tipos de check
	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/netcfg"
	"github.com/future-chocolate6122/qaphi/internal/settings"
	"github.com/future-chocolate6122/qaphi/internal/state"
	"github.com/future-chocolate6122/qaphi/internal/store/sqlite"
)

const testPassword = "una contraseña de prueba"

type fakeScheduler struct {
	mu        sync.Mutex
	set       []model.Check
	removed   []int64
	triggered []int64
}

func (f *fakeScheduler) Set(c model.Check) { f.mu.Lock(); f.set = append(f.set, c); f.mu.Unlock() }
func (f *fakeScheduler) Remove(id int64) {
	f.mu.Lock()
	f.removed = append(f.removed, id)
	f.mu.Unlock()
}

func (f *fakeScheduler) Trigger(id int64) {
	f.mu.Lock()
	f.triggered = append(f.triggered, id)
	f.mu.Unlock()
}

type env struct {
	t       *testing.T
	set     *settings.Service
	net     *fakeNetwork
	lang    string // Accept-Language de las peticiones
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
	set, err := settings.Load(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, store: st, token: auth.NewSetupToken(), sched: &fakeScheduler{}, tracker: state.New(st, log, nil),
		set: set, net: &fakeNetwork{cur: netcfg.Config{Port: 9077}}, lang: "es"}
	s, err := New(Deps{
		Log: log, Store: st, Settings: set, Admin: admin, Sessions: auth.NewSessions(st, nil),
		SetupToken: e.token, Limiter: auth.DefaultLimiter(), Tracker: e.tracker, Scheduler: e.sched,
		Network: e.net, Certs: e.net,
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
	if e.lang != "" {
		req.Header.Set("Accept-Language", e.lang)
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
	e.tracker.Record(state.Observation{CheckID: c.ID, At: time.Now(), OK: true, Latency: 12 * time.Millisecond, Message: msg.New("check.http.msg.status", 200)})
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
		0: "—", 400 * time.Microsecond: "0,4 ms", 12 * time.Millisecond: "12 ms", 12 * time.Second: "12,0 s",
	} {
		if got := formatLatency(msg.ES, d); got != want {
			t.Errorf("formatLatency(%v) = %q, se esperaba %q", d, got, want)
		}
	}
	now := time.Unix(1_800_000_000, 0)
	for d, want := range map[time.Duration]string{
		0: "ahora", 5 * time.Second: "hace 5 s", 3 * time.Minute: "hace 3 min", 5 * time.Hour: "hace 5 h", 72 * time.Hour: "hace 3 d",
	} {
		if got := ago(msg.ES, now, now.Add(-d)); got != want {
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

func TestFormatoPorIdioma(t *testing.T) {
	if got := formatLatency(msg.EN, 400*time.Microsecond); got != "0.4 ms" {
		t.Errorf("en: %q", got)
	}
	now := time.Unix(1_800_000_000, 0)
	for l, want := range map[msg.Lang]string{msg.EN: "3 min ago", msg.DE: "vor 3 min", msg.FR: "il y a 3 min"} {
		if got := ago(l, now, now.Add(-3*time.Minute)); got != want {
			t.Errorf("%s: %q", l, got)
		}
	}
	loc, _ := time.LoadLocation("Europe/Madrid")
	ts := time.Date(2026, 11, 21, 9, 5, 7, 0, time.UTC)
	for l, want := range map[msg.Lang]string{msg.ES: "21/11/2026 10:05:07 CET", msg.EN: "2026-11-21 10:05:07 CET", msg.DE: "21.11.2026 10:05:07 CET"} {
		if got := formatDateTime(l, loc, ts); got != want {
			t.Errorf("%s: %q", l, got)
		}
	}
}

// fakeNetwork simula el gestor de red y el certificado.
type fakeNetwork struct {
	cur       netcfg.Config
	pending   *netcfg.Config
	inPlace   bool
	locked    bool
	applyErr  error
	confirmOK bool
	reverted  bool
	regens    int
}

func (f *fakeNetwork) Current() netcfg.Config { return f.cur }
func (f *fakeNetwork) Locked() bool           { return f.locked }
func (f *fakeNetwork) Pending() (netcfg.Config, time.Time, bool, bool) {
	if f.pending == nil {
		return netcfg.Config{}, time.Time{}, false, false
	}
	return *f.pending, time.Now().Add(2 * time.Minute), f.inPlace, true
}
func (f *fakeNetwork) Apply(_ context.Context, c netcfg.Config) (bool, error) {
	if f.applyErr != nil {
		return false, f.applyErr
	}
	if c == f.cur {
		return false, netcfg.ErrUnchanged
	}
	f.pending = &c
	f.inPlace = c.Port == f.cur.Port
	return f.inPlace, nil
}
func (f *fakeNetwork) Confirm(*http.Request) error {
	if f.pending == nil {
		return netcfg.ErrNoPending
	}
	if !f.confirmOK {
		return netcfg.ErrWrongEndpoint
	}
	f.cur, f.pending = *f.pending, nil
	return nil
}
func (f *fakeNetwork) Cancel() error {
	if f.pending == nil {
		return netcfg.ErrNoPending
	}
	f.pending = nil
	return nil
}
func (f *fakeNetwork) TakeReverted() bool { r := f.reverted; f.reverted = false; return r }
func (f *fakeNetwork) Info() (netcfg.CertInfo, bool) {
	if f.regens == 0 {
		return netcfg.CertInfo{}, false
	}
	return netcfg.CertInfo{NotAfter: time.Date(2031, 1, 2, 0, 0, 0, 0, time.UTC), Fingerprint: "AB:CD"}, true
}
func (f *fakeNetwork) Regenerate(context.Context) error { f.regens++; return nil }

func TestIdiomas(t *testing.T) {
	e := newEnv(t)
	// Antes del setup: el idioma del navegador.
	e.lang = "de-DE,de;q=0.9"
	if r := e.get("/setup"); !strings.Contains(r.body, "Ersteinrichtung") || !strings.Contains(r.body, `lang="de"`) {
		t.Fatalf("/setup en alemán: %.300s", r.body)
	}
	// Selector de idioma de /setup: ?lang=fr se recuerda en una cookie.
	expect(t, e.get("/setup?lang=fr"), http.StatusSeeOther, "/setup")
	if r := e.get("/setup"); !strings.Contains(r.body, "Configuration initiale") {
		t.Fatalf("/setup en francés: %.300s", r.body)
	}
	// El setup fija el idioma como ajuste global.
	csrf := e.csrfFrom("/setup")
	expect(t, e.post("/setup", url.Values{"csrf": {csrf}, "token": {e.token.String()}, "password": {testPassword}, "confirm": {testPassword}}),
		http.StatusSeeOther, "/admin?m=welcome")
	if ui := e.set.UI(); !ui.LangSet || ui.Lang != msg.FR {
		t.Fatalf("idioma guardado = %+v", ui)
	}
	e.lang = "es" // ya no importa el navegador
	if r := e.get("/admin?m=welcome"); !strings.Contains(r.body, "Mot de passe enregistré") || !strings.Contains(r.body, "Paramètres") {
		t.Fatalf("/admin en francés: %.500s", r.body)
	}
	// Cambiarlo en Ajustes.
	csrf = e.csrfFrom("/admin/settings")
	expect(t, e.post("/admin/settings", url.Values{"csrf": {csrf}, "language": {"en"}, "theme": {"auto"}, "timezone": {""}}),
		http.StatusSeeOther, "/admin/settings?m=settings")
	if r := e.get("/admin/settings?m=settings"); !strings.Contains(r.body, "Settings saved.") || !strings.Contains(r.body, `lang="en"`) {
		t.Fatalf("ajustes en inglés: %.500s", r.body)
	}
}

func TestTemaYZonaHoraria(t *testing.T) {
	e := newEnv(t)
	if r := e.get("/setup"); !strings.Contains(r.body, `href="/theme/auto.css?v=`) {
		t.Fatal("el tema por defecto debe ser el automático")
	}
	e.setup()
	csrf := e.csrfFrom("/admin/settings")
	r := e.get("/admin/settings")
	for _, s := range []string{`value="dark"`, `value="Europe/Madrid"`, "Oscuro"} {
		if !strings.Contains(r.body, s) {
			t.Errorf("la página de ajustes no contiene %s", s)
		}
	}
	expect(t, e.post("/admin/settings", url.Values{"csrf": {csrf}, "language": {"es"}, "theme": {"dark"}, "timezone": {"Europe/Madrid"}}),
		http.StatusSeeOther, "/admin/settings?m=settings")
	r = e.get("/home")
	if !strings.Contains(r.body, `href="/theme/dark.css?v=`) {
		t.Fatal("no se aplica el tema oscuro")
	}
	if e.set.Location().String() != "Europe/Madrid" {
		t.Error("no se guardó la zona horaria")
	}
	// La hoja del tema.
	css := e.get("/theme/dark.css")
	if css.status != 200 || !strings.Contains(css.body, "color-scheme: dark") || !strings.HasPrefix(css.header.Get("Content-Type"), "text/css") {
		t.Fatalf("hoja del tema: %d %s", css.status, css.body)
	}
	expect(t, e.get("/theme/rosa.css"), http.StatusNotFound, "")
	// Valores no válidos.
	expect(t, e.post("/admin/settings", url.Values{"csrf": {csrf}, "language": {"es"}, "theme": {"rosa"}, "timezone": {""}}), http.StatusBadRequest, "")
	expect(t, e.post("/admin/settings", url.Values{"csrf": {csrf}, "language": {"it"}, "theme": {"dark"}, "timezone": {""}}), http.StatusBadRequest, "")
}

func TestEstadoEnVivo(t *testing.T) {
	e := newEnv(t)
	e.setup()
	c := model.Check{Name: "Web", Type: "tcp", Interval: time.Minute, FailThreshold: 1, Params: map[string]string{"host": "127.0.0.1", "port": "1", "timeout": "1s"}}
	if err := e.store.CreateCheck(context.Background(), &c); err != nil {
		t.Fatal(err)
	}
	e.tracker.SetCheck(c)
	e.tracker.Record(state.Observation{CheckID: c.ID, At: time.Now().Add(-5 * time.Second), OK: false,
		Message: msg.New("check.msg.refused")})
	r := e.get("/home")
	if !strings.Contains(r.body, `data-live="/home/state.json"`) || !strings.Contains(r.body, "js/qaphi.js") || !strings.Contains(r.body, "<noscript>") {
		t.Fatal("/home debe cargar la actualización en vivo con alternativa sin JavaScript")
	}
	if !strings.Contains(r.body, "conexión rechazada") {
		t.Error("el mensaje del resultado debe mostrarse traducido")
	}
	r = e.get("/home/state.json")
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "application/json") {
		t.Fatalf("state.json: %d %s", r.status, r.header.Get("Content-Type"))
	}
	var data struct {
		Summary string `json:"summary"`
		Level   string `json:"level"`
		Down    int    `json:"down"`
		Groups  []struct {
			Name   string `json:"name"`
			Checks []struct {
				ID          int64  `json:"id"`
				Target      string `json:"target"`
				Status      string `json:"status"`
				StatusLabel string `json:"status_label"`
				Message     string `json:"message"`
				Last        string `json:"last"`
				Uptime      string `json:"uptime"`
				Beats       []struct {
					S string  `json:"s"`
					H float64 `json:"h"`
					T string  `json:"t"`
				} `json:"beats"`
			} `json:"checks"`
		} `json:"groups"`
		Incidents []struct {
			Check string `json:"check"`
			Open  bool   `json:"open"`
			Error string `json:"error"`
		} `json:"incidents"`
	}
	if err := json.Unmarshal([]byte(r.body), &data); err != nil {
		t.Fatal(err)
	}
	if data.Summary != "0/1 (1 caído)" || data.Level != "down" || data.Down != 1 || len(data.Groups) != 1 || len(data.Groups[0].Checks) != 1 {
		t.Fatalf("datos en vivo = %+v", data)
	}
	ck := data.Groups[0].Checks[0]
	if ck.StatusLabel != "Caído" || ck.Message != "conexión rechazada" || !strings.HasPrefix(ck.Last, "hace") ||
		ck.Target != "127.0.0.1:1" || ck.Uptime != "0 %" || len(ck.Beats) != 1 || ck.Beats[0].S != "down" ||
		!strings.Contains(ck.Beats[0].T, "conexión rechazada") {
		t.Fatalf("check en vivo = %+v", ck)
	}
	if len(data.Incidents) != 1 || !data.Incidents[0].Open || data.Incidents[0].Error != "conexión rechazada" {
		t.Fatalf("incidencias = %+v", data.Incidents)
	}
	// Sin sesión no hay datos.
	e.client.Jar, _ = cookiejar.New(nil)
	if r := e.get("/home/state.json"); r.status != http.StatusSeeOther {
		t.Errorf("sin sesión: %d", r.status)
	}
}

func TestRed(t *testing.T) {
	e := newEnv(t)
	e.setup()
	csrf := e.csrfFrom("/admin/settings")
	form := func(bind, port, proto string) url.Values {
		return url.Values{"csrf": {csrf}, "bind": {bind}, "port": {port}, "protocol": {proto}}
	}
	r := e.get("/admin/settings")
	if !strings.Contains(r.body, "http://0.0.0.0:9077") || !strings.Contains(r.body, `name="port"`) {
		t.Fatalf("sección de red: %.500s", r.body)
	}
	// Validación.
	r = e.post("/admin/settings/network", form("", "80", "http"))
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "entre 1024 y 65535") || !strings.Contains(r.body, `value="80"`) {
		t.Fatalf("puerto 80: %d %.300s", r.status, r.body)
	}
	r = e.post("/admin/settings/network", form("10.99.99.99", "9077", "http"))
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "no pertenece a este equipo") {
		t.Fatalf("IP ajena: %d", r.status)
	}
	expect(t, e.post("/admin/settings/network", form("", "9077", "http")), http.StatusUnprocessableEntity, "")
	// Aplicar un cambio de puerto: página de confirmación pendiente.
	r = e.post("/admin/settings/network", form("127.0.0.1", "9100", "https"))
	if r.status != 200 || !strings.Contains(r.body, "https://127.0.0.1:9100/admin/settings/network/confirm") {
		t.Fatalf("cambio pendiente: %d %.500s", r.status, r.body)
	}
	if r := e.get("/admin/settings"); !strings.Contains(r.body, "Cambio pendiente de confirmar") {
		t.Error("los ajustes deben mostrar el cambio pendiente")
	}
	// Un segundo cambio mientras hay uno pendiente.
	e.net.applyErr = netcfg.ErrPending
	if r := e.post("/admin/settings/network", form("", "9200", "http")); r.status != http.StatusConflict {
		t.Errorf("segundo cambio: %d", r.status)
	}
	e.net.applyErr = nil
	// Confirmar desde la dirección equivocada.
	if r := e.post("/admin/settings/network/confirm", url.Values{"csrf": {csrf}}); r.status != http.StatusConflict || !strings.Contains(r.body, "nueva dirección") {
		t.Fatalf("confirmar desde la anterior: %d %.300s", r.status, r.body)
	}
	e.net.confirmOK = true
	expect(t, e.post("/admin/settings/network/confirm", url.Values{"csrf": {csrf}}), http.StatusSeeOther, "/admin/settings?m=network")
	if e.net.cur.Port != 9100 || !e.net.cur.TLS {
		t.Fatalf("configuración tras confirmar = %+v", e.net.cur)
	}
	// Cancelar.
	e.post("/admin/settings/network", form("", "9300", "http"))
	expect(t, e.post("/admin/settings/network/cancel", url.Values{"csrf": {csrf}}), http.StatusSeeOther, "/admin/settings?m=cancelled")
	if e.net.pending != nil {
		t.Error("el cambio no se canceló")
	}
	// Aviso de vuelta atrás por no confirmar.
	e.net.reverted = true
	if r := e.get("/admin/settings"); !strings.Contains(r.body, "no se confirmó a tiempo") {
		t.Error("falta el aviso de vuelta atrás")
	}
	// Certificado.
	expect(t, e.post("/admin/settings/cert", url.Values{"csrf": {csrf}}), http.StatusSeeOther, "/admin/settings?m=cert")
	if r := e.get("/admin/settings"); !strings.Contains(r.body, "AB:CD") || !strings.Contains(r.body, "02/01/2031") {
		t.Errorf("datos del certificado: %.300s", r.body)
	}
	// Dirección fijada al arrancar.
	e.net.locked = true
	if r := e.get("/admin/settings"); !strings.Contains(r.body, "fijada al arrancar") || strings.Contains(r.body, `name="port"`) {
		t.Error("con --listen la red no debe poder cambiarse")
	}
}

// TestClavesDeLasPlantillas comprueba que todas las claves de texto usadas en
// las plantillas existen.
func TestClavesDeLasPlantillas(t *testing.T) {
	re := regexp.MustCompile(`\{\{-?\s*tf?\s+"([^"]+)"`)
	err := fs.WalkDir(templateFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := fs.ReadFile(templateFS, p)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if !msg.Has(m[1]) {
				t.Errorf("%s: clave de texto inexistente %q", p, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// addCheck crea un check y le registra un resultado.
func (e *env) addCheck(c model.Check, o state.Observation) model.Check {
	e.t.Helper()
	if err := e.store.CreateCheck(context.Background(), &c); err != nil {
		e.t.Fatal(err)
	}
	e.tracker.SetCheck(c)
	o.CheckID = c.ID
	if o.At.IsZero() {
		o.At = time.Now()
	}
	e.tracker.Record(o)
	return c
}

func TestPaginaPublica(t *testing.T) {
	e := newEnv(t)
	e.setup()
	secret := "http://10.20.30.40:8080/salud"
	c := e.addCheck(model.Check{Name: "Interno", Type: "http", Group: "Red", Interval: time.Minute, FailThreshold: 1,
		Params: map[string]string{"url": secret, "method": "GET", "expected_status": "200", "timeout": "5s"}},
		state.Observation{OK: false, Message: msg.New("check.msg.error", "dial tcp 10.20.30.40:8080: connect: connection refused")})

	// Sin página pública: sin sesión, al login.
	e.post("/logout", url.Values{"csrf": {e.csrfFrom("/admin")}})
	expect(t, e.get("/home"), http.StatusSeeOther, "/login?next=%2Fhome")
	expect(t, e.get("/check/"+itoa(c.ID)), http.StatusSeeOther, "")

	// Activarla.
	e.set.SetData(context.Background(), settings.Data{Public: true, RawHours: 48, AggregateDays: 90, SessionTTL: time.Hour})
	for _, path := range []string{"/home", "/home/state.json", "/check/" + itoa(c.ID), "/check/" + itoa(c.ID) + "/data.json"} {
		r := e.get(path)
		if r.status != 200 {
			t.Fatalf("%s: estado %d", path, r.status)
		}
		for _, leak := range []string{"10.20.30.40", "8080", "salud", "connection refused"} {
			if strings.Contains(r.body, leak) {
				t.Errorf("%s muestra %q en modo público", path, leak)
			}
		}
	}
	if r := e.get("/home"); !strings.Contains(r.body, "Interno") || !strings.Contains(r.body, "Iniciar sesión") || strings.Contains(r.body, "Cerrar sesión") {
		t.Error("la página pública debe mostrar el nombre y el enlace de login")
	}
	// La administración sigue cerrada.
	expect(t, e.get("/admin"), http.StatusSeeOther, "/login?next=%2Fadmin")

	// Con sesión se ve todo.
	csrf := e.csrfFrom("/login")
	e.post("/login", url.Values{"csrf": {csrf}, "password": {testPassword}})
	if r := e.get("/home/state.json"); !strings.Contains(r.body, "10.20.30.40") {
		t.Error("con sesión deben verse los destinos")
	}
}

func TestPaginaDeDetalle(t *testing.T) {
	e := newEnv(t)
	e.setup()
	c := e.addCheck(model.Check{Name: "API", Type: "tcp", Interval: time.Minute,
		Params: map[string]string{"host": "127.0.0.1", "port": "9", "timeout": "1s"}},
		state.Observation{OK: true, Latency: 15 * time.Millisecond, Message: msg.New("check.tcp.msg.ok")})
	path := "/check/" + itoa(c.ID)
	r := e.get(path)
	for _, want := range []string{"API", "127.0.0.1:9", "data-chart", "data-heatmap", "data-ring", `data-range="7d"`, "Tiempo de respuesta", "Últimos 90 días"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("la página de detalle no contiene %q", want)
		}
	}
	for _, rng := range []string{"1h", "24h", "7d", "30d", "raro"} {
		r := e.get(path + "/data.json?range=" + rng)
		var d struct {
			Range  string `json:"range"`
			From   int64  `json:"from"`
			To     int64  `json:"to"`
			TZ     string `json:"tz"`
			Points []struct {
				V float64 `json:"v"`
				S string  `json:"s"`
			} `json:"points"`
			Heatmap []struct {
				V float64 `json:"v"`
			} `json:"heatmap"`
			Uptimes []struct{ Text string } `json:"uptimes"`
		}
		if err := json.Unmarshal([]byte(r.body), &d); err != nil {
			t.Fatalf("%s: %v", rng, err)
		}
		wantRange := rng
		if rng == "raro" {
			wantRange = "24h"
		}
		if d.Range != wantRange || d.To <= d.From || d.TZ == "" || len(d.Heatmap) != 90 || len(d.Uptimes) != 3 {
			t.Fatalf("%s: %+v", rng, d)
		}
		// El resultado aún no volcado aparece en todos los rangos.
		if len(d.Points) != 1 || d.Points[0].V != 15 || d.Points[0].S != "up" {
			t.Errorf("%s: puntos = %+v", rng, d.Points)
		}
		if d.Heatmap[89].V != 1 || d.Uptimes[0].Text != "100 %" {
			t.Errorf("%s: hoy = %v, uptime = %+v", rng, d.Heatmap[89].V, d.Uptimes)
		}
	}
	expect(t, e.get("/check/999"), http.StatusNotFound, "")
	expect(t, e.get("/check/999/data.json"), http.StatusNotFound, "")
}

func TestHeartbeat(t *testing.T) {
	e := newEnv(t)
	e.setup()
	// El formulario genera el token y lo muestra de solo lectura.
	r := e.get("/admin/checks/new?type=heartbeat")
	m := regexp.MustCompile(`name="p_token" type="text" value="([A-Z2-7]{26})"[^>]*readonly`).FindStringSubmatch(r.body)
	if m == nil {
		t.Fatalf("el formulario no trae el token generado: %.800s", r.body)
	}
	csrf := e.csrfFrom("/admin")
	expect(t, e.post("/admin/checks", url.Values{"csrf": {csrf}, "type": {"heartbeat"}, "name": {"Copia nocturna"}, "interval": {"60s"},
		"fail_threshold": {"1"}, "p_token": {m[1]}, "p_period": {"24h"}, "p_grace": {"1h"}}), http.StatusSeeOther, "/admin?m=created")

	// Pulso con el token: 200 y se pide comprobar ya.
	var logs strings.Builder
	e.srv.Config.Handler.(*Server).Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead} {
		if r := e.do(method, "/hb/"+m[1], nil, nil); r.status != 200 {
			t.Fatalf("%s /hb: %d", method, r.status)
		}
	}
	if len(e.sched.triggered) != 3 {
		t.Errorf("Trigger llamado %d veces", len(e.sched.triggered))
	}
	expect(t, e.get("/hb/OTROTOKENQUENOEXISTEXXXXXX"), http.StatusNotFound, "")
	if strings.Contains(logs.String(), m[1]) {
		t.Error("el token del heartbeat no debe aparecer en el log")
	}
	// Al borrar el check, el token deja de valer.
	cs, _ := e.store.ListChecks(context.Background())
	e.post("/admin/checks/"+itoa(cs[0].ID)+"/delete", url.Values{"csrf": {csrf}})
	expect(t, e.get("/hb/"+m[1]), http.StatusNotFound, "")
}

func TestAjustesDeDatosWeb(t *testing.T) {
	e := newEnv(t)
	e.setup()
	csrf := e.csrfFrom("/admin/settings")
	if r := e.get("/admin/settings"); !strings.Contains(r.body, "Página de estado pública") || !strings.Contains(r.body, `name="raw_hours"`) {
		t.Fatal("faltan los ajustes de datos")
	}
	r := e.post("/admin/settings/data", url.Values{"csrf": {csrf}, "public": {"false", "true"}, "raw_hours": {"0"}, "aggregate_days": {"90"}, "session_ttl": {"168h"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "fuera de rango") {
		t.Fatalf("valor fuera de rango: %d", r.status)
	}
	expect(t, e.post("/admin/settings/data", url.Values{"csrf": {csrf}, "public": {"false", "true"}, "raw_hours": {"24"},
		"aggregate_days": {"30"}, "aggregates_only": {"false", "true"}, "session_ttl": {"12h"}}), http.StatusSeeOther, "/admin/settings?m=data")
	d := e.set.Data()
	if !d.Public || d.RawHours != 24 || d.AggregateDays != 30 || !d.AggregatesOnly || d.SessionTTL != 12*time.Hour {
		t.Fatalf("ajustes guardados = %+v", d)
	}
	if !e.tracker.AggregatesOnly.Load() {
		t.Error("el modo solo agregados debe aplicarse al momento")
	}
}

func TestCamposComunesNuevos(t *testing.T) {
	e := newEnv(t)
	e.setup()
	csrf := e.csrfFrom("/admin")
	form := url.Values{"csrf": {csrf}, "type": {"tcp"}, "name": {"x"}, "interval": {"60s"}, "fail_threshold": {"5"},
		"degraded_after": {"250ms"}, "p_host": {"127.0.0.1"}, "p_port": {"22"}, "p_timeout": {"2s"}}
	expect(t, e.post("/admin/checks", form), http.StatusSeeOther, "/admin?m=created")
	cs, _ := e.store.ListChecks(context.Background())
	if cs[0].FailThreshold != 5 || cs[0].DegradedAfter != 250*time.Millisecond {
		t.Fatalf("check = %+v", cs[0])
	}
	if r := e.get("/admin/checks/" + itoa(cs[0].ID)); !strings.Contains(r.body, `value="250ms"`) || !strings.Contains(r.body, `value="5"`) {
		t.Error("el formulario de edición no muestra los umbrales")
	}
	form.Set("fail_threshold", "0")
	if r := e.post("/admin/checks", form); r.status != http.StatusUnprocessableEntity {
		t.Errorf("umbral 0: %d", r.status)
	}
}

// TestVistaDeCuadricula: la vista de /home sale de la cookie del visitante;
// por defecto, lista.
func TestVistaDeCuadricula(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.addCheck(model.Check{Name: "Web", Type: "tcp", Interval: time.Minute,
		Params: map[string]string{"host": "127.0.0.1", "port": "1", "timeout": "1s"}}, state.Observation{OK: true})

	r := e.get("/home")
	if !strings.Contains(r.body, `data-view-switch hidden`) || strings.Contains(r.body, "view-grid") ||
		!strings.Contains(r.body, `data-view="list" title="Vista de lista" aria-label="Vista de lista" aria-pressed="true"`) {
		t.Fatalf("por defecto debe pintarse la lista:\n%s", r.body)
	}
	if !strings.Contains(r.body, `class="monitor is-up"`) {
		t.Error("cada fila debe llevar la clase de su estado")
	}
	for _, c := range []struct {
		val  string
		grid bool
	}{{"grid", true}, {"list", false}, {"otra", false}} {
		r := e.do(http.MethodGet, "/home", nil, map[string]string{"Cookie": viewCookie + "=" + c.val})
		if got := strings.Contains(r.body, "checks-view view-grid"); got != c.grid {
			t.Errorf("cookie %q: cuadrícula = %v", c.val, got)
		}
	}
}

// TestOpcionesPaginaPublica: checks ocultos y ajustes de qué se muestra sin
// sesión. Todo se filtra en el servidor (también en los JSON).
func TestOpcionesPaginaPublica(t *testing.T) {
	e := newEnv(t)
	e.setup()
	ctx := context.Background()

	// Un check oculto creado desde el formulario (casilla desmarcada).
	form := url.Values{"csrf": {e.csrfFrom("/admin")}, "type": {"tcp"}, "name": {"Secreto"}, "group": {"Interno"},
		"interval": {"60s"}, "fail_threshold": {"1"}, "public": {"false"}, "p_host": {"127.0.0.1"}, "p_port": {"22"}}
	expect(t, e.post("/admin/checks", form), http.StatusSeeOther, "/admin?m=created")
	cs, _ := e.store.ListChecks(ctx)
	if len(cs) != 1 || !cs[0].Hidden {
		t.Fatalf("el check debe guardarse oculto: %+v", cs)
	}
	secret := cs[0]
	e.tracker.SetCheck(secret)
	e.tracker.Record(state.Observation{CheckID: secret.ID, At: time.Now(), OK: false, Message: msg.New("check.msg.refused")})
	if r := e.get("/admin/checks/" + itoa(secret.ID)); !strings.Contains(r.body, `name="public" value="true">`) {
		t.Error("al editar, la casilla de visibilidad debe salir desmarcada")
	}
	if r := e.get("/admin"); !strings.Contains(r.body, "no público") {
		t.Error("la lista de checks debe marcar los ocultos")
	}

	web := e.addCheck(model.Check{Name: "Web", Type: "tcp", Group: "Servicios", Interval: time.Minute, FailThreshold: 1,
		Params: map[string]string{"host": "127.0.0.1", "port": "1", "timeout": "1s"}},
		state.Observation{OK: true, Latency: 42 * time.Millisecond})
	e.tracker.Record(state.Observation{CheckID: web.ID, At: time.Now(), OK: false, Message: msg.New("check.msg.refused")})
	e.tracker.Record(state.Observation{CheckID: web.ID, At: time.Now(), OK: true, Latency: 42 * time.Millisecond})

	e.post("/logout", url.Values{"csrf": {e.csrfFrom("/admin")}})
	public := func(d settings.Data) {
		d.Public, d.RawHours, d.AggregateDays, d.SessionTTL = true, 48, 90, time.Hour
		if err := e.set.SetData(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	// Por defecto: todo menos el check oculto.
	public(settings.Data{})
	home, js := e.get("/home"), e.get("/home/state.json")
	for _, r := range []resp{home, js} {
		if strings.Contains(r.body, "Secreto") || strings.Contains(r.body, "Interno") {
			t.Errorf("el check oculto no debe aparecer:\n%s", r.body)
		}
	}
	for _, want := range []string{"Servicios", "42 ms", `href="/check/` + itoa(web.ID), "1/1"} {
		if !strings.Contains(home.body, want) {
			t.Errorf("/home debe mostrar %q", want)
		}
	}
	if !strings.Contains(js.body, `"incidents":[{`) || !strings.Contains(js.body, `"links":true`) {
		t.Errorf("deben verse las incidencias del check visible: %s", js.body)
	}
	expect(t, e.get("/check/"+itoa(secret.ID)), http.StatusSeeOther, "/login?next=%2Fcheck%2F"+itoa(secret.ID))
	expect(t, e.get("/check/"+itoa(secret.ID)+"/data.json"), http.StatusNotFound, "")
	if r := e.get("/check/" + itoa(web.ID)); r.status != 200 || !strings.Contains(r.body, "data-chart") {
		t.Errorf("el detalle debe verse con su gráfica: %d", r.status)
	}

	// Todo desactivado.
	public(settings.Data{HideGroups: true, HideLatency: true, HideIncidents: true, HideDetail: true})
	home, js = e.get("/home"), e.get("/home/state.json")
	for _, r := range []resp{home, js} {
		for _, leak := range []string{"Servicios", "42 ms", `href="/check/`, "group-name", "Sin grupo"} {
			if strings.Contains(r.body, leak) {
				t.Errorf("con todo oculto no debe aparecer %q", leak)
			}
		}
	}
	if strings.Contains(home.body, "data-incidents") || !strings.Contains(js.body, `"incidents":null`) ||
		!strings.Contains(js.body, `"links":false`) || !strings.Contains(js.body, `"latency":""`) {
		t.Errorf("incidencias, enlaces y tiempos deben retirarse: %s", js.body)
	}
	expect(t, e.get("/check/"+itoa(web.ID)), http.StatusSeeOther, "/login?next=%2Fcheck%2F"+itoa(web.ID))
	expect(t, e.get("/check/"+itoa(web.ID)+"/data.json"), http.StatusNotFound, "")

	// Solo el detalle permitido: sin gráfica, tiempos ni incidencias.
	public(settings.Data{HideLatency: true, HideIncidents: true})
	d := e.get("/check/" + itoa(web.ID) + "/data.json")
	if d.status != 200 || strings.Contains(d.body, "42 ms") || !strings.Contains(d.body, `"points":null`) ||
		!strings.Contains(d.body, `"incidents":null`) || !strings.Contains(d.body, `"avg":""`) {
		t.Errorf("detalle sin tiempos ni incidencias: %s", d.body)
	}
	if r := e.get("/check/" + itoa(web.ID)); strings.Contains(r.body, "data-chart") || strings.Contains(r.body, `data-field="avg"`) {
		t.Error("la página de detalle no debe incluir la gráfica ni los tiempos")
	}

	// Con sesión se ve todo, incluido el check oculto.
	e.post("/login", url.Values{"csrf": {e.csrfFrom("/login")}, "password": {testPassword}})
	if r := e.get("/home"); !strings.Contains(r.body, "Secreto") || !strings.Contains(r.body, "42 ms") || !strings.Contains(r.body, "Servicios") {
		t.Error("con sesión debe verse todo")
	}
	expect(t, e.get("/check/"+itoa(secret.ID)), http.StatusOK, "")
}

// TestLogoYFavicon: el punto del logotipo y el favicon muestran el estado
// global, sin darlo a quien no puede ver la página de estado; con caídas, el
// anillo del favicon también se pone en rojo.
func TestLogoYFavicon(t *testing.T) {
	for level, want := range map[model.Status][2]string{
		"":                   {brandBlue, brandBlue},
		model.StatusUp:       {brandBlue, brandUp},
		model.StatusDegraded: {brandBlue, brandSlow},
		model.StatusDown:     {brandDown, brandDown},
		model.StatusUnknown:  {brandBlue, brandOther},
	} {
		svg := faviconSVG(level)
		if !strings.Contains(svg, `stroke="`+want[0]+`"`) || !strings.Contains(svg, `<circle cx="12.34" cy="12.34" r="2.5" fill="`+want[1]+`"`) {
			t.Errorf("favicon %q: %s", level, svg)
		}
	}
	if u := string(faviconURL(model.StatusDown)); !strings.HasPrefix(u, "data:image/svg+xml,") || strings.ContainsAny(u, "<>\" ") {
		t.Errorf("favicon mal codificado: %s", u)
	}

	e := newEnv(t)
	e.setup()
	ctx := context.Background()
	e.addCheck(model.Check{Name: "Web", Type: "tcp", Interval: time.Minute, FailThreshold: 1,
		Params: map[string]string{"host": "127.0.0.1", "port": "1", "timeout": "1s"}}, state.Observation{OK: true})
	e.addCheck(model.Check{Name: "Oculto", Type: "tcp", Interval: time.Minute, FailThreshold: 1, Hidden: true,
		Params: map[string]string{"host": "127.0.0.1", "port": "2", "timeout": "1s"}}, state.Observation{OK: false})

	// Con sesión: estado de todos (hay uno caído), en todas las páginas.
	for _, path := range []string{"/home", "/admin", "/admin/settings"} {
		r := e.get(path)
		if !strings.Contains(r.body, `data-logo data-level="down"`) {
			t.Errorf("%s: el logotipo debe mostrar el estado global", path)
		}
		if !strings.Contains(r.body, "%23d64545") || strings.Contains(r.body, "%232f7fd6") {
			t.Errorf("%s: el favicon debe ir en rojo (anillo y punto) con caídas", path)
		}
	}
	if r := e.get("/check/1/data.json"); !strings.Contains(r.body, `"global":"down"`) {
		t.Errorf("el JSON del detalle debe llevar el estado global: %s", r.body)
	}

	// Sin sesión ni página pública: sin estado.
	e.post("/logout", url.Values{"csrf": {e.csrfFrom("/admin")}})
	r := e.get("/login")
	if !strings.Contains(r.body, `data-logo data-level=""`) || strings.Contains(r.body, "%23d64545") || strings.Contains(r.body, "%232e9e5b") {
		t.Error("el login no debe revelar el estado si la página no es pública")
	}
	if !strings.Contains(r.body, `class="logo mark"`) {
		t.Error("el login debe mostrar el símbolo")
	}

	// Página pública: el estado de los checks visibles (el oculto no cuenta).
	e.set.SetData(ctx, settings.Data{Public: true, RawHours: 48, AggregateDays: 90, SessionTTL: time.Hour})
	if r := e.get("/home"); !strings.Contains(r.body, `data-logo data-level="up"`) || strings.Contains(r.body, "%23d64545") {
		t.Error("en la página pública, el estado global no debe contar los checks ocultos")
	}
}
