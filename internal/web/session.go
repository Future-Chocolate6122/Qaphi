package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"qaphi/internal/auth"
	"qaphi/internal/model"
)

// Nombres de las cookies.
const (
	sessionCookie = "qaphi_session"
	csrfCookie    = "qaphi_csrf" // solo para formularios sin sesión (login, setup)
	csrfField     = "csrf"
)

type ctxKey int

const sessionKey ctxKey = 0

func withSession(r *http.Request, s model.Session) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), sessionKey, s))
}

func sessionFrom(r *http.Request) (model.Session, bool) {
	s, ok := r.Context().Value(sessionKey).(model.Session)
	return s, ok
}

// isHTTPS indica si el cliente llegó por HTTPS, directamente o a través de un
// proxy inverso. Solo decide si la cookie lleva Secure: una cabecera falsa
// solo afecta a quien la envía.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// currentSession devuelve la sesión de la cookie, si es válida.
func (s *Server) currentSession(r *http.Request) (model.Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return model.Session{}, false
	}
	sess, err := s.Sessions.Lookup(r.Context(), c.Value)
	if err != nil {
		if !errors.Is(err, auth.ErrNoSession) {
			s.Log.Error("error al validar la sesión", "error", err)
		}
		return model.Session{}, false
	}
	return sess, true
}

// requireAuth exige sesión. Sin ella, redirige a /login recordando la página
// pedida. En peticiones que modifican datos, además, comprueba el origen y el
// token CSRF de la sesión.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.currentSession(r)
		if !ok {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()))
				return
			}
			s.renderError(w, r, http.StatusForbidden, "http.forbidden")
			return
		}
		if !safeMethod(r.Method) {
			if !sameOrigin(r) || !tokenEqual(r.PostFormValue(csrfField), sess.CSRF) {
				s.Log.Warn("petición rechazada por CSRF", "ruta", r.URL.Path, "remoto", r.RemoteAddr)
				s.renderError(w, r, http.StatusForbidden, "http.forbidden")
				return
			}
		}
		next.ServeHTTP(w, withSession(r, sess))
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func tokenEqual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// sameOrigin comprueba que una petición que modifica datos viene de una
// página de Qaphi. Si el navegador envía Origin, debe coincidir con el host;
// si no, se usa Sec-Fetch-Site. Peticiones sin ninguna de las dos (clientes
// no navegadores) dependen solo del token CSRF.
func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// anonCSRF devuelve el token CSRF de los formularios sin sesión (patrón
// «double submit»: cookie + campo oculto), creándolo si hace falta.
func (s *Server) anonCSRF(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) >= 16 && len(c.Value) <= 64 {
		return c.Value
	}
	tok := rand.Text()
	s.setCookie(w, r, csrfCookie, tok, 0)
	return tok
}

// checkAnonCSRF valida un formulario sin sesión.
func checkAnonCSRF(r *http.Request) bool {
	c, err := r.Cookie(csrfCookie)
	return err == nil && sameOrigin(r) && tokenEqual(r.PostFormValue(csrfField), c.Value)
}

// clientKey identifica al cliente para el límite de intentos. No se confía en
// X-Forwarded-For porque cualquiera puede falsificarlo; detrás de un proxy
// inverso todos los clientes comparten clave, lo que con un único usuario es
// aceptable.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// safeNext valida la página a la que volver tras iniciar sesión: solo rutas
// locales, nunca otro sitio.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") ||
		strings.ContainsAny(next, "\r\n") {
		return "/home"
	}
	return next
}
