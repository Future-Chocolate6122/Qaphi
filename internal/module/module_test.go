package module

import (
	"strings"
	"testing"

	"github.com/future-chocolate6122/qaphi/internal/msg"
)

func init() {
	msg.Register(map[string]msg.Text{
		"test.mod":      msg.Tr("Módulo de prueba", "Módulo de prueba", "Módulo de prueba", "Módulo de prueba"),
		"test.url":      msg.Tr("URL", "URL", "URL", "URL"),
		"test.timeout":  msg.Tr("Tiempo máximo", "Tiempo máximo", "Tiempo máximo", "Tiempo máximo"),
		"test.retries":  msg.Tr("Reintentos", "Reintentos", "Reintentos", "Reintentos"),
		"test.insecure": msg.Tr("Ignorar TLS", "Ignorar TLS", "Ignorar TLS", "Ignorar TLS"),
		"test.method":   msg.Tr("Método", "Método", "Método", "Método"),
		"test.get":      msg.Tr("GET", "GET", "GET", "GET"),
		"test.head":     msg.Tr("HEAD", "HEAD", "HEAD", "HEAD"),
		"test.token":    msg.Tr("Token", "Token", "Token", "Token"),
		"test.body":     msg.Tr("Cuerpo", "Cuerpo", "Cuerpo", "Cuerpo"),
	})
}

func testInfo() Info {
	return Info{
		ID:   "demo",
		Name: "test.mod",
		Fields: []Field{
			{Key: "url", Label: "test.url", Type: Text, Required: true, MaxLen: 20},
			{Key: "timeout", Label: "test.timeout", Type: Duration, Default: "10s", Min: "1s", Max: "1m"},
			{Key: "retries", Label: "test.retries", Type: Int, Default: "3", Min: "1", Max: "10"},
			{Key: "insecure", Label: "test.insecure", Type: Bool},
			{Key: "method", Label: "test.method", Type: Select, Default: "GET", Options: []Option{{"GET", "test.get"}, {"HEAD", "test.head"}}},
			{Key: "token", Label: "test.token", Type: Secret},
			{Key: "body", Label: "test.body", Type: TextArea},
		},
	}
}

func TestNormalize(t *testing.T) {
	info := testInfo()
	if err := info.Validate(); err != nil {
		t.Fatalf("declaración de prueba no válida: %v", err)
	}
	tests := []struct {
		name     string
		in       Values
		want     Values // solo se comprueban estas claves
		errField string // campo con error esperado; vacío = sin error
	}{
		{
			name: "defaults y canonicalización",
			in:   Values{"url": "  https://x  ", "timeout": "", "extra": "se descarta"},
			want: Values{"url": "https://x", "timeout": "10s", "retries": "3", "insecure": "false", "method": "GET", "token": "", "body": ""},
		},
		{
			name: "duración canónica y bool marcado",
			in:   Values{"url": "a", "timeout": "45000ms", "insecure": "on"},
			want: Values{"timeout": "45s", "insecure": "true"},
		},
		{name: "duración sobre máximo", in: Values{"url": "a", "timeout": "90s"}, errField: "timeout"},
		{name: "bool en forma de formulario", in: Values{"url": "a", "insecure": "on"}, want: Values{"insecure": "true"}},
		{name: "secreto conserva espacios", in: Values{"url": "a", "token": " s3cr3t "}, want: Values{"token": " s3cr3t "}},
		{name: "texto de una línea sin saltos", in: Values{"url": "a\nb"}, want: Values{"url": "a b"}},
		{name: "textarea normaliza CRLF", in: Values{"url": "a", "body": "x\r\ny"}, want: Values{"body": "x\ny"}},
		{name: "obligatorio vacío", in: Values{"url": "   "}, errField: "url"},
		{name: "texto demasiado largo", in: Values{"url": strings.Repeat("x", 21)}, errField: "url"},
		{name: "longitud en runas, no bytes", in: Values{"url": strings.Repeat("ñ", 20)}, want: Values{"url": strings.Repeat("ñ", 20)}},
		{name: "entero no numérico", in: Values{"url": "a", "retries": "tres"}, errField: "retries"},
		{name: "entero bajo mínimo", in: Values{"url": "a", "retries": "0"}, errField: "retries"},
		{name: "entero sobre máximo", in: Values{"url": "a", "retries": "11"}, errField: "retries"},
		{name: "duración ilegible", in: Values{"url": "a", "timeout": "diez"}, errField: "timeout"},
		{name: "duración negativa", in: Values{"url": "a", "timeout": "-5s"}, errField: "timeout"},
		{name: "duración bajo mínimo", in: Values{"url": "a", "timeout": "500ms"}, errField: "timeout"},
		{name: "bool no válido", in: Values{"url": "a", "insecure": "quizá"}, errField: "insecure"},
		{name: "select fuera de lista", in: Values{"url": "a", "method": "DELETE"}, errField: "method"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errs := info.Normalize(tt.in)
			if tt.errField != "" {
				if errs[tt.errField].IsZero() {
					t.Fatalf("se esperaba error en %q, errores = %v", tt.errField, errs)
				}
				if got != nil {
					t.Fatalf("con errores no se deben devolver valores")
				}
				return
			}
			if errs != nil {
				t.Fatalf("errores inesperados: %v", errs)
			}
			if _, ok := got["extra"]; ok {
				t.Errorf("las claves no declaradas deben descartarse")
			}
			for k, want := range tt.want {
				if got[k] != want {
					t.Errorf("%s = %q, se esperaba %q", k, got[k], want)
				}
			}
		})
	}
}

