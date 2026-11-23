package theme

import (
	"strings"
	"testing"
)

func TestTemasIncluidos(t *testing.T) {
	compiled := Compile()
	for _, id := range []string{"auto", "light", "dark", "ocean", "sand"} {
		c, ok := compiled[id]
		if !ok || len(c.CSS) == 0 || c.ETag == "" {
			t.Fatalf("falta el tema %q", id)
		}
		css := string(c.CSS)
		for _, v := range Variables {
			if v == "color-scheme" {
				continue
			}
			if !strings.Contains(css, "--"+v+":") {
				t.Errorf("%s: falta la variable --%s", id, v)
			}
		}
	}
	auto := string(compiled["auto"].CSS)
	if !strings.Contains(auto, "@media (prefers-color-scheme: dark)") || !strings.Contains(auto, "color-scheme: dark") {
		t.Error("el tema automático debe cambiar según la preferencia del sistema")
	}
	if strings.Contains(string(compiled["dark"].CSS), "@media") || !strings.Contains(string(compiled["dark"].CSS), "color-scheme: dark") {
		t.Error("el tema oscuro debe ser siempre oscuro")
	}
	if compiled["light"].ETag == compiled["dark"].ETag {
		t.Error("temas distintos con la misma huella")
	}
	if !Exists("dark") || Exists("rosa") {
		t.Error("Exists incorrecto")
	}
}

func TestValidacion(t *testing.T) {
	incompleta := Theme{Base: Palette{"bg": "#000"}}
	if err := validate(incompleta); err == nil || !strings.Contains(err.Error(), "missing variable") {
		t.Errorf("paleta incompleta: %v", err)
	}
	desconocida := Theme{Base: with(LightPalette, Palette{"rosa": "#f0f"})}
	if err := validate(desconocida); err == nil || !strings.Contains(err.Error(), "unknown variable") {
		t.Errorf("variable desconocida: %v", err)
	}
	if err := validate(Theme{Base: LightPalette, Dark: Palette{"bg": "#000"}}); err != nil {
		t.Errorf("una paleta oscura parcial es válida: %v", err)
	}
}

// TestMosaicos: los temas con fondo llevan un SVG embebido pequeño (sin
// peticiones extra, permitido por la CSP con img-src data:); el resto, none.
func TestMosaicos(t *testing.T) {
	compiled := Compile()
	for _, id := range []string{"ocean", "sand"} {
		css := string(compiled[id].CSS)
		i := strings.Index(css, "--bg-pattern: url(\"data:image/svg+xml,")
		if i < 0 {
			t.Fatalf("%s: falta el mosaico de fondo", id)
		}
		line := css[i : strings.Index(css[i:], "\n")+i]
		if len(line) > 600 || strings.ContainsAny(line[len("--bg-pattern: url(\"data:"):len(line)-3], "#<>\"") {
			t.Errorf("%s: mosaico demasiado grande o mal codificado: %s", id, line)
		}
	}
	for _, id := range []string{"light", "dark"} {
		if !strings.Contains(string(compiled[id].CSS), "--bg-pattern: none;") {
			t.Errorf("%s: no debe llevar mosaico", id)
		}
	}
}
