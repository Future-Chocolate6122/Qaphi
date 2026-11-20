package module

import (
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"qaphi/internal/msg"
)

var lineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

// Values son los valores de configuración de un módulo, en forma canónica de
// texto e indexados por la clave del campo. Es la forma en que se guardan en
// la base de datos y se exportan.
type Values map[string]string

// Errors asocia la clave de un campo con un mensaje de error para el usuario.
type Errors map[string]string

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
		sb.WriteString(k + ": " + e[k])
	}
	return sb.String()
}

// Normalize valida la entrada contra los campos declarados y devuelve los
// valores en forma canónica. Las claves no declaradas se descartan, los
// campos vacíos toman su valor por defecto y los booleanos ausentes valen
// "false" (una casilla sin marcar no se envía en un formulario HTML).
// Si hay errores, se devuelven todos juntos.
func (info Info) Normalize(in Values) (Values, Errors) {
	out := make(Values, len(info.Fields))
	errs := Errors{}
	for _, f := range info.Fields {
		raw, present := in[f.Key]
		if f.Type == Bool && !present {
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
				errs[f.Key] = msg.T("field.required")
			} else {
				out[f.Key] = ""
			}
			continue
		}
		v, e := f.normalize(raw)
		if e != "" {
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
func (f Field) normalize(raw string) (string, string) {
	switch f.Type {
	case Text, Secret:
		if f.Type == Text {
			raw = lineBreaks.Replace(raw) // un campo de una línea no admite saltos
		}
		if n := f.maxLen(); utf8.RuneCountInString(raw) > n {
			return "", msg.F("field.too_long", n)
		}
		return raw, ""
	case TextArea:
		raw = strings.ReplaceAll(raw, "\r\n", "\n")
		if n := f.maxLen(); utf8.RuneCountInString(raw) > n {
			return "", msg.F("field.too_long", n)
		}
		return raw, ""
	case Int:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return "", msg.T("field.int")
		}
		if f.Min != "" {
			if lo, _ := strconv.ParseInt(f.Min, 10, 64); n < lo {
				return "", msg.F("field.int_min", f.Min)
			}
		}
		if f.Max != "" {
			if hi, _ := strconv.ParseInt(f.Max, 10, 64); n > hi {
				return "", msg.F("field.int_max", f.Max)
			}
		}
		return strconv.FormatInt(n, 10), ""
	case Duration:
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return "", msg.T("field.duration")
		}
		if f.Min != "" {
			if lo, _ := time.ParseDuration(f.Min); d < lo {
				return "", msg.F("field.duration_min", f.Min)
			}
		}
		if f.Max != "" {
			if hi, _ := time.ParseDuration(f.Max); d > hi {
				return "", msg.F("field.duration_max", f.Max)
			}
		}
		return d.String(), ""
	case Bool:
		switch strings.ToLower(raw) {
		case "true", "on", "1", "yes", "si", "sí":
			return "true", ""
		case "false", "off", "0", "no":
			return "false", ""
		}
		return "", msg.T("field.bool")
	case Select:
		for _, o := range f.Options {
			if o.Value == raw {
				return raw, ""
			}
		}
		return "", msg.T("field.select")
	}
	return "", msg.T("field.select")
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
