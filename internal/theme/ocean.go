package theme

import (
	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

// Océano: tema oscuro en azules profundos con acento turquesa. El fondo lleva
// un mosaico de olas muy tenue (un SVG de unos 200 bytes que se repite).
var OceanPalette = with(common, Palette{
	"color-scheme":   "dark",
	"bg":             "#0b1622",
	"surface":        "#10202f",
	"text":           "#e3edf6",
	"muted":          "#93a9bd",
	"border":         "#1e3549",
	"accent":         "#4cb5dc",
	"accent-text":    "#04121c",
	"danger":         "#f0605d",
	"banner-text":    "#04121c", // texto oscuro sobre los colores de estado, más legible
	"status-up":      "#36c48f",
	"status-slow":    "#f2b33d",
	"status-pending": "#f28c4e",
	"status-down":    "#f0625e",
	"status-unknown": "#8195aa",
	"status-paused":  "#a491f2",
	"radius":         "12px",
	"bg-pattern": `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='48' height='24'%3E` +
		`%3Cpath d='M0 12q12-9 24 0t24 0' fill='none' stroke='%2367b4e0' stroke-opacity='.07' stroke-width='1.5'/%3E` +
		`%3Cpath d='M-24 24q12-9 24 0t24 0t24 0' fill='none' stroke='%2367b4e0' stroke-opacity='.04' stroke-width='1.5'/%3E%3C/svg%3E")`,
})

func init() {
	msg.Register(map[string]msg.Text{
		"theme.ocean": msg.Tr("Océano (oscuro)", "Ocean (dark)", "Ozean (dunkel)", "Océan (sombre)"),
	})
	Register(module.Info{ID: "ocean", Name: "theme.ocean", Order: 40}, Theme{Base: OceanPalette})
}
