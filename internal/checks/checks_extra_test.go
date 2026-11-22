package checks

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

func TestHTTPPalabraClave(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html>estado: OK, base de datos conectada</html>"))
	}))
	defer srv.Close()
	tests := []struct {
		kw, mode string
		ok       bool
		want     string
	}{
		{"base de datos conectada", "present", true, "HTTP 200"},
		{"ERROR", "present", false, "falta la palabra clave"},
		{"ERROR", "absent", true, "HTTP 200"},
		{"estado: OK", "absent", false, "contiene la palabra clave"},
		{"estado: ok", "present", false, "falta"}, // distingue mayúsculas
	}
	for _, tt := range tests {
		c, p := params(t, "http", module.Values{"url": srv.URL, "keyword": tt.kw, "keyword_mode": tt.mode})
		r := c.Check(context.Background(), p)
		if r.OK != tt.ok || !strings.Contains(r.Message.In(msg.ES), tt.want) {
			t.Errorf("%q/%s: %+v %q", tt.kw, tt.mode, r, r.Message.In(msg.ES))
		}
	}
}

// shortCert crea un certificado autofirmado que caduca en d.
func shortCert(t *testing.T, d time.Duration) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "prueba"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(d),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestHTTPCaducidadTLS(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{shortCert(t, 5*24*time.Hour+time.Hour)}}
	srv.StartTLS()
	defer srv.Close()

	c, p := params(t, "http", module.Values{"url": srv.URL, "insecure_tls": "true", "tls_expiry_days": "14"})
	r := c.Check(context.Background(), p)
	if !r.OK || !r.Degraded || !strings.Contains(r.Message.In(msg.ES), "caduca en 5 días") || r.CertExpiry.IsZero() {
		t.Fatalf("certificado a punto de caducar: %+v %q", r, r.Message.In(msg.ES))
	}
	c, p = params(t, "http", module.Values{"url": srv.URL, "insecure_tls": "true", "tls_expiry_days": "0"})
	if r := c.Check(context.Background(), p); !r.OK || r.Degraded {
		t.Fatalf("sin aviso (0 días): %+v", r)
	}
	c, p = params(t, "http", module.Values{"url": srv.URL, "insecure_tls": "true", "tls_expiry_days": "3"})
	if r := c.Check(context.Background(), p); !r.OK || r.Degraded {
		t.Fatalf("con margen suficiente: %+v", r)
	}
}

func TestICMPLocal(t *testing.T) {
	c, p := params(t, "icmp", module.Values{"host": "127.0.0.1", "count": "2", "timeout": "3s"})
	r := c.Check(context.Background(), p)
	switch {
	case r.NoPerm:
		t.Logf("sin permisos ICMP en este entorno: %s", r.Message.In(msg.ES))
		if !strings.Contains(r.Message.In(msg.ES), "CAP_NET_RAW") {
			t.Error("el mensaje debe explicar cómo dar permisos")
		}
	case !r.OK || r.Latency <= 0 || !strings.Contains(r.Message.In(msg.ES), "2 recibidos de 2"):
		t.Fatalf("ping a 127.0.0.1: %+v %q", r, r.Message.In(msg.ES))
	}
	if got := c.Target(p); got != "127.0.0.1" {
		t.Errorf("Target = %q", got)
	}
}

func TestICMPSinRespuesta(t *testing.T) {
	// 192.0.2.0/24 (TEST-NET-1) no se enruta: no hay respuesta.
	c, p := params(t, "icmp", module.Values{"host": "192.0.2.1", "count": "1", "timeout": "1s"})
	r := c.Check(context.Background(), p)
	if r.OK && r.Latency < 5*time.Millisecond {
		t.Skip("la red de este entorno responde a cualquier ping; no se puede probar la falta de respuesta")
	}
	if r.OK && !r.NoPerm {
		t.Fatalf("no debería haber respuesta: %+v", r)
	}
	if !r.NoPerm && !strings.Contains(r.Message.In(msg.ES), "sin respuesta") {
		t.Errorf("mensaje = %q", r.Message.In(msg.ES))
	}
}

func TestIsPermission(t *testing.T) {
	if !isPermission(&net.OpError{Op: "listen", Err: osSyscallErr()}) {
		t.Error("EPERM debe reconocerse como falta de permisos")
	}
	if isPermission(&net.OpError{Op: "listen", Err: net.UnknownNetworkError("x")}) {
		t.Error("otro error no es de permisos")
	}
}

