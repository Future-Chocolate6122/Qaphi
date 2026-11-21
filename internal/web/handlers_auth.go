package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/auth"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

type authForm struct {
	Error string
	Next  string
}

func (s *Server) handleSetupForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "setup", pageData{
		Title: msg.T("setup.title"), CSRF: s.anonCSRF(w, r), Body: authForm{},
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	if !checkAnonCSRF(r) {
		s.renderError(w, r, http.StatusForbidden, "http.forbidden")
		return
	}
	fail := func(status int, key string, args ...any) {
		s.render(w, r, status, "setup", pageData{
			Title: msg.T("setup.title"), CSRF: s.anonCSRF(w, r), Body: authForm{Error: msg.F(key, args...)},
		})
	}
	key := "setup|" + clientKey(r)
	if ok, wait := s.Limiter.Allow(key); !ok {
		fail(http.StatusTooManyRequests, "login.locked", roundWait(wait))
		return
	}
	if s.SetupToken == nil || !s.SetupToken.Check(r.PostFormValue("token")) {
		s.Limiter.Failure(key)
		s.Log.Warn("token de configuración incorrecto", "remoto", r.RemoteAddr)
		fail(http.StatusUnauthorized, "setup.err.token")
		return
	}
	pw := r.PostFormValue("password")
	if e := auth.ValidatePassword(pw, r.PostFormValue("confirm")); e != "" {
		fail(http.StatusBadRequest, e)
		return
	}
	if err := s.Admin.SetPassword(r.Context(), pw); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.SetupToken.Invalidate()
	s.Limiter.Success(key)
	s.Log.Info("contraseña de admin configurada desde /setup", "remoto", r.RemoteAddr)
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
		Title: msg.T("login.title"), CSRF: s.anonCSRF(w, r), Body: authForm{Next: next},
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
	fail := func(status int, key string, args ...any) {
		s.render(w, r, status, "login", pageData{
			Title: msg.T("login.title"), CSRF: s.anonCSRF(w, r), Body: authForm{Error: msg.F(key, args...), Next: next},
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
		s.Log.Warn("inicio de sesión fallido", "remoto", r.RemoteAddr)
		fail(http.StatusUnauthorized, "login.err")
		return
	}
	s.Limiter.Success(key)
	if !s.startSession(w, r) {
		return
	}
	s.Log.Info("sesión iniciada", "remoto", r.RemoteAddr)
	redirect(w, r, next)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.Sessions.Destroy(r.Context(), c.Value); err != nil {
			s.Log.Error("no se pudo cerrar la sesión", "error", err)
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