func TestNormalizeTodosLosErrores(t *testing.T) {
	_, errs := testInfo().Normalize(Values{"retries": "x", "method": "y"})
	for _, k := range []string{"url", "retries", "method"} {
		if errs[k].IsZero() {
			t.Errorf("falta el error de %q (errores = %v)", k, errs)
		}
	}
}

func TestValuesAccessors(t *testing.T) {
	v, errs := testInfo().Normalize(Values{"url": "a", "timeout": "40s", "retries": "5", "insecure": "true"})
	if errs != nil {
		t.Fatal(errs)
	}
	if v.Duration("timeout").Seconds() != 40 || v.Int64("retries") != 5 || !v.Bool("insecure") || v.String("url") != "a" {
		t.Fatalf("accesores incorrectos: %v", v)
	}
}

func TestValidate(t *testing.T) {
	base := testInfo
	tests := []struct {
		name   string
		mutate func(*Info)
		want   string
	}{
		{"id con mayúsculas", func(i *Info) { i.ID = "HTTP" }, "invalid module id"},
		{"nombre sin registrar", func(i *Info) { i.Name = "no.existe" }, "unregistered text key"},
		{"campo duplicado", func(i *Info) { i.Fields = append(i.Fields, i.Fields[0]) }, "duplicate field"},
		{"clave de campo no válida", func(i *Info) { i.Fields[0].Key = "URL" }, "invalid key"},
		{"tipo desconocido", func(i *Info) { i.Fields[0].Type = "color" }, "unknown type"},
		{"select sin opciones", func(i *Info) { i.Fields[4].Options = nil }, "needs options"},
		{"límite ilegible", func(i *Info) { i.Fields[2].Max = "diez" }, "invalid limit"},
		{"min en texto", func(i *Info) { i.Fields[0].Min = "1" }, "min/max"},
		{"default incoherente", func(i *Info) { i.Fields[2].Default = "99" }, "invalid default value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := base()
			tt.mutate(&info)
			err := info.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, se esperaba %q", err, tt.want)
			}
		})
	}
}

type fakeImpl interface{ Hello() string }
type impl string

func (i impl) Hello() string { return string(i) }

func TestRegistry(t *testing.T) {
	r := NewRegistry[fakeImpl]("prueba")
	a := testInfo()
	a.ID, a.Order = "zeta", 1
	b := testInfo()
	b.ID, b.Order = "alfa", 2
	c := testInfo()
	c.ID, c.Order = "beta", 1
	r.Register(a, impl("a"))
	r.Register(b, impl("b"))
	r.Register(c, impl("c"))

	if r.Len() != 3 {
		t.Fatalf("Len = %d", r.Len())
	}
	var ids []string
	for _, e := range r.List() {
		ids = append(ids, e.Info.ID)
	}
	if got := strings.Join(ids, ","); got != "beta,zeta,alfa" {
		t.Fatalf("orden = %s", got)
	}
	e, ok := r.Lookup("alfa")
	if !ok || e.Impl.Hello() != "b" {
		t.Fatalf("Lookup(alfa) = %v, %v", e, ok)
	}
	if _, ok := r.Lookup("nada"); ok {
		t.Fatal("Lookup de un id inexistente debe fallar")
	}

	mustPanic(t, "duplicate id", func() { r.Register(a, impl("otra")) })
	bad := testInfo()
	bad.ID = "Mal"
	mustPanic(t, "invalid module id", func() { r.Register(bad, impl("x")) })
}

func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("se esperaba un pánico con %q", want)
		}
		if s, _ := r.(string); !strings.Contains(s, want) {
			t.Fatalf("pánico = %v, se esperaba %q", r, want)
		}
	}()
	fn()
}

func TestBoolConDefault(t *testing.T) {
	info := Info{ID: "b", Name: "test.mod", Fields: []Field{
		{Key: "x", Label: "test.insecure", Type: Bool, Default: "true"},
	}}
	if err := info.Validate(); err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"": "true", "false": "false", "on": "true"} {
		v, errs := info.Normalize(Values{"x": in})
		if errs != nil || v["x"] != want {
			t.Errorf("x=%q → %q (%v), se esperaba %q", in, v["x"], errs, want)
		}
	}
	if v, _ := info.Normalize(Values{}); v["x"] != "true" {
		t.Errorf("ausente → %q, se esperaba el valor por defecto", v["x"])
	}
}
