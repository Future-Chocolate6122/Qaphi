package module

import (
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/future-chocolate6122/qaphi/internal/msg"
)

var lineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

// Values son los valores de configuración de un módulo, en forma canónica de
// texto e indexados por la clave del campo. Es la forma en que se guardan en
// la base de datos y se exportan.
type Values map[string]string

// Errors asocia la clave de un campo con un mensaje de error traducible.
type Errors map[string]msg.Msg

func (e Errors) Error() string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(k + ": " + e[k].String())
	}
	return sb.String()
}

// Normalize valida la entrada contra los campos declarados y devuelve los
// valores en forma canónica. Las claves no declaradas se descartan y los
// campos vacíos o ausentes toman su valor por defecto (los booleanos sin
// valor por defecto valen "false"). Los formularios HTML envían un campo
// oculto "false" antes de cada casilla, de modo que una casilla desmarcada
// llega como "false" y no como ausente. Si hay errores, se devuelven todos
// juntos.
func (info Info) Normalize(in Values) (Values, Errors) {
	out := make(Values, len(info.Fields))
	errs := Errors{}
	for _, f := range info.Fields {
		raw := in[f.Key]
		if f.Type == Bool && strings.TrimSpace(raw) == "" && f.Default == "" {
			out[f.Key] = "false"
			continue
		}
		if f.Type != Secret {
			raw = strings.TrimSpace(raw)
		}
		if raw == "" {
			raw = f.Default
		}
		if raw == "" {
			if f.Required {
				errs[f.Key] = msg.New("field.required")
			} else {
				out[f.Key] = ""
			}
			continue
		}
		v, e := f.normalize(raw)
		if !e.IsZero() {
			errs[f.Key] = e
			continue
		}
		out[f.Key] = v
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

// normalize valida un valor no vacío y lo devuelve en forma canónica, o un
// mensaje de error para el usuario.
func (f Field) normalize(raw string) (string, msg.Msg) {
	var ok msg.Msg // mensaje vacío: sin error
	switch f.Type {
	case Text, Secret:
		if f.Type == Text {
			raw = lineBreaks.Replace(raw) // un campo de una línea no admite saltos
		}
		if n := f.maxLen(); utf8.RuneCountInString(raw) > n {
			return "", msg.New("field.too_long", n)
		}
		return raw, ok
	case TextArea:
		raw = strings.ReplaceAll(raw, "\r\n", "\n")
		if n := f.maxLen(); utf8.RuneCountInString(raw) > n {
			return "", msg.New("field.too_long", n)
		}
		return raw, ok
	case Int:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return "", msg.New("field.int")
		}
		if f.Min != "" {
			if lo, _ := strconv.ParseInt(f.Min, 10, 64); n < lo {
				return "", msg.New("field.min", f.Min)
			}
		}
		if f.Max != "" {
			if hi, _ := strconv.ParseInt(f.Max, 10, 64); n > hi {
				return "", msg.New("field.max", f.Max)
			}
		}
		return strconv.FormatInt(n, 10), ok
	case Duration:
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return "", msg.New("field.duration")
		}
		if f.Min != "" {
			if lo, _ := time.ParseDuration(f.Min); d < lo {
				return "", msg.New("field.min", f.Min)
			}
		}
		if f.Max != "" {
			if hi, _ := time.ParseDuration(f.Max); d > hi {
				return "", msg.New("field.max", f.Max)
			}
		}
		return d.String(), ok
	case Bool:
		switch strings.ToLower(raw) {
		case "true", "on", "1", "yes", "si", "sí":
			return "true", ok
		case "false", "off", "0", "no":
			return "false", ok
		}
		return "", msg.New("field.bool")
	case Select:
		for _, o := range f.Options {
			if o.Value == raw {
				return raw, ok
			}
		}
		return "", msg.New("field.select")
	}
	return "", msg.New("field.select")
}

func (f Field) maxLen() int {
	switch {
	case f.MaxLen > 0:
		return f.MaxLen
	case f.Type == TextArea:
		return defaultTextAreaLn
	default:
		return defaultMaxLen
	}
}

// Accesores tipados. Sobre valores ya normalizados no fallan; ante un valor
// ausente o ilegible devuelven el cero del tipo.

// String devuelve el valor tal cual.
func (v Values) String(key string) string { return v[key] }

// Int64 devuelve el valor como entero de 64 bits.
func (v Values) Int64(key string) int64 {
	n, _ := strconv.ParseInt(v[key], 10, 64)
	return n
}

// Duration devuelve el valor como duración.
func (v Values) Duration(key string) time.Duration {
	d, _ := time.ParseDuration(v[key])
	return d
}

// Bool devuelve el valor como booleano.
func (v Values) Bool(key string) bool { return v[key] == "true" }
