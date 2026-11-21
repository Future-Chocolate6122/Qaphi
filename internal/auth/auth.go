// Package auth gestiona la autenticación del único usuario, admin: la
// contraseña (bcrypt), el token de configuración inicial, las sesiones y la
// limitación de intentos.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

// Claves de ajustes.
const (
	SettingPasswordHash = "admin.password_hash"
	SettingSessionTTL   = "session.ttl"
)

// Valores por defecto y límites.
const (
	DefaultSessionTTL = 7 * 24 * time.Hour
	MinPasswordLen    = 10 // caracteres
	MaxPasswordBytes  = 72 // límite de bcrypt
	bcryptCost        = bcrypt.DefaultCost
)

// Errores de validación de la contraseña. Son claves de texto (paquete msg).
const (
	ErrPasswordShort    = "auth.err.password_short"
	ErrPasswordLong     = "auth.err.password_long"
	ErrPasswordMismatch = "auth.err.password_mismatch"
)

// ErrNoSession indica que no hay sesión válida.
var ErrNoSession = errors.New("invalid or expired session")

// ValidatePassword comprueba una contraseña nueva y su confirmación. Devuelve
// la clave del mensaje de error o "" si es válida.
func ValidatePassword(pw, confirm string) string {
	switch {
	case len([]rune(pw)) < MinPasswordLen:
		return ErrPasswordShort
	case len(pw) > MaxPasswordBytes:
		return ErrPasswordLong
	case pw != confirm:
		return ErrPasswordMismatch
	}
	return ""
}

// bcryptSem serializa las comparaciones bcrypt: cada una cuesta decenas o
// cientos de milisegundos de CPU en un ARM pequeño, y así un ataque de fuerza
// bruta no puede saturar el equipo.
var bcryptSem = make(chan struct{}, 1)

// Admin gestiona la contraseña del administrador.
type Admin struct {
	st      store.Store
	hasPass atomic.Bool
}

// NewAdmin carga el estado de la contraseña desde el almacenamiento.
func NewAdmin(ctx context.Context, st store.Store) (*Admin, error) {
	a := &Admin{st: st}
	h, ok, err := st.GetSetting(ctx, SettingPasswordHash)
	if err != nil {
		return nil, err
	}
	a.hasPass.Store(ok && h != "")
	return a, nil
}

// HasPassword indica si la contraseña ya está configurada. No accede al disco.
func (a *Admin) HasPassword() bool { return a.hasPass.Load() }

// SetPassword guarda una contraseña nueva (ya validada), cierra todas las
// sesiones abiertas y actualiza el indicador en memoria.
func (a *Admin) SetPassword(ctx context.Context, pw string) error {
	if err := SetPassword(ctx, a.st, pw); err != nil {
		return err
	}
	a.hasPass.Store(true)
	return nil
}

// SetPassword guarda una contraseña nueva y cierra todas las sesiones. Es la
// versión sin estado, usada también por reset-password.
func SetPassword(ctx context.Context, st store.Store, pw string) error {
	if len(pw) > MaxPasswordBytes {
		return errors.New("password too long")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := st.SetSetting(ctx, SettingPasswordHash, string(h)); err != nil {
		return err
	}
	return st.DeleteAllSessions(ctx)
}

// Verify comprueba la contraseña. Las comprobaciones se serializan.
func (a *Admin) Verify(ctx context.Context, pw string) (bool, error) {
	h, ok, err := a.st.GetSetting(ctx, SettingPasswordHash)
	if err != nil || !ok {
		return false, err
	}
	select {
	case bcryptSem <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-bcryptSem }()
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(pw)) == nil, nil
}

// ---------------------------------------------------------------- token de configuración

// SetupToken es el token de un solo uso que exige /setup. Solo existe en
// memoria y se imprime en el log al arrancar mientras no haya contraseña.
type SetupToken struct {
	mu    sync.Mutex
	value string // normalizado, sin guiones
	used  bool
}

// NewSetupToken genera un token aleatorio de 80 bits.
func NewSetupToken() *SetupToken {
	return &SetupToken{value: rand.Text()[:16]}
}

// String devuelve el token en grupos de cuatro: ABCD-EFGH-IJKL-MNOP.
func (t *SetupToken) String() string {
	v := t.value
	return v[0:4] + "-" + v[4:8] + "-" + v[8:12] + "-" + v[12:16]
}

// Check compara (en tiempo constante) el token introducido, sin distinguir
// mayúsculas ni tener en cuenta espacios o guiones.
func (t *SetupToken) Check(input string) bool {
	in := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(input)))
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.used {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(in), []byte(t.value)) == 1
}

// Invalidate inutiliza el token (tras usarlo).
func (t *SetupToken) Invalidate() {
	t.mu.Lock()
	t.used = true
	t.mu.Unlock()
}

// ---------------------------------------------------------------- sesiones

// Sessions crea y valida sesiones persistentes.
type Sessions struct {
	st  store.Store
	now func() time.Time
}

// NewSessions crea el gestor de sesiones. now puede ser nil (time.Now).
func NewSessions(st store.Store, now func() time.Time) *Sessions {
	if now == nil {
		now = time.Now
	}
	return &Sessions{st: st, now: now}
}

// HashToken devuelve el hash con el que se guarda un token de sesión.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// TTL devuelve la caducidad configurada de las sesiones.
func (s *Sessions) TTL(ctx context.Context) time.Duration {
	if v, ok, err := s.st.GetSetting(ctx, SettingSessionTTL); err == nil && ok {
		if d, err := time.ParseDuration(v); err == nil && d >= time.Hour && d <= 365*24*time.Hour {
			return d
		}
	}
	return DefaultSessionTTL
}

// Create abre una sesión y devuelve el token para la cookie.
func (s *Sessions) Create(ctx context.Context) (string, model.Session, error) {
	token := rand.Text()
	now := s.now()
	sess := model.Session{
		IDHash:    HashToken(token),
		CSRF:      rand.Text(),
		CreatedAt: now,
		ExpiresAt: now.Add(s.TTL(ctx)),
	}
	if err := s.st.CreateSession(ctx, sess); err != nil {
		return "", model.Session{}, err
	}
	return token, sess, nil
}

// Lookup valida un token de sesión.
func (s *Sessions) Lookup(ctx context.Context, token string) (model.Session, error) {
	if token == "" || len(token) > 128 {
		return model.Session{}, ErrNoSession
	}
	sess, err := s.st.GetSession(ctx, HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return model.Session{}, ErrNoSession
	}
	if err != nil {
		return model.Session{}, err
	}
	if !s.now().Before(sess.ExpiresAt) {
		_ = s.st.DeleteSession(ctx, sess.IDHash)
		return model.Session{}, ErrNoSession
	}
	return sess, nil
}

// Destroy cierra una sesión.
func (s *Sessions) Destroy(ctx context.Context, token string) error {
	return s.st.DeleteSession(ctx, HashToken(token))
}

// Cleanup borra las sesiones caducadas.
func (s *Sessions) Cleanup(ctx context.Context) (int64, error) {
	return s.st.DeleteExpiredSessions(ctx, s.now())
}
