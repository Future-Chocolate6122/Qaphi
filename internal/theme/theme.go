// Package theme gestiona los temas visuales.
//
// Un tema es un conjunto de valores para las variables CSS de Variables. Cada
// tema vive en su propio fichero, se registra solo desde init() y declara su
// paleta; Qaphi genera con ella una hoja de estilos pequeña y cacheable. La
// hoja de estilos general (style.css) solo usa variables, nunca colores
// fijos, así que añadir un tema no obliga a tocar nada más.
//
// Los nombres de las variables son estables: son la API de los temas.
package theme

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/future-chocolate6122/qaphi/internal/module"
)

// Variables son las variables CSS que todo tema debe definir (sin «--»).
var Variables = []string{
	"color-scheme", // "light" o "dark": controles nativos del navegador
	"bg", "surface", "text", "muted", "border",
	"accent", "accent-text", "danger", "banner-text",
	"status-up", "status-slow", "status-down", "status-unknown", "status-paused",
	"radius", "space", "font", "mono",
}

// Palette asigna un valor a cada variable.
type Palette map[string]string

// Theme es la definición de un tema.
type Theme struct {
	// Base es la paleta principal.
	Base Palette
	// Dark, si existe, se aplica cuando el sistema prefiere el modo oscuro
	// (prefers-color-scheme). Es lo que hace el tema «Automático».
	Dark Palette
}

// Registry contiene los temas disponibles.
var Registry = module.NewRegistry[Theme]("theme")

// DefaultID es el tema por defecto.
const DefaultID = "auto"

// Register valida y registra un tema. Una paleta incompleta o con variables
// desconocidas es un error de programación y provoca un pánico.
func Register(info module.Info, t Theme) {
	if err := validate(t); err != nil {
		panic(fmt.Sprintf("theme %q: %v", info.ID, err))
	}
	Registry.Register(info, t)
}

func validate(t Theme) error {
	known := map[string]bool{}
	for _, v := range Variables {
		known[v] = true
	}
	check := func(name string, p Palette, complete bool) error {
		for k := range p {
			if !known[k] {
				return fmt.Errorf("%s palette: unknown variable %q", name, k)
			}
		}
		if complete {
			for _, v := range Variables {
				if p[v] == "" {
					return fmt.Errorf("%s palette: missing variable %q", name, v)
				}
			}
		}
		return nil
	}
	if err := check("base", t.Base, true); err != nil {
		return err
	}
	if t.Dark != nil {
		return check("dark", t.Dark, false)
	}
	return nil
}

// CSS genera la hoja de estilos de un tema.
func (t Theme) CSS() []byte {
	var b strings.Builder
	writeRoot := func(p Palette) {
		keys := make([]string, 0, len(p))
		for k := range p {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString(":root {\n")
		for _, k := range keys {
			if k == "color-scheme" {
				fmt.Fprintf(&b, "  color-scheme: %s;\n", p[k])
				continue
			}
			fmt.Fprintf(&b, "  --%s: %s;\n", k, p[k])
		}
		b.WriteString("}\n")
	}
	writeRoot(t.Base)
	if t.Dark != nil {
		b.WriteString("@media (prefers-color-scheme: dark) {\n")
		writeRoot(t.Dark)
		b.WriteString("}\n")
	}
	return []byte(b.String())
}

// Compiled es la hoja de estilos de un tema lista para servir.
type Compiled struct {
	CSS  []byte
	ETag string
}

// Compile genera las hojas de estilos de todos los temas registrados.
func Compile() map[string]Compiled {
	out := map[string]Compiled{}
	for _, e := range Registry.List() {
		css := e.Impl.CSS()
		sum := sha256.Sum256(css)
		out[e.Info.ID] = Compiled{CSS: css, ETag: hex.EncodeToString(sum[:8])}
	}
	return out
}

// Exists indica si hay un tema con ese ID.
func Exists(id string) bool {
	_, ok := Registry.Lookup(id)
	return ok
}
