package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/auth"
	"github.com/future-chocolate6122/qaphi/internal/config"
	"github.com/future-chocolate6122/qaphi/internal/netcfg"
	"github.com/future-chocolate6122/qaphi/internal/settings"
	"github.com/future-chocolate6122/qaphi/internal/store"
	"github.com/future-chocolate6122/qaphi/internal/store/sqlite"
)

// TestCicloDeVida comprueba el criterio de la fase 0: arranca, crea
// qaphi.db, responde en / y se detiene limpiamente.
func TestCicloDeVida(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "datos") // aún no existe: debe crearse
	cfg := &config.Config{Listen: "127.0.0.1:0", DataDir: dir, FlushInterval: 5 * time.Minute}
	var logs strings.Builder
	log := slog.New(slog.NewTextHandler(&logs, nil))

	a, err := New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, DBFileName)); err != nil {
		t.Fatalf("no se creó la base de datos: %v", err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm()&0o077 != 0 && !strings.Contains(logs.String(), "accessible") {
		t.Errorf("permisos del directorio de datos = %o", fi.Mode().Perm())
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, ln) }()

	res, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "Qaphi") {
		t.Fatalf("GET /: estado %d, cuerpo %q", res.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve devolvió error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve no terminó tras cancelar el contexto")
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond); err == nil {
		t.Error("el puerto sigue aceptando conexiones tras la parada")
	}

	// Un segundo arranque sobre los mismos datos funciona.
	a2, err := New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("segundo arranque: %v", err)
	}
	a2.Close()
}

func TestDataDirEsUnFichero(t *testing.T) {
	f := filepath.Join(t.TempDir(), "fichero")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Listen: "127.0.0.1:0", DataDir: f}
	if _, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("se esperaba error si el directorio de datos es un fichero")
	}
}

func TestPuertoOcupado(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg := &config.Config{Listen: ln.Addr().String(), DataDir: t.TempDir(), FlushInterval: time.Minute}
	a, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("se esperaba error al escuchar en un puerto ocupado: %v", err)
	}
}

// syncBuffer es un strings.Builder seguro para escribir desde varias
// goroutines (el logger) y leer desde el test.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

var (
	tokenRe = regexp.MustCompile(`token=([A-Z2-7]{4}(?:-[A-Z2-7]{4}){3})`)
	csrfRe  = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
)

type running struct {
	app    *App
	base   string
	cancel context.CancelFunc
	done   chan error
}

func start(t *testing.T, cfg *config.Config, logs io.Writer) *running {
	t.Helper()
	a, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{app: a, base: "http://" + ln.Addr().String(), cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- a.Serve(ctx, ln) }()
	return r
}

