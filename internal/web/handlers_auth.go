package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/auth"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

type authForm struct {
	Error     string
	Next      string
	Languages []msg.Language // selector de idioma (solo en /setup)
}

func (s *Server) setupData(w http.ResponseWriter, r *http.Request, errText string) pageData {
	return pageData{
		Title: s.T(r, "setup.title"), CSRF: s.anonCSRF(w, r),
		Body: authForm{Error: errText, Languages: msg.Languages()},
	}
}

// handleSetupForm muestra /setup. Con ?lang=xx cambia el idioma de la página
// (se guarda en una cookie hasta que el setup lo fija como ajuste global).
func (s *Server) handleSetupForm(w http.ResponseWriter, r *http.Request) {
	if q := r.URL.Query().Get("lang"); q != "" {
		if l, ok := msg.Parse(q); ok {
			s.setCookie(w, r, langCookie, string(l), 0)
		}
		redirect(w, r, "/setup")
		return
	}
	s.render(w, r, http.StatusOK, "setup", s.setupData(w, r, ""))
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	if !checkAnonCSRF(r) {
		s.renderError(w, r, http.StatusForbidden, "http.forbidden")
		return
	}
	l := s.lang(r)
	fail := func(status int, key string, args ...any) {
		s.render(w, r, status, "setup", s.setupData(w, r, msg.F(l, key, args...)))
	}
	key := "setup|" + clientKey(r)
	if ok, wait := s.Limiter.Allow(key); !ok {
		fail(http.StatusTooManyRequests, "login.locked", roundWait(wait))
		return
	}
	if s.SetupToken == nil || !s.SetupToken.Check(r.PostFormValue("token")) {
		s.Limiter.Failure(key)
		s.Log.Warn("wrong setup token", "remote", r.RemoteAddr)
		fail(http.StatusUnauthorized, "setup.err.token")
		return
	}
	pw := r.PostFormValue("password")
	if e := auth.ValidatePassword(pw, r.PostFormValue("confirm")); e != "" {
		fail(http.StatusBadRequest, e)
		return
	}
	if err := s.Settings.SetLanguage(r.Context(), l); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.Admin.SetPassword(r.Context(), pw); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.SetupToken.Invalidate()
	s.Limiter.Success(key)
	s.setCookie(w, r, langCookie, "", -1)
	s.Log.Info("admin password set from /setup", "remote", r.RemoteAddr, "language", string(l))
	if !s.startSession(w, r) {
		return
	}
	redirect(w, r, "/admin?m=welcome")
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if _, ok := s.currentSession(r); ok {
		redirect(w, r, next)
		return
	}
	s.render(w, r, http.StatusOK, "login", pageData{
		Title: s.T(r, "login.title"), CSRF: s.anonCSRF(w, r), Body: authForm{Next: next},
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	if !checkAnonCSRF(r) {
		s.renderError(w, r, http.StatusForbidden, "http.forbidden")
		return
	}
	next := safeNext(r.PostFormValue("next"))
	l := s.lang(r)
	fail := func(status int, key string, args ...any) {
		s.render(w, r, status, "login", pageData{
			Title: msg.T(l, "login.title"), CSRF: s.anonCSRF(w, r), Body: authForm{Error: msg.F(l, key, args...), Next: next},
		})
	}
	key := "login|" + clientKey(r)
	if ok, wait := s.Limiter.Allow(key); !ok {
		fail(http.StatusTooManyRequests, "login.locked", roundWait(wait))
		return
	}
	ok, err := s.Admin.Verify(r.Context(), r.PostFormValue("password"))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !ok {
		s.Limiter.Failure(key)
		s.Log.Warn("failed login", "remote", r.RemoteAddr)
		fail(http.StatusUnauthorized, "login.err")
		return
	}
	s.Limiter.Success(key)
	if !s.startSession(w, r) {
		return
	}
	s.Log.Info("login", "remote", r.RemoteAddr)
	redirect(w, r, next)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.Sessions.Destroy(r.Context(), c.Value); err != nil {
			s.Log.Error("could not close the session", "error", err)
		}
	}
	s.setCookie(w, r, sessionCookie, "", -1)
	redirect(w, r, "/login")
}

// startSession crea la sesión y su cookie. Devuelve false si falló (ya se
// respondió con un error).
func (s *Server) startSession(w http.ResponseWriter, r *http.Request) bool {
	tok, sess, err := s.Sessions.Create(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return false
	}
	s.setCookie(w, r, sessionCookie, tok, int(time.Until(sess.ExpiresAt).Seconds()))
	s.setCookie(w, r, csrfCookie, "", -1) // ya no hace falta
	return true
}

// parseForm lee el formulario; responde con un error si es demasiado grande.
func (s *Server) parseForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.renderError(w, r, http.StatusRequestEntityTooLarge, "http.toolarge")
		} else {
			s.renderError(w, r, http.StatusBadRequest, "http.forbidden")
		}
		return false
	}
	return true
}

func roundWait(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}
