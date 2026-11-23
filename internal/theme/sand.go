package theme

import (
	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

// Arena: tema claro y cálido, crema con acento granate. El fondo lleva un
// mosaico de rombos muy tenue (un SVG de unos 200 bytes que se repite).
var SandPalette = with(common, Palette{
	"color-scheme":   "light",
	"bg":             "#f5efe6",
	"surface":        "#fffcf7",
	"text":           "#2a221d",
	"muted":          "#6f6156",
	"border":         "#e5d9c7",
	"accent":         "#8b3a62",
	"accent-text":    "#ffffff",
	"danger":         "#b3322f",
	"status-up":      "#2b8452",
	"status-slow":    "#a8740a",
	"status-pending": "#b95f18",
	"status-down":    "#bb3a2e",
	"status-unknown": "#7f766c",
	"status-paused":  "#6f5cb0",
	"radius":         "8px",
	"bg-pattern": `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='32' height='32'%3E` +
		`%3Cpath d='M16 0L32 16L16 32L0 16Z' fill='none' stroke='%238b6a3e' stroke-opacity='.07'/%3E` +
		`%3Ccircle cx='16' cy='16' r='1.6' fill='%238b6a3e' fill-opacity='.08'/%3E%3C/svg%3E")`,
})

func init() {
	msg.Register(map[string]msg.Text{
		"theme.sand": msg.Tr("Arena (claro)", "Sand (light)", "Sand (hell)", "Sable (clair)"),
	})
	Register(module.Info{ID: "sand", Name: "theme.sand", Order: 50}, Theme{Base: SandPalette})
}
