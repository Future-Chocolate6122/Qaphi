package theme

import "maps"

// common son los valores que no dependen de claro u oscuro.
var common = Palette{
	"radius":      "10px",
	"space":       "1rem",
	"font":        `system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`,
	"mono":        `ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace`,
	"banner-text": "#ffffff",
	"bg-pattern":  "none",
}

// LightPalette es la paleta clara.
var LightPalette = with(common, Palette{
	"color-scheme":   "light",
	"bg":             "#f4f6f9",
	"surface":        "#ffffff",
	"text":           "#1b2130",
	"muted":          "#5b6475",
	"border":         "#dde2ea",
	"accent":         "#2f6fde",
	"accent-text":    "#ffffff",
	"danger":         "#c63b3b",
	"status-up":      "#2e9e5b",
	"status-slow":    "#d99a1e",
	"status-pending": "#e07b26",
	"status-down":    "#d64545",
	"status-unknown": "#8a93a3",
	"status-paused":  "#7a6fd0",
})

// DarkPalette es la paleta oscura.
var DarkPalette = with(common, Palette{
	"color-scheme":   "dark",
	"bg":             "#0f1218",
	"surface":        "#181c24",
	"text":           "#e4e8ef",
	"muted":          "#9aa3b2",
	"border":         "#2a313d",
	"accent":         "#6d9df0",
	"accent-text":    "#0d1117",
	"danger":         "#ef6b6b",
	"status-up":      "#3fb871",
	"status-slow":    "#e0a83a",
	"status-pending": "#f0904a",
	"status-down":    "#e5534b",
	"status-unknown": "#7c8595",
	"status-paused":  "#9488e0",
})

// with devuelve una copia de base con los valores de extra añadidos.
func with(base, extra Palette) Palette {
	out := maps.Clone(base)
	maps.Copy(out, extra)
	return out
}
