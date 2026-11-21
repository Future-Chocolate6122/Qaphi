package theme

import (
	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

// Temas incluidos. Para añadir otro basta con un fichero que llame a
// Register con su paleta (y sus textos en los cuatro idiomas).
func init() {
	msg.Register(map[string]msg.Text{
		"theme.auto":  msg.Tr("Automático (según el sistema)", "Automatic (follow the system)", "Automatisch (wie das System)", "Automatique (selon le système)"),
		"theme.light": msg.Tr("Claro", "Light", "Hell", "Clair"),
		"theme.dark":  msg.Tr("Oscuro", "Dark", "Dunkel", "Sombre"),
	})
	Register(module.Info{ID: "auto", Name: "theme.auto", Order: 10}, Theme{Base: LightPalette, Dark: DarkPalette})
	Register(module.Info{ID: "light", Name: "theme.light", Order: 20}, Theme{Base: LightPalette})
	Register(module.Info{ID: "dark", Name: "theme.dark", Order: 30}, Theme{Base: DarkPalette})
}
