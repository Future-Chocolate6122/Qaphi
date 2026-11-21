// Package msg centraliza los textos de la interfaz web en cuatro idiomas
// (castellano, inglés, alemán y francés).
//
// Cada texto se registra con sus cuatro traducciones juntas (Text), de modo
// que una traducción que falte es un error de programación que se detecta al
// arrancar. Los textos generales están en texts.go; cada módulo (check,
// aviso, tema…) registra los suyos con Register desde su propio fichero, así
// que añadir o quitar un módulo no obliga a tocar este paquete.
//
// Los logs y la salida de consola no pasan por aquí: van en inglés y se
// escriben directamente en el código.
package msg

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Lang es un código de idioma admitido.
type Lang string

// Idiomas admitidos.
const (
	ES Lang = "es"
	EN Lang = "en"
	DE Lang = "de"
	FR Lang = "fr"
)

// Default es el idioma de reserva cuando no se puede deducir otro.
const Default = EN

// Language describe un idioma para los selectores. El nombre va siempre en
// el propio idioma («Deutsch», no «Alemán»).
type Language struct {
	Code Lang
	Name string
}

var languages = []Language{{ES, "Español"}, {EN, "English"}, {DE, "Deutsch"}, {FR, "Français"}}

// Languages devuelve los idiomas admitidos.
func Languages() []Language { return append([]Language(nil), languages...) }

// Parse valida un código de idioma.
func Parse(s string) (Lang, bool) {
	for _, l := range languages {
		if string(l.Code) == s {
			return l.Code, true
		}
	}
	return "", false
}

// Text son las cuatro traducciones de un texto.
type Text struct{ ES, EN, DE, FR string }

// Tr construye un Text a partir de sus traducciones, en el orden castellano,
// inglés, alemán y francés. Es la forma de registrar textos desde otros
// paquetes.
func Tr(es, en, de, fr string) Text { return Text{ES: es, EN: en, DE: de, FR: fr} }

func (t Text) in(l Lang) string {
	switch l {
	case ES:
		return t.ES
	case DE:
		return t.DE
	case FR:
		return t.FR
	default:
		return t.EN
	}
}

var (
	mu      sync.RWMutex
	catalog = map[string]Text{}
)

// Register añade textos. Se llama desde init(); una clave repetida o una
// traducción vacía son errores de programación y provocan un pánico.
func Register(texts map[string]Text) {
	mu.Lock()
	defer mu.Unlock()
	for k, t := range texts {
		if _, dup := catalog[k]; dup {
			panic("msg: duplicate text key: " + k)
		}
		if t.ES == "" || t.EN == "" || t.DE == "" || t.FR == "" {
			panic("msg: missing translation for key: " + k)
		}
		catalog[k] = t
	}
}

// T devuelve el texto de key en el idioma l. Si la clave no existe, devuelve
// la propia clave para que la ausencia sea visible sin romper la página.
func T(l Lang, key string) string {
	mu.RLock()
	t, ok := catalog[key]
	mu.RUnlock()
	if !ok {
		return key
	}
	return t.in(l)
}

// F formatea el texto de key en el idioma l con fmt.Sprintf.
func F(l Lang, key string, args ...any) string {
	return fmt.Sprintf(T(l, key), args...)
}

// Has indica si existe un texto para key.
func Has(key string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := catalog[key]
	return ok
}

// Keys devuelve todas las claves registradas, ordenadas.
func Keys() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(catalog))
	for k := range catalog {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- mensajes

// Msg es un mensaje traducible: una clave y sus argumentos. Se usa para los
// errores de validación y para los mensajes de resultado de los checks, que
// se guardan codificados y se traducen al mostrarlos. Los textos que se usan
// con Msg deben formatear sus argumentos con %v.
type Msg struct {
	Key  string
	Args []any
}

// New crea un mensaje.
func New(key string, args ...any) Msg { return Msg{Key: key, Args: args} }

// IsZero indica si el mensaje está vacío.
func (m Msg) IsZero() bool { return m.Key == "" }

// In devuelve el mensaje en el idioma l.
func (m Msg) In(l Lang) string {
	if m.Key == "" {
		return ""
	}
	if !Has(m.Key) {
		// Texto sin clave (p. ej. mensajes guardados por versiones antiguas).
		return m.Key
	}
	return F(l, m.Key, m.Args...)
}

// String devuelve el mensaje en inglés (logs, errores técnicos).
func (m Msg) String() string { return m.In(EN) }

// Encode codifica el mensaje para guardarlo: un array JSON con la clave y los
// argumentos convertidos a texto, p. ej. ["check.http.msg.status","200"].
func (m Msg) Encode() string {
	if m.Key == "" {
		return ""
	}
	parts := make([]string, 0, len(m.Args)+1)
	parts = append(parts, m.Key)
	for _, a := range m.Args {
		parts = append(parts, fmt.Sprint(a))
	}
	b, _ := json.Marshal(parts)
	return string(b)
}

// Decode interpreta un mensaje guardado. Un texto que no es un mensaje
// codificado (datos de versiones anteriores) se devuelve tal cual como clave,
// y In lo mostrará sin traducir.
func Decode(s string) Msg {
	if !strings.HasPrefix(s, "[") {
		return Msg{Key: s}
	}
	var parts []string
	if err := json.Unmarshal([]byte(s), &parts); err != nil || len(parts) == 0 {
		return Msg{Key: s}
	}
	args := make([]any, 0, len(parts)-1)
	for _, p := range parts[1:] {
		args = append(args, p)
	}
	return Msg{Key: parts[0], Args: args}
}

// ---------------------------------------------------------------- negociación

// Negotiate elige el idioma a partir de la cabecera Accept-Language del
// navegador. Si ninguno de los pedidos está admitido, devuelve Default.
func Negotiate(acceptLanguage string) Lang {
	type cand struct {
		lang Lang
		q    float64
		pos  int
	}
	var cands []cand
	for i, part := range strings.Split(acceptLanguage, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
		l, ok := Parse(primary)
		if !ok {
			continue
		}
		q := 1.0
		if v, found := strings.CutPrefix(strings.TrimSpace(params), "q="); found {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				q = f
			}
		}
		if q > 0 {
			cands = append(cands, cand{l, q, i})
		}
	}
	if len(cands) == 0 {
		return Default
	}
	sort.SliceStable(cands, func(a, b int) bool { return cands[a].q > cands[b].q })
	return cands[0].lang
}
