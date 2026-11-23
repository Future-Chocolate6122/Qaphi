package web

import (
	"html/template"
	"net/http"
	"net/url"

	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/state"
)

// Logotipo y favicon (ver docs/brand). En la interfaz el logotipo va en línea
// y monocromo (color del texto del tema) con el punto de estado en el color
// del estado global, que se actualiza en vivo. El favicon no puede usar las
// variables CSS del tema, así que lleva colores fijos de la marca, los mismos
// que static/js/qaphi.js.
const (
	brandBlue  = "#2f7fd6"
	brandUp    = "#2e9e5b"
	brandSlow  = "#d99a1e"
	brandDown  = "#d64545"
	brandOther = "#8a93a3"
)

// faviconColors devuelve el color del anillo y del punto para un estado
// global. Sin estado (login, setup), todo en el azul de la marca. Con
// caídas, también el anillo en rojo.
func faviconColors(level model.Status) (ring, dot string) {
	switch level {
	case "":
		return brandBlue, brandBlue
	case model.StatusUp:
		return brandBlue, brandUp
	case model.StatusDegraded, model.StatusPending:
		return brandBlue, brandSlow
	case model.StatusDown:
		return brandDown, brandDown
	default:
		return brandBlue, brandOther
	}
}

// faviconSVG es qaphi-favicon.svg con los colores del estado.
func faviconSVG(level model.Status) string {
	ring, dot := faviconColors(level)
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16">` +
		`<path d="M12.39 8.34A5.25 5.25 0 1 0 8.34 12.39" stroke="` + ring + `" stroke-width="2.5" stroke-linecap="round" fill="none"/>` +
		`<circle cx="12.34" cy="12.34" r="2.5" fill="` + dot + `"/></svg>`
}

// faviconURL devuelve el favicon como URL data: (la CSP admite img-src data:).
func faviconURL(level model.Status) template.URL {
	return template.URL("data:image/svg+xml," + url.PathEscape(faviconSVG(level))) //nolint:gosec // contenido propio
}

// headerLevel devuelve el estado global que muestran la cabecera y el
// favicon. Con sesión, el de todos los checks; sin sesión y con la página
// pública activada, el de los checks visibles; si no, ninguno (no se da
// información a quien no puede ver el estado).
func (s *Server) headerLevel(r *http.Request, loggedIn bool) model.Status {
	if !loggedIn && !s.Settings.Data().Public {
		return ""
	}
	cs, err := s.visibleChecks(r, loggedIn)
	if err != nil || len(cs) == 0 {
		return ""
	}
	return state.Summarize(cs, s.Tracker.Snapshot()).Level()
}
