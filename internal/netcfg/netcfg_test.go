package netcfg

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/store/sqlite"
)

// freePort devuelve un puerto libre (≥ 1024).
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

type env struct {
	t   *testing.T
	m   *Manager
	st  *sqlite.Store
	ctx context.Context
}

func newEnv(t *testing.T, window time.Duration) *env {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	certs, err := LoadCertStore(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, ctx: ctx}
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok %d", EndpointID(r.Context()))
	})
	mux.HandleFunc("/confirm", func(w http.ResponseWriter, r *http.Request) {
		if err := e.m.Confirm(r); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		io.WriteString(w, "confirmado")
	})
	e.m = NewManager(Options{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Handler: mux, Store: st, Certs: certs,
		Window: window, SwapDelay: 20 * time.Millisecond,
	})
	t.Cleanup(e.m.Shutdown)
	return e
}

var client = &http.Client{
	Timeout:   2 * time.Second,
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true},
}

// get hace una petición; devuelve el cuerpo o el error.
func get(scheme string, port int, path string) (int, string, error) {
	res, err := client.Get(fmt.Sprintf("%s://127.0.0.1:%d%s", scheme, port, path))
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), nil
}

func (e *env) eventually(what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("no se cumplió: %s", what)
}

func up(scheme string, port int) bool {
	code, body, err := get(scheme, port, "/ping")
	return err == nil && code == 200 && strings.HasPrefix(body, "ok")
}

func local(port int, tls bool) Config { return Config{Bind: BindLocal, Port: port, TLS: tls} }

func TestCambioDePuertoConfirmado(t *testing.T) {
	e := newEnv(t, time.Minute)
	a, b := freePort(t), freePort(t)
	if err := e.m.Start(e.ctx, local(a, false), nil); err != nil {
		t.Fatal(err)
	}
	if !up("http", a) {
		t.Fatal("no responde en el puerto inicial")
	}
	inPlace, err := e.m.Apply(e.ctx, local(b, false))
	if err != nil || inPlace {
		t.Fatalf("Apply = %v, %v", inPlace, err)
	}
	// Superposición: las dos responden.
	if !up("http", a) || !up("http", b) {
		t.Fatal("durante el cambio deben responder las dos direcciones")
	}
	if cfg, _, _, ok := e.m.Pending(); !ok || cfg.Port != b {
		t.Fatal("falta el cambio pendiente")
	}
	if e.m.Current().Port != a {
		t.Error("mientras no se confirma, la actual sigue siendo la anterior")
	}
	// Confirmar desde la dirección anterior no vale.
	if code, body, _ := get("http", a, "/confirm"); code != http.StatusConflict || !strings.Contains(body, "new address") {
		t.Fatalf("confirmar desde la anterior: %d %s", code, body)
	}
	if code, _, _ := get("http", b, "/confirm"); code != 200 {
		t.Fatalf("confirmar desde la nueva: %d", code)
	}
	e.eventually("la anterior deja de responder", func() bool { return !up("http", a) })
	if !up("http", b) || e.m.Current().Port != b {
		t.Fatal("la nueva debe seguir respondiendo")
	}
	saved, err := Load(e.ctx, e.st, false)
	if err != nil || saved != local(b, false) {
		t.Fatalf("configuración guardada = %+v, %v", saved, err)
	}
}

