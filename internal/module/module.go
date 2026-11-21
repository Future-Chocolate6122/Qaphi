// Package module define el mecanismo común de los módulos de Qaphi: tipos de
// check, canales de aviso y componentes visuales.
//
// Cada módulo vive en su propio fichero, se registra solo desde init() y
// declara sus campos de configuración con un Info. A partir de esa
// declaración se generan los formularios de /admin y se validan los valores
// recibidos, sin que el resto de la aplicación conozca los detalles de cada
// módulo. Quitar un fichero (o, en el futuro, excluirlo con build tags) elimina
// el módulo sin tocar nada más.
package module

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/msg"
)

// FieldType es el tipo de un campo declarado por un módulo. Determina el
// control del formulario y la validación que se aplica.
type FieldType string

const (
	Text     FieldType = "text"     // texto de una línea
	TextArea FieldType = "textarea" // texto de varias líneas
	Secret   FieldType = "secret"   // contraseña o token: nunca se muestra ni se registra en logs
	Int      FieldType = "int"      // entero de 64 bits
	Duration FieldType = "duration" // duración de Go (30s, 5m…)
	Bool     FieldType = "bool"     // casilla de verificación
	Select   FieldType = "select"   // una opción de una lista cerrada
)

// Longitudes máximas por defecto si el campo no declara MaxLen.
const (
	defaultMaxLen     = 255
	defaultTextAreaLn = 4096
)

// Option es una opción de un campo Select.
type Option struct {
	Value string
	Label string // clave de texto (paquete msg)
}

// Field declara un campo de configuración de un módulo.
type Field struct {
	Key      string    // identificador estable; se usa en formularios, base de datos y exportación
	Label    string    // clave de texto (paquete msg)
	Help     string    // clave de texto opcional con una ayuda breve
	Type     FieldType //
	Required bool      //
	Default  string    // valor por defecto, en el mismo formato que la entrada
	MaxLen   int       // longitud máxima para textos; 0 = valor por defecto del tipo
	Min, Max string    // límites para Int y Duration, en su formato ("1", "500ms"); vacío = sin límite
	Options  []Option  // opciones de un Select
}

// Info describe un módulo registrado.
type Info struct {
	ID          string  // identificador estable en minúsculas, p. ej. "http"
	Name        string  // clave de texto con el nombre visible
	Description string  // clave de texto opcional
	Order       int     // orden de aparición en la interfaz (menor primero; empate por ID)
	Fields      []Field //
}

var (
	idRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// Validate comprueba que la declaración es coherente. Un error aquí es un
// fallo de programación del módulo.
func (info Info) Validate() error {
	if !idRe.MatchString(info.ID) {
		return fmt.Errorf("invalid module id %q", info.ID)
	}
	if err := checkText(info.Name, true); err != nil {
		return fmt.Errorf("module %q: name: %w", info.ID, err)
	}
	if err := checkText(info.Description, false); err != nil {
		return fmt.Errorf("module %q: description: %w", info.ID, err)
	}
	seen := map[string]bool{}
	for _, f := range info.Fields {
		if err := f.validate(); err != nil {
			return fmt.Errorf("module %q: field %q: %w", info.ID, f.Key, err)
		}
		if seen[f.Key] {
			return fmt.Errorf("module %q: duplicate field %q", info.ID, f.Key)
		}
		seen[f.Key] = true
	}
	return nil
}

func (f Field) validate() error {
	if !keyRe.MatchString(f.Key) {
		return fmt.Errorf("invalid key")
	}
	if err := checkText(f.Label, true); err != nil {
		return fmt.Errorf("label: %w", err)
	}
	if err := checkText(f.Help, false); err != nil {
		return fmt.Errorf("help: %w", err)
	}
	switch f.Type {
	case Text, TextArea, Secret, Bool:
	case Int:
		for _, s := range []string{f.Min, f.Max} {
			if s != "" {
				if _, err := strconv.ParseInt(s, 10, 64); err != nil {
					return fmt.Errorf("invalid limit %q", s)
				}
			}
		}
	case Duration:
		for _, s := range []string{f.Min, f.Max} {
			if s != "" {
				if _, err := time.ParseDuration(s); err != nil {
					return fmt.Errorf("invalid limit %q", s)
				}
			}
		}
	case Select:
		if len(f.Options) == 0 {
			return fmt.Errorf("a select field needs options")
		}
		vals := map[string]bool{}
		for _, o := range f.Options {
			if vals[o.Value] {
				return fmt.Errorf("duplicate option %q", o.Value)
			}
			vals[o.Value] = true
			if err := checkText(o.Label, true); err != nil {
				return fmt.Errorf("option %q: %w", o.Value, err)
			}
		}
	default:
		return fmt.Errorf("unknown type %q", f.Type)
	}
	if (f.Min != "" || f.Max != "") && f.Type != Int && f.Type != Duration {
		return fmt.Errorf("min/max are only allowed on int and duration fields")
	}
	if f.MaxLen < 0 {
		return fmt.Errorf("negative MaxLen")
	}
	if f.Default != "" {
		if _, msgErr := f.normalize(f.Default); !msgErr.IsZero() {
			return fmt.Errorf("invalid default value %q: %s", f.Default, msgErr)
		}
	}
	return nil
}

// checkText comprueba que una clave de texto existe en el catálogo de msg.
func checkText(key string, required bool) error {
	if key == "" {
		if required {
			return fmt.Errorf("missing text key")
		}
		return nil
	}
	if !msg.Has(key) {
		return fmt.Errorf("unregistered text key %q", key)
	}
	return nil
}