func (r *running) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	if err := <-r.done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if err := r.app.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestCriterioFase1: desde cero, configurar la contraseña, crear 3 checks y
// verlos actualizarse en /home; tras reiniciar, los datos persisten.
func TestCriterioFase1(t *testing.T) {
	// Tres servicios TCP locales que aceptan conexiones.
	var ports []string
	for i := 0; i < 3; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
		ports = append(ports, strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	}

	cfg := &config.Config{Listen: "127.0.0.1:0", DataDir: t.TempDir(), FlushInterval: time.Minute}
	logs := &syncBuffer{}
	r := start(t, cfg, logs)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	get := func(path string) string {
		t.Helper()
		res, err := client.Get(r.base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	post := func(path string, form url.Values) int {
		t.Helper()
		res, err := client.PostForm(r.base+path, form)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	csrf := func(path string) string {
		t.Helper()
		m := csrfRe.FindStringSubmatch(get(path))
		if m == nil {
			t.Fatalf("sin token CSRF en %s", path)
		}
		return m[1]
	}

	// 1. El token de configuración aparece en el log.
	var m []string
	for wait := time.Now().Add(5 * time.Second); m == nil && time.Now().Before(wait); time.Sleep(20 * time.Millisecond) {
		m = tokenRe.FindStringSubmatch(logs.String())
	}
	if m == nil {
		t.Fatalf("el log no contiene el token de configuración:\n%s", logs.String())
	}
	pw := "contraseña de la fase uno"
	if code := post("/setup", url.Values{"csrf": {csrf("/setup")}, "token": {m[1]}, "password": {pw}, "confirm": {pw}}); code != http.StatusOK {
		t.Fatalf("setup: estado %d", code)
	}

	// 2. Tres checks.
	tok := csrf("/admin")
	for i, p := range ports {
		form := url.Values{"csrf": {tok}, "type": {"tcp"}, "name": {"Servicio " + strconv.Itoa(i+1)}, "group": {"Pruebas"},
			"interval": {"10s"}, "p_host": {"127.0.0.1"}, "p_port": {p}, "p_timeout": {"2s"}}
		if code := post("/admin/checks", form); code != http.StatusOK {
			t.Fatalf("crear check %d: estado %d", i+1, code)
		}
	}

	// 3. /home se actualiza en unos segundos (los checks nuevos se ejecutan enseguida).
	deadline := time.Now().Add(15 * time.Second)
	for {
		if body := get("/home"); strings.Contains(body, "3/3 (all up)") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/home no llegó a 3/3:\n%s", get("/home"))
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 4. Reinicio: la parada vuelca el buffer y todo persiste.
	r.stop(t)
	if !strings.Contains(logs.String(), "buffer flushed") {
		t.Error("la parada no volcó el buffer")
	}
	r = start(t, cfg, logs)
	defer r.stop(t)
	jar2, _ := cookiejar.New(nil)
	client.Jar = jar2
	if code := post("/login", url.Values{"csrf": {csrf("/login")}, "password": {pw}}); code != http.StatusOK {
		t.Fatalf("login tras reiniciar: estado %d", code)
	}
	body := get("/home")
	for i := 1; i <= 3; i++ {
		if !strings.Contains(body, "Servicio "+strconv.Itoa(i)) {
			t.Errorf("tras reiniciar falta el check %d", i)
		}
	}
	// El último resultado se restaura: no vuelven a «sin datos».
	if !strings.Contains(body, "3/3 (all up)") {
		t.Errorf("tras reiniciar no se restauró el estado:\n%s", body)
	}
	if strings.Count(logs.String(), "initial setup pending") != 1 {
		t.Error("tras configurar la contraseña no debe volver a pedirse el token")
	}
}

// langTransport añade Accept-Language a todas las peticiones y acepta el
// certificado autofirmado.
type langTransport struct{ base http.RoundTripper }

func (t langTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Accept-Language", "es-ES,es;q=0.9")
	return t.base.RoundTrip(r)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestCriterioFase2: cambiar idioma y tema y verlos en todas las páginas;
// pasar a HTTPS en otro puerto y confirmar; repetir sin confirmar y comprobar
// que vuelve sola a la configuración anterior.
func TestCriterioFase2(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	portA, portB := freePort(t), freePort(t)
	// Configuración de red inicial: HTTP en 127.0.0.1:portA.
	st, err := sqlite.Open(ctx, filepath.Join(dir, DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := netcfg.Save(ctx, st, netcfg.Config{Bind: "127.0.0.1", Port: portA}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	logs := &syncBuffer{}
	cfg := &config.Config{DataDir: dir, FlushInterval: time.Minute}
	a, err := New(ctx, cfg, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	a.NetWindow, a.NetSwapDelay = 2*time.Second, 50*time.Millisecond
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Serve(runCtx, nil) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
		a.Close()
	}()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar, Timeout: 5 * time.Second,
		Transport: langTransport{&http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}},
	}
	base := func(scheme string, port int) string { return fmt.Sprintf("%s://127.0.0.1:%d", scheme, port) }
	get := func(u string) (int, string) {
		t.Helper()
		res, err := client.Get(u)
		if err != nil {
			return 0, err.Error()
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	post := func(u string, form url.Values) (int, string) {
		t.Helper()
		res, err := client.PostForm(u, form)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	csrf := func(u string) string {
		t.Helper()
		_, body := get(u)
		m := csrfRe.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("sin token CSRF en %s", u)
		}
		return m[1]
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if cond() {
				return
			}
		}
		t.Fatalf("no se cumplió: %s", what)
	}
	httpA := base("http", portA)

	// Setup (en castellano, por el idioma del navegador).
	var token []string
	waitFor("token en el log", func() bool { token = tokenRe.FindStringSubmatch(logs.String()); return token != nil })
	if !strings.Contains(logs.String(), fmt.Sprintf("listen=127.0.0.1:%d scheme=http", portA)) {
		t.Fatalf("el log de arranque debe indicar dirección y protocolo:\n%s", logs.String())
	}
	pw := "contraseña de la fase dos"
	if code, body := post(httpA+"/setup", url.Values{"csrf": {csrf(httpA + "/setup")}, "token": {token[1]}, "password": {pw}, "confirm": {pw}}); code != 200 || !strings.Contains(body, "Contraseña guardada") {
		t.Fatalf("setup: %d", code)
	}

	// 1. Idioma y tema: alemán y oscuro, en todas las páginas.
	tok := csrf(httpA + "/admin/settings")
	if code, _ := post(httpA+"/admin/settings", url.Values{"csrf": {tok}, "language": {"de"}, "theme": {"dark"}, "timezone": {"Europe/Berlin"}}); code != 200 {
		t.Fatalf("guardar ajustes: %d", code)
	}
	for path, want := range map[string]string{"/home": "Dienststatus", "/admin": "Neuer Check", "/admin/settings": "Einstellungen", "/admin/checks/new?type=http": "Erwartete Statuscodes"} {
		_, body := get(httpA + path)
		if !strings.Contains(body, want) || !strings.Contains(body, `lang="de"`) || !strings.Contains(body, "/theme/dark.css") {
			t.Errorf("%s no está en alemán con el tema oscuro", path)
		}
	}

	// 2. HTTPS en otro puerto, confirmado desde la dirección nueva.
	httpsB := base("https", portB)
	code, body := post(httpA+"/admin/settings/network", url.Values{"csrf": {tok}, "bind": {"127.0.0.1"}, "port": {strconv.Itoa(portB)}, "protocol": {"https"}})
	if code != 200 || !strings.Contains(body, httpsB+"/admin/settings/network/confirm") {
		t.Fatalf("aplicar red: %d %.400s", code, body)
	}
	if code, _ := get(httpA + "/home"); code != 200 {
		t.Fatal("durante el cambio, la dirección anterior debe seguir respondiendo")
	}
	if code, _ := get(httpsB + "/admin/settings/network/confirm"); code != 200 {
		t.Fatalf("página de confirmación por HTTPS: %d", code)
	}
	if code, body := post(httpsB+"/admin/settings/network/confirm", url.Values{"csrf": {tok}}); code != 200 || !strings.Contains(body, "Netzwerkeinstellungen bestätigt") {
		t.Fatalf("confirmar: %d %.300s", code, body)
	}
	waitFor("la dirección anterior deja de responder", func() bool { c, _ := get(httpA + "/home"); return c == 0 })

	// 3. Sin confirmar: volver a HTTP en el mismo puerto (cambio en el sitio).
	code, body = post(httpsB+"/admin/settings/network", url.Values{"csrf": {tok}, "bind": {"127.0.0.1"}, "port": {strconv.Itoa(portB)}, "protocol": {"http"}})
	if code != 200 || !strings.Contains(body, "Port gleich") {
		t.Fatalf("cambio en el sitio: %d %.400s", code, body)
	}
	httpB := base("http", portB)
	waitFor("pasa a HTTP", func() bool { c, _ := get(httpB + "/home"); return c == 200 })
	waitFor("vuelve sola a HTTPS al vencer el plazo", func() bool { c, _ := get(httpsB + "/home"); return c == 200 })
	if _, body := get(httpsB + "/admin/settings"); !strings.Contains(body, "nicht rechtzeitig bestätigt") {
		t.Error("los ajustes deben avisar de que el cambio se deshizo")
	}
	saved, err := netcfg.Load(ctx, a.store, false)
	if err != nil || saved != (netcfg.Config{Bind: "127.0.0.1", Port: portB, TLS: true}) {
		t.Fatalf("configuración guardada = %+v, %v", saved, err)
	}
}

// TestCriterioFase3: provocar la caída de un servicio y que se registre una
// incidencia con su duración; el modo público no muestra ningún destino.
func TestCriterioFase3(t *testing.T) {
	ctx := context.Background()
	// Servicio TCP que se puede tirar y levantar en el mismo puerto.
	svc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	svcAddr := svc.Addr().String()
	serve := func(ln net.Listener) {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}
	go serve(svc)

	cfg := &config.Config{Listen: "127.0.0.1:0", DataDir: t.TempDir(), FlushInterval: time.Minute}
	logs := &syncBuffer{}
	r := start(t, cfg, logs)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	get := func(path string) string {
		t.Helper()
		res, err := client.Get(r.base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	post := func(path string, form url.Values) int {
		t.Helper()
		res, err := client.PostForm(r.base+path, form)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	csrf := func(path string) string {
		t.Helper()
		m := csrfRe.FindStringSubmatch(get(path))
		if m == nil {
			t.Fatalf("sin token CSRF en %s", path)
		}
		return m[1]
	}
	waitFor := func(what string, d time.Duration, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if cond() {
				return
			}
		}
		t.Fatalf("no se cumplió: %s", what)
	}

	var token []string
	waitFor("token", 5*time.Second, func() bool { token = tokenRe.FindStringSubmatch(logs.String()); return token != nil })
	pw := "contraseña de la fase tres"
	post("/setup", url.Values{"csrf": {csrf("/setup")}, "token": {token[1]}, "password": {pw}, "confirm": {pw}})

	host, port, _ := net.SplitHostPort(svcAddr)
	tok := csrf("/admin")
	if code := post("/admin/checks", url.Values{"csrf": {tok}, "type": {"tcp"}, "name": {"Servicio crítico"}, "interval": {"10s"},
		"fail_threshold": {"1"}, "p_host": {host}, "p_port": {port}, "p_timeout": {"1s"}}); code != 200 {
		t.Fatalf("crear check: %d", code)
	}
	cs, _ := r.app.store.ListChecks(ctx)
	id := cs[0].ID
	status := func() string { st, _ := r.app.tracker.Get(id); return string(st.Status) }
	waitFor("operativo", 10*time.Second, func() bool { return status() == "up" })

	// Caída: se cierra el servicio y se fuerza la comprobación.
	svc.Close()
	r.app.sched.Trigger(id)
	waitFor("caído", 10*time.Second, func() bool { return status() == "down" })
	time.Sleep(1100 * time.Millisecond) // que la incidencia dure algo
	// Recuperación en el mismo puerto.
	svc2, err := net.Listen("tcp", svcAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer svc2.Close()
	go serve(svc2)
	r.app.sched.Trigger(id)
	waitFor("recuperado", 10*time.Second, func() bool { return status() == "up" })

	incs, err := r.app.store.ListIncidents(ctx, id, 10)
	if err != nil || len(incs) != 1 || incs[0].Open() || incs[0].Duration(time.Now()) < time.Second {
		t.Fatalf("incidencia = %+v, %v", incs, err)
	}
	if !strings.Contains(logs.String(), `msg="check down" check="Servicio crítico"`) || !strings.Contains(logs.String(), `msg="check recovered"`) {
		t.Errorf("faltan la caída o la recuperación en el log:\n%s", logs.String())
	}
	body := get("/check/" + strconv.FormatInt(id, 10) + "/data.json?range=1h")
	if !strings.Contains(body, `"open":false`) || !strings.Contains(body, `"duration":"1 s"`) {
		t.Errorf("la incidencia no aparece con su duración: %s", body)
	}

	// Reinicio: la incidencia cerrada, los pulsos y el uptime se conservan.
	r.stop(t)
	r = start(t, cfg, logs)
	defer r.stop(t)
	if st, _ := r.app.tracker.Get(id); len(st.Recent) < 3 || st.Status != "up" {
		t.Fatalf("estado tras reiniciar = %+v", st)
	}
	if up := r.app.tracker.Uptime(id)[0]; up <= 0 || up >= 1 {
		t.Errorf("uptime tras reiniciar = %v", up)
	}

	// Modo público: sin sesión, ningún destino ni error.
	client.Jar, _ = cookiejar.New(nil)
	if err := r.app.settings.SetData(ctx, settings.Data{Public: true, RawHours: 48, AggregateDays: 90, SessionTTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/home", "/home/state.json", "/check/" + strconv.FormatInt(id, 10), "/check/" + strconv.FormatInt(id, 10) + "/data.json?range=1h"} {
		body := get(path)
		if !strings.Contains(body, "Servicio crítico") {
			t.Errorf("%s: la página pública debe mostrar el servicio", path)
		}
		for _, leak := range []string{svcAddr, port, "refused", "rechazada"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s muestra %q en modo público", path, leak)
			}
		}
	}
}

// TestPuertoTrasReiniciar: una instalación nueva conserva el puerto 9077 tras
// la configuración inicial y los reinicios (antes pasaba al 8091 de la v0.1.0
// si nunca se había cambiado la red); una base de datos de la v0.1.0
// (contraseña, sin red ni idioma) conserva el 8091.
func TestPuertoTrasReiniciar(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	port := func(dir string) int {
		t.Helper()
		a, err := New(ctx, &config.Config{DataDir: dir, FlushInterval: time.Minute}, quiet)
		if err != nil {
			t.Fatal(err)
		}
		p := a.netCfg.Port
		a.Close()
		return p
	}
	withStore := func(dir string, fn func(store.Store)) {
		t.Helper()
		st, err := sqlite.Open(ctx, filepath.Join(dir, DBFileName))
		if err != nil {
			t.Fatal(err)
		}
		fn(st)
		st.Close()
	}

	// Instalación nueva: primer arranque, /setup (contraseña e idioma) y
	// reinicios.
	dir := t.TempDir()
	if p := port(dir); p != netcfg.DefaultPort {
		t.Fatalf("primer arranque: puerto %d", p)
	}
	withStore(dir, func(st store.Store) {
		if err := auth.SetPassword(ctx, st, "una contraseña larga"); err != nil {
			t.Fatal(err)
		}
		st.SetSetting(ctx, settings.KeyLanguage, "es")
	})
	for i := 0; i < 2; i++ {
		if p := port(dir); p != netcfg.DefaultPort {
			t.Fatalf("reinicio %d tras /setup: puerto %d, se esperaba %d", i+1, p, netcfg.DefaultPort)
		}
	}

	// Instalación nueva creada por una versión que no guardaba la red al
	// arrancar: contraseña e idioma, sin red. También 9077.
	dir = t.TempDir()
	withStore(dir, func(st store.Store) {
		auth.SetPassword(ctx, st, "una contraseña larga")
		st.SetSetting(ctx, settings.KeyLanguage, "en")
	})
	if p := port(dir); p != netcfg.DefaultPort {
		t.Fatalf("instalación v0.2+ sin red guardada: puerto %d", p)
	}

	// v0.1.0: contraseña, sin red ni idioma. Conserva el 8091 (también tras
	// reiniciar, porque queda guardado).
	dir = t.TempDir()
	withStore(dir, func(st store.Store) { auth.SetPassword(ctx, st, "una contraseña larga") })
	for i := 0; i < 2; i++ {
		if p := port(dir); p != netcfg.LegacyPort {
			t.Fatalf("v0.1.0, arranque %d: puerto %d, se esperaba %d", i+1, p, netcfg.LegacyPort)
		}
	}
}
