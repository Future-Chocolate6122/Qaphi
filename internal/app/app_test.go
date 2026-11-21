package app

import (
	"context"
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

	"github.com/future-chocolate6122/qaphi/internal/config"
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
	if fi, _ := os.Stat(dir); fi.Mode().Perm()&0o077 != 0 && !strings.Contains(logs.String(), "accesible") {
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
	cfg := &config.Config{Listen: ln.Addr().String(), DataDir: t.TempDir()}
	a, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "escuchar") {
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
		if body := get("/home"); strings.Contains(body, "3/3 (todos up)") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/home no llegó a 3/3:\n%s", get("/home"))
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 4. Reinicio: la parada vuelca el buffer y todo persiste.
	r.stop(t)
	if !strings.Contains(logs.String(), "buffer volcado") {
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
	if !strings.Contains(body, "3/3 (todos up)") {
		t.Errorf("tras reiniciar no se restauró el estado:\n%s", body)
	}
	if strings.Count(logs.String(), "configuración inicial pendiente") != 1 {
		t.Error("tras configurar la contraseña no debe volver a pedirse el token")
	}
}