// dnsServer responde a consultas A, TXT y MX de «qaphi.test.».
func dnsServer(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dnsmessage.Parser
			h, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, err := p.Question()
			if err != nil {
				continue
			}
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true})
			b.EnableCompression()
			b.StartQuestions()
			b.Question(q)
			b.StartAnswers()
			rh := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}
			if q.Name.String() == "qaphi.test." {
				switch q.Type {
				case dnsmessage.TypeA:
					b.AResource(rh, dnsmessage.AResource{A: [4]byte{10, 1, 2, 3}})
				case dnsmessage.TypeTXT:
					b.TXTResource(rh, dnsmessage.TXTResource{TXT: []string{"v=spf1 -all"}})
				case dnsmessage.TypeMX:
					b.MXResource(rh, dnsmessage.MXResource{Pref: 10, MX: dnsmessage.MustNewName("mail.qaphi.test.")})
				}
			}
			out, err := b.Finish()
			if err != nil {
				continue
			}
			pc.WriteTo(out, addr)
		}
	}()
	return pc.LocalAddr().String()
}

func TestDNS(t *testing.T) {
	srv := dnsServer(t)
	tests := []struct {
		in   module.Values
		ok   bool
		want string
	}{
		{module.Values{"query": "qaphi.test", "record_type": "A"}, true, "10.1.2.3"},
		{module.Values{"query": "qaphi.test", "record_type": "A", "expected": "10.1.2.3"}, true, "10.1.2.3"},
		{module.Values{"query": "qaphi.test", "record_type": "A", "expected": "10.9.9.9"}, false, "no aparece el valor esperado"},
		{module.Values{"query": "qaphi.test", "record_type": "TXT", "expected": "spf1"}, true, "v=spf1 -all"},
		{module.Values{"query": "qaphi.test", "record_type": "MX"}, true, "mail.qaphi.test. 10"},
		{module.Values{"query": "otro.test", "record_type": "A"}, false, ""},
	}
	for _, tt := range tests {
		tt.in["server"] = srv
		tt.in["timeout"] = "2s"
		c, p := params(t, "dns", tt.in)
		r := c.Check(context.Background(), p)
		if r.OK != tt.ok || !strings.Contains(r.Message.In(msg.ES), tt.want) {
			t.Errorf("%v: %+v %q", tt.in, r, r.Message.In(msg.ES))
		}
	}
	e, _ := Registry.Lookup("dns")
	for _, bad := range []module.Values{{"query": "no valido"}, {"query": "1.2.3.4"}, {"query": "a.test", "server": "dns.google"}} {
		v, errs := e.Info.Normalize(bad)
		if errs == nil && e.Impl.Validate(v) == nil {
			t.Errorf("%v debería ser inválido", bad)
		}
	}
	if got := serverAddr("1.1.1.1"); got != "1.1.1.1:53" {
		t.Errorf("serverAddr = %q", got)
	}
	_, p := params(t, "dns", module.Values{"query": "qaphi.test", "record_type": "MX", "server": "192.168.1.1"})
	if got := e.Impl.Target(p); got != "MX qaphi.test @192.168.1.1" {
		t.Errorf("Target = %q", got)
	}
}

func TestHeartbeat(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	old := Heartbeats
	Heartbeats = NewHeartbeatRegistry(func() time.Time { return now })
	defer func() { Heartbeats = old }()

	e, _ := Registry.Lookup("heartbeat")
	prep := e.Impl.(Preparer).Prepare(module.Values{"period": "5m", "grace": "1m"})
	tok := prep["token"]
	if len(tok) != 26 {
		t.Fatalf("token generado = %q", tok)
	}
	if again := e.Impl.(Preparer).Prepare(prep); again["token"] != tok {
		t.Fatal("Prepare no debe cambiar un token existente")
	}
	c, p := params(t, "heartbeat", prep)
	if c.Target(p) != "/hb/"+tok {
		t.Errorf("Target = %q", c.Target(p))
	}
	Heartbeats.Sync(map[string]int64{tok: 7})

	check := func(want bool, msgPart string) {
		t.Helper()
		r := c.Check(context.Background(), p)
		if r.OK != want || !strings.Contains(r.Message.In(msg.ES), msgPart) {
			t.Fatalf("%+v %q", r, r.Message.In(msg.ES))
		}
	}
	check(true, "esperando el primer pulso") // recién arrancado
	now = now.Add(7 * time.Minute)
	check(false, "no ha llegado ningún pulso")
	if id, ok := Heartbeats.Ping(tok); !ok || id != 7 {
		t.Fatalf("Ping = %d, %v", id, ok)
	}
	if _, ok := Heartbeats.Ping("DESCONOCIDO"); ok {
		t.Fatal("un token desconocido no debe aceptarse")
	}
	now = now.Add(2 * time.Minute)
	check(true, "último pulso hace 2m0s")
	now = now.Add(5 * time.Minute)
	check(false, "sin pulsos desde hace 7m0s")
	// Al dejar de existir el check, el token deja de valer.
	Heartbeats.Sync(nil)
	if _, ok := Heartbeats.Ping(tok); ok {
		t.Fatal("token de un check borrado")
	}
	if _, errs := e.Info.Normalize(module.Values{"token": "corto"}); errs == nil {
		if e.Impl.Validate(module.Values{"token": "corto"}) == nil {
			t.Error("token no válido aceptado")
		}
	}
}

func osSyscallErr() error { return syscallEPERM }