func TestCambioDePuertoSinConfirmar(t *testing.T) {
	e := newEnv(t, 300*time.Millisecond)
	a, b := freePort(t), freePort(t)
	if err := e.m.Start(e.ctx, local(a, false), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Apply(e.ctx, local(b, false)); err != nil {
		t.Fatal(err)
	}
	if !up("http", b) {
		t.Fatal("la nueva debe responder durante el plazo")
	}
	e.eventually("la nueva se cierra al vencer el plazo", func() bool { return !up("http", b) })
	if !up("http", a) {
		t.Fatal("la anterior debe seguir respondiendo")
	}
	if !e.m.TakeReverted() || e.m.TakeReverted() {
		t.Error("TakeReverted debe avisar una sola vez")
	}
	if saved, _ := Load(e.ctx, e.st, false); saved.Port != DefaultPort {
		t.Errorf("no debe guardarse nada sin confirmar: %+v", saved)
	}
}

func TestCambioAHTTPSEnElSitio(t *testing.T) {
	e := newEnv(t, time.Minute)
	a := freePort(t)
	if err := e.m.Start(e.ctx, local(a, false), nil); err != nil {
		t.Fatal(err)
	}
	inPlace, err := e.m.Apply(e.ctx, local(a, true))
	if err != nil || !inPlace {
		t.Fatalf("Apply = %v, %v (se esperaba cambio en el sitio)", inPlace, err)
	}
	e.eventually("responde por HTTPS en el mismo puerto", func() bool { return up("https", a) })
	if code, _, err := get("http", a, "/ping"); err == nil && code == 200 {
		t.Error("tras el cambio no debe responder por HTTP normal")
	}
	if info, ok := e.m.o.Certs.Info(); !ok || info.Fingerprint == "" || !info.NotAfter.After(time.Now().Add(4*365*24*time.Hour)) {
		t.Errorf("certificado = %+v, %v", info, ok)
	}
	if code, _, _ := get("https", a, "/confirm"); code != 200 {
		t.Fatalf("confirmar: %d", code)
	}
	if saved, _ := Load(e.ctx, e.st, false); !saved.TLS || saved.Port != a {
		t.Fatalf("guardado = %+v", saved)
	}
}

func TestCambioEnElSitioSinConfirmar(t *testing.T) {
	e := newEnv(t, 400*time.Millisecond)
	a := freePort(t)
	if err := e.m.Start(e.ctx, local(a, false), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Apply(e.ctx, local(a, true)); err != nil {
		t.Fatal(err)
	}
	e.eventually("pasa a HTTPS", func() bool { return up("https", a) })
	e.eventually("vuelve a HTTP al vencer el plazo", func() bool { return up("http", a) })
	if e.m.Current().TLS {
		t.Error("la configuración actual debe volver a ser HTTP")
	}
}

func TestCancelar(t *testing.T) {
	e := newEnv(t, time.Minute)
	a, b := freePort(t), freePort(t)
	if err := e.m.Start(e.ctx, local(a, false), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Apply(e.ctx, local(b, false)); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Cancel(); err != nil {
		t.Fatal(err)
	}
	e.eventually("la nueva se cierra", func() bool { return !up("http", b) })
	if !up("http", a) || e.m.TakeReverted() {
		t.Error("cancelar no es lo mismo que vencer el plazo")
	}
	if err := e.m.Cancel(); !errors.Is(err, ErrNoPending) {
		t.Errorf("Cancel sin pendiente = %v", err)
	}
}

func TestErroresDeApply(t *testing.T) {
	e := newEnv(t, time.Minute)
	a := freePort(t)
	if err := e.m.Start(e.ctx, local(a, false), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Apply(e.ctx, local(a, false)); !errors.Is(err, ErrUnchanged) {
		t.Errorf("sin cambios: %v", err)
	}
	// Puerto ocupado por otro proceso.
	other, _ := net.Listen("tcp", "127.0.0.1:0")
	defer other.Close()
	busy := other.Addr().(*net.TCPAddr).Port
	_, err := e.m.Apply(e.ctx, local(busy, false))
	var nerr *Error
	if !errors.As(err, &nerr) || nerr.Msg.Key != "settings.net.err.inuse" {
		t.Fatalf("puerto ocupado: %v", err)
	}
	if !strings.Contains(nerr.Msg.In(msg.ES), "ya está en uso") {
		t.Errorf("mensaje = %q", nerr.Msg.In(msg.ES))
	}
	if _, _, _, ok := e.m.Pending(); ok {
		t.Error("un error no debe dejar un cambio pendiente")
	}
	// Con un cambio pendiente no se admite otro.
	if _, err := e.m.Apply(e.ctx, local(freePort(t), false)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Apply(e.ctx, local(freePort(t), false)); !errors.Is(err, ErrPending) {
		t.Errorf("dos cambios: %v", err)
	}
}

func TestDireccionFijadaAlArrancar(t *testing.T) {
	e := newEnv(t, time.Minute)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.m.Start(e.ctx, Config{Bind: BindLocal, Port: ln.Addr().(*net.TCPAddr).Port}, ln); err != nil {
		t.Fatal(err)
	}
	if !e.m.Locked() {
		t.Fatal("con listener dado debe quedar bloqueada")
	}
	if _, err := e.m.Apply(e.ctx, local(freePort(t), false)); !errors.Is(err, ErrLocked) {
		t.Errorf("Apply bloqueado = %v", err)
	}
}

func TestRegenerarCertificado(t *testing.T) {
	e := newEnv(t, time.Minute)
	a := freePort(t)
	if err := e.m.Start(e.ctx, local(a, true), nil); err != nil {
		t.Fatal(err)
	}
	fp := func() string {
		conn, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", a), &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		return string(conn.ConnectionState().PeerCertificates[0].Signature)
	}
	before := fp()
	info1, _ := e.m.o.Certs.Info()
	if err := e.m.o.Certs.Regenerate(e.ctx); err != nil {
		t.Fatal(err)
	}
	info2, _ := e.m.o.Certs.Info()
	if info1.Fingerprint == info2.Fingerprint || fp() == before {
		t.Fatal("el certificado nuevo debe usarse sin reiniciar")
	}
	// Persiste.
	cs, err := LoadCertStore(e.ctx, e.st)
	if err != nil {
		t.Fatal(err)
	}
	if info3, ok := cs.Info(); !ok || info3.Fingerprint != info2.Fingerprint {
		t.Error("el certificado no se guardó")
	}
}

func TestValidate(t *testing.T) {
	ips := []string{"192.168.1.5"}
	tests := []struct {
		c   Config
		key string
	}{
		{Config{Port: 9077}, ""},
		{Config{Bind: BindLocal, Port: 1024}, ""},
		{Config{Bind: "192.168.1.5", Port: 65535}, ""},
		{Config{Port: 80}, "settings.net.err.port"},
		{Config{Port: 70000}, "settings.net.err.port"},
		{Config{Bind: "10.9.9.9", Port: 9077}, "settings.net.err.bind"},
	}
	for _, tt := range tests {
		err := Validate(tt.c, ips)
		var ne *Error
		switch {
		case tt.key == "" && err != nil:
			t.Errorf("%+v: error inesperado %v", tt.c, err)
		case tt.key != "" && (!errors.As(err, &ne) || ne.Msg.Key != tt.key):
			t.Errorf("%+v: error = %v, se esperaba %s", tt.c, err, tt.key)
		}
	}
}

func TestLoadSave(t *testing.T) {
	e := newEnv(t, time.Minute)
	if c, _ := Load(e.ctx, e.st, false); c != Default() {
		t.Errorf("por defecto = %+v", c)
	}
	if c, _ := Load(e.ctx, e.st, true); c.Port != LegacyPort {
		t.Errorf("instalación antigua = %+v", c)
	}
	want := Config{Bind: "127.0.0.1", Port: 9100, TLS: true}
	if err := Save(e.ctx, e.st, want); err != nil {
		t.Fatal(err)
	}
	if c, _ := Load(e.ctx, e.st, true); c != want {
		t.Errorf("guardado = %+v", c)
	}
	if want.String() != "https://127.0.0.1:9100" || (Config{Port: 9077}).String() != "http://0.0.0.0:9077" {
		t.Error("String incorrecto")
	}
}
