package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/store/sqlite"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		pw, confirm, want string
	}{
		{"corta", "corta", ErrPasswordShort},
		{"ñññññññññ", "ñññññññññ", ErrPasswordShort}, // 9 caracteres
		{"ññññññññññ", "ññññññññññ", ""},             // 10 caracteres
		{strings.Repeat("a", 73), strings.Repeat("a", 73), ErrPasswordLong},
		{"contraseña larga", "contraseña larg", ErrPasswordMismatch},
		{"una contraseña buena", "una contraseña buena", ""},
	}
	for _, tt := range tests {
		if got := ValidatePassword(tt.pw, tt.confirm); got != tt.want {
			t.Errorf("ValidatePassword(%q) = %q, se esperaba %q", tt.pw, got, tt.want)
		}
	}
}

func TestSetupToken(t *testing.T) {
	tok := NewSetupToken()
	s := tok.String()
	if len(s) != 19 || strings.Count(s, "-") != 3 {
		t.Fatalf("formato del token: %q", s)
	}
	if tok.Check("AAAA-BBBB-CCCC-DDDD") {
		t.Fatal("token incorrecto aceptado")
	}
	if !tok.Check(" " + strings.ToLower(s) + " ") {
		t.Fatal("el token debe aceptarse sin distinguir mayúsculas")
	}
	if !tok.Check(strings.ReplaceAll(s, "-", "")) {
		t.Fatal("el token debe aceptarse sin guiones")
	}
	tok.Invalidate()
	if tok.Check(s) {
		t.Fatal("un token usado no debe aceptarse")
	}
	if NewSetupToken().String() == s {
		t.Fatal("dos tokens iguales")
	}
}

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestAdmin(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	a, err := NewAdmin(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if a.HasPassword() {
		t.Fatal("instalación nueva sin contraseña")
	}
	if ok, _ := a.Verify(ctx, "lo que sea"); ok {
		t.Fatal("sin contraseña no debe verificarse nada")
	}
	sess := NewSessions(st, nil)
	tok, _, _ := sess.Create(ctx)

	if err := a.SetPassword(ctx, "contraseña segura"); err != nil {
		t.Fatal(err)
	}
	if !a.HasPassword() {
		t.Fatal("HasPassword tras guardar")
	}
	if ok, _ := a.Verify(ctx, "contraseña segura"); !ok {
		t.Fatal("la contraseña correcta no se verifica")
	}
	if ok, _ := a.Verify(ctx, "contraseña mala"); ok {
		t.Fatal("una contraseña incorrecta se verifica")
	}
	if _, err := sess.Lookup(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Fatal("cambiar la contraseña debe cerrar las sesiones")
	}
	h, _, _ := st.GetSetting(ctx, SettingPasswordHash)
	if !strings.HasPrefix(h, "$2") || strings.Contains(h, "segura") {
		t.Fatalf("hash inesperado: %q", h)
	}
	a2, _ := NewAdmin(ctx, st)
	if !a2.HasPassword() {
		t.Fatal("HasPassword no se carga al arrancar")
	}
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	now := time.Unix(1_800_000_000, 0)
	s := NewSessions(st, func() time.Time { return now })

	tok, sess, err := s.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !sess.ExpiresAt.Equal(now.Add(DefaultSessionTTL)) || sess.CSRF == "" {
		t.Fatalf("sesión = %+v", sess)
	}
	got, err := s.Lookup(ctx, tok)
	if err != nil || got.CSRF != sess.CSRF {
		t.Fatalf("Lookup = %+v, %v", got, err)
	}
	for _, bad := range []string{"", "otro", strings.Repeat("x", 200)} {
		if _, err := s.Lookup(ctx, bad); !errors.Is(err, ErrNoSession) {
			t.Errorf("Lookup(%q) = %v", bad, err)
		}
	}
	// Caducidad.
	now = now.Add(DefaultSessionTTL)
	if _, err := s.Lookup(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Fatal("una sesión caducada no debe valer")
	}
	// TTL configurable.
	st.SetSetting(ctx, SettingSessionTTL, "2h")
	tok2, sess2, _ := s.Create(ctx)
	if !sess2.ExpiresAt.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("TTL configurado no aplicado: %v", sess2.ExpiresAt.Sub(now))
	}
	s.Destroy(ctx, tok2)
	if _, err := s.Lookup(ctx, tok2); !errors.Is(err, ErrNoSession) {
		t.Fatal("Destroy no cerró la sesión")
	}
	// El token de la cookie no se guarda en claro.
	if _, err := st.GetSession(ctx, []byte(tok2)); err == nil {
		t.Fatal("el token se guardó sin hash")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(3, time.Minute, 10*time.Minute, func() time.Time { return now })
	steps := []struct {
		advance time.Duration
		fail    bool
		success bool
		allowed bool
	}{
		{0, true, false, true},            // 1 fallo
		{time.Second, true, false, true},  // 2
		{time.Second, true, false, false}, // 3: bloqueado
		{5 * time.Minute, false, false, false},
		{6 * time.Minute, false, false, true}, // fin del bloqueo
		{0, true, false, true},
		{2 * time.Minute, true, false, true}, // fuera de la ventana: empieza de nuevo
		{0, true, false, true},
		{0, false, true, true}, // éxito: borra el historial
		{0, true, false, true},
	}
	for i, s := range steps {
		now = now.Add(s.advance)
		if s.fail {
			l.Failure("1.2.3.4")
		}
		if s.success {
			l.Success("1.2.3.4")
		}
		if ok, _ := l.Allow("1.2.3.4"); ok != s.allowed {
			t.Fatalf("paso %d: Allow = %v, se esperaba %v", i, ok, s.allowed)
		}
	}
	if ok, _ := l.Allow("5.6.7.8"); !ok {
		t.Fatal("otra IP no debe verse afectada")
	}
}
