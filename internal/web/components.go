package web

import (
	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

// Component es un elemento visual de una página de estado. Cada uno tiene
// una plantilla (templates/components/) que deja preparado su contenedor y,
// si necesita dibujar algo, un renderizador en qaphi.js con el mismo ID que
// trabaja con el modelo de datos común (el JSON de la página). Los
// componentes nunca acceden al almacenamiento.
//
// En la fase de «Diseño» se podrán activar, desactivar y ordenar desde
// /admin; de momento se muestran todos en su orden por defecto.
type Component struct {
	Page     string // "home" o "check"
	Template string // nombre de la plantilla
}

// Components es el registro de componentes visuales.
var Components = module.NewRegistry[Component]("component")

func init() {
	msg.Register(map[string]msg.Text{
		"component.banner":    msg.Tr("Estado global", "Overall status", "Gesamtstatus", "État global"),
		"component.checks":    msg.Tr("Servicios y pulsos", "Services and beats", "Dienste und Pulse", "Services et pulsations"),
		"component.incidents": msg.Tr("Últimas incidencias", "Latest incidents", "Letzte Vorfälle", "Derniers incidents"),
		"component.summary":   msg.Tr("Resumen del servicio", "Service summary", "Dienstübersicht", "Résumé du service"),
		"component.chart":     msg.Tr("Gráfica de respuesta", "Response chart", "Antwortzeit-Diagramm", "Graphique de réponse"),
		"component.heatmap":   msg.Tr("Mapa de 90 días", "90-day map", "90-Tage-Karte", "Carte sur 90 jours"),
	})
	for _, c := range []struct {
		id, page string
		order    int
	}{
		{"banner", "home", 10}, {"checks", "home", 20}, {"incidents", "home", 30},
		{"summary", "check", 10}, {"chart", "check", 20}, {"heatmap", "check", 30}, {"incidents", "check", 40},
	} {
		id := c.page + "_" + c.id
		Components.Register(module.Info{ID: id, Name: "component." + c.id, Order: c.order},
			Component{Page: c.page, Template: "c-" + c.page + "-" + c.id})
	}
}

// pageComponents devuelve las plantillas de los componentes de una página,
// en orden.
func pageComponents(page string) []string {
	var out []string
	for _, e := range Components.List() {
		if e.Impl.Page == page {
			out = append(out, e.Impl.Template)
		}
	}
	return out
}
