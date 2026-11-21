package msg

import (
	"strings"
	"testing"
)

func TestTodasLasClavesTraducidas(t *testing.T) {
	// Register ya exige las cuatro traducciones; aquí se comprueba además
	// que no se ha copiado el mismo texto por descuido en textos largos.
	for _, k := range Keys() {
		mu.RLock()
		tx := catalog[k]
		mu.RUnlock()
		if len(tx.ES) > 25 && (tx.ES == tx.EN || tx.ES == tx.DE || tx.ES == tx.FR) {
			t.Errorf("%s: el texto parece sin traducir: %q", k, tx.ES)
		}
		// Mismo número de verbos de formato en todos los idiomas.
		n := strings.Count(tx.ES, "%")
		for _, other := range []string{tx.EN, tx.DE, tx.FR} {
			if strings.Count(other, "%") != n {
				t.Errorf("%s: distinto número de argumentos entre idiomas", k)
			}
		}
	}
}

func TestRegisterExigeTraducciones(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("una traducción vacía debe provocar un pánico")
		}
	}()
	Register(map[string]Text{"test.incompleto": {"hola", "hello", "", "salut"}})
}

func TestTyF(t *testing.T) {
	if got := T(DE, "status.down"); got != "Ausgefallen" {
		t.Errorf("T = %q", got)
	}
	if got := F(FR, "time.ago.m", 3); got != "il y a 3 min" {
		t.Errorf("F = %q", got)
	}
	if got := T(ES, "no.existe"); got != "no.existe" {
		t.Errorf("clave inexistente = %q", got)
	}
}

func TestMsgCodificado(t *testing.T) {
	m := New("field.too_long", 20)
	enc := m.Encode()
	if enc != `["field.too_long","20"]` {
		t.Fatalf("Encode = %s", enc)
	}
	d := Decode(enc)
	for l, want := range map[Lang]string{ES: "Admite como máximo 20 caracteres.", EN: "At most 20 characters."} {
		if got := d.In(l); got != want {
			t.Errorf("%s: %q, se esperaba %q", l, got, want)
		}
	}
	// Texto antiguo, sin codificar: se muestra tal cual.
	if got := Decode("conexión rechazada").In(EN); got != "conexión rechazada" {
		t.Errorf("texto antiguo = %q", got)
	}
	if Decode("[roto").In(ES) != "[roto" || New("").In(ES) != "" || New("").Encode() != "" {
		t.Error("casos límite incorrectos")
	}
}

func TestNegotiate(t *testing.T) {
	tests := map[string]Lang{
		"":                                 "en",
		"de-DE,de;q=0.9,en;q=0.8":          "de",
		"es-ES,es;q=0.9":                   "es",
		"it-IT,it;q=0.9,fr;q=0.7,en;q=0.5": "fr",
		"en;q=0.5, es;q=0.9":               "es",
		"pt-BR":                            "en",
		"fr;q=0, de":                       "de",
		"FR-ca":                            "fr",
	}
	for in, want := range tests {
		if got := Negotiate(in); got != want {
			t.Errorf("Negotiate(%q) = %q, se esperaba %q", in, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	if l, ok := Parse("de"); !ok || l != DE {
		t.Error("Parse(de)")
	}
	if _, ok := Parse("it"); ok {
		t.Error("Parse(it) no debe aceptarse")
	}
	if len(Languages()) != 4 {
		t.Error("deben ser cuatro idiomas")
	}
}
