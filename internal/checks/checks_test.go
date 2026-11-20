package checks

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"qaphi/internal/module"
)

// params normaliza los valores con la declaración registrada del tipo.
func params(t *testing.T, typ string, in module.Values) (Checker, module.Values) {
	t.Helper()
	e, ok := Registry.Lookup(typ)
	if !ok {
		t.Fatalf("tipo %q no registrado", typ)
	}
	v, errs := e.Info.Normalize(in)
	if errs != nil {
		t.Fatalf("parámetros no válidos: %v", errs)
	}
	if errs := e.Impl.Validate(v); errs != nil {
		t.Fatalf("Validate: %v", errs)
	}
	return e.Impl, v
}

func TestRegistro(t *testing.T) {
	for _, id := range []string{"http", "tcp"} {
		if _, ok := Registry.Lookup(id); !ok {
			t.Errorf("falta el tipo %q", id)
		}
	}
}

func TestHTTP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.UserAgent(), "Qaphi/") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte("hola"))
	})
	mux.HandleFunc("/fallo", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	mux.HandleFunc("/redir", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusFound) })
	mux.HandleFunc("/lento", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/solo-get", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	tlsSrv := httptest.NewTLSServer(mux)
	defer tlsSrv.Close()

	tests := []struct {
		name    string
		in      module.Values
		wantOK  bool
		wantMsg string
	}{
		{"200 esperado", module.Values{"url": srv.URL + "/ok"}, true, "HTTP 200"},
		{"503 no esperado", module.Values{"url": srv.URL + "/fallo"}, false, "HTTP 503 (no esperado)"},
		{"503 esperado", module.Values{"url": srv.URL + "/fallo", "expected_status": "500-599"}, true, "HTTP 503"},
		{"sigue redirecciones", module.Values{"url": srv.URL + "/redir"}, true, "HTTP 200"},
		{"sin seguir redirecciones", module.Values{"url": srv.URL + "/redir", "follow_redirects": "false"}, false, "HTTP 302"},
		{"302 esperado sin seguir", module.Values{"url": srv.URL + "/redir", "follow_redirects": "false", "expected_status": "200,302"}, true, "HTTP 302"},
		{"método HEAD", module.Values{"url": srv.URL + "/solo-get", "method": "HEAD"}, true, "HTTP 200"},
		{"tiempo agotado", module.Values{"url": srv.URL + "/lento", "timeout": "1s"}, false, "tiempo agotado"},
		{"certificado no válido", module.Values{"url": tlsSrv.URL + "/ok"}, false, "error TLS"},
		{"certificado ignorado", module.Values{"url": tlsSrv.URL + "/ok", "insecure_tls": "true"}, true, "HTTP 200"},
		{"puerto cerrado", module.Values{"url": "http://" + closedAddr(t) + "/"}, false, "conexión rechazada"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, p := params(t, "http", tt.in)
			r := c.Check(context.Background(), p)
			if r.OK != tt.wantOK || !strings.Contains(r.Message, tt.wantMsg) {
				t.Fatalf("resultado = %+v, se esperaba ok=%v y %q", r, tt.wantOK, tt.wantMsg)
			}
			if r.OK && r.Latency <= 0 {
				t.Errorf("latencia no medida: %v", r.Latency)
			}
		})
	}
}

func TestHTTPRespetaContexto(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	c, p := params(t, "http", module.Values{"url": srv.URL, "timeout": "60s"})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if r := c.Check(ctx, p); r.OK {
		t.Fatal("no debería ser ok")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("no respetó la cancelación del contexto: %v", d)
	}
}

func TestHTTPValidate(t *testing.T) {
	e, _ := Registry.Lookup("http")
	tests := []struct {
		in   module.Values
		errs []string
	}{
		{module.Values{"url": "ftp://x"}, []string{"url"}},
		{module.Values{"url": "https://"}, []string{"url"}},
		{module.Values{"url": "ejemplo.org"}, []string{"url"}},
		{module.Values{"url": "https://x", "expected_status": "abc"}, []string{"expected_status"}},
		{module.Values{"url": "https://x", "expected_status": "300-200"}, []string{"expected_status"}},
		{module.Values{"url": "https://x", "expected_status": "99"}, []string{"expected_status"}},
		{module.Values{"url": "https://x", "expected_status": "200, 301-308"}, nil},
	}
	for _, tt := range tests {
		v, nerrs := e.Info.Normalize(tt.in)
		if nerrs != nil {
			t.Fatalf("%v: %v", tt.in, nerrs)
		}
		errs := e.Impl.Validate(v)
		for _, k := range tt.errs {
			if errs[k] == "" {
				t.Errorf("%v: falta error en %s (%v)", tt.in, k, errs)
			}
		}
		if tt.errs == nil && errs != nil {
			t.Errorf("%v: errores inesperados %v", tt.in, errs)
		}
	}
}

func TestHTTPTargetOcultaClave(t *testing.T) {
	c, p := params(t, "http", module.Values{"url": "https://usuario:secreto@ejemplo.org/x"})
	if got := c.Target(p); strings.Contains(got, "secreto") {
		t.Fatalf("el destino muestra la contraseña: %s", got)
	}
}

func TestTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	c, p := params(t, "tcp", module.Values{"host": "127.0.0.1", "port": port})
	if r := c.Check(context.Background(), p); !r.OK || r.Latency <= 0 {
		t.Fatalf("puerto abierto: %+v", r)
	}
	if got := c.Target(p); got != "127.0.0.1:"+port {
		t.Errorf("Target = %q", got)
	}

	host, cport, _ := net.SplitHostPort(closedAddr(t))
	c, p = params(t, "tcp", module.Values{"host": host, "port": cport})
	if r := c.Check(context.Background(), p); r.OK || r.Message != "conexión rechazada" {
		t.Fatalf("puerto cerrado: %+v", r)
	}

	c, p = params(t, "tcp", module.Values{"host": "::1", "port": "22"})
	if got := c.Target(p); got != "[::1]:22" {
		t.Errorf("Target IPv6 = %q", got)
	}
}

func TestValidHost(t *testing.T) {
	for h, want := range map[string]bool{
		"ejemplo.org": true, "nas": true, "192.168.1.1": true, "::1": true, "[fe80::1]": true,
		"mi_host.local": true, "": false, "con espacio": false, "-malo.org": false, "a..b": false,
	} {
		if got := validHost(h); got != want {
			t.Errorf("validHost(%q) = %v, se esperaba %v", h, got, want)
		}
	}
}

// closedAddr devuelve una dirección local en la que no escucha nadie.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}
