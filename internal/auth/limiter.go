package auth

import (
	"sync"
	"time"
)

// Limiter limita los intentos fallidos por clave (normalmente la IP del
// cliente): tras Max fallos dentro de Window, la clave queda bloqueada
// durante Lockout. Solo vive en memoria: un reinicio lo pone a cero, lo que
// es aceptable porque reiniciar requiere acceso al equipo.
type Limiter struct {
	Max     int
	Window  time.Duration
	Lockout time.Duration
	now     func() time.Time

	mu      sync.Mutex
	entries map[string]*limitEntry
}

type limitEntry struct {
	fails       int
	first       time.Time
	lockedUntil time.Time
}

// maxEntries acota la memoria ante muchas IP distintas.
const maxEntries = 10000

// NewLimiter crea un limitador. now puede ser nil (time.Now).
func NewLimiter(max int, window, lockout time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{Max: max, Window: window, Lockout: lockout, now: now, entries: map[string]*limitEntry{}}
}

// DefaultLimiter: 5 fallos en 15 minutos bloquean 15 minutos.
func DefaultLimiter() *Limiter { return NewLimiter(5, 15*time.Minute, 15*time.Minute, nil) }

// Allow indica si la clave puede intentarlo ahora y, si no, cuánto falta.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok {
		return true, 0
	}
	now := l.now()
	if now.Before(e.lockedUntil) {
		return false, e.lockedUntil.Sub(now)
	}
	return true, 0
}

// Failure registra un intento fallido.
func (l *Limiter) Failure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[key]
	if !ok || now.Sub(e.first) > l.Window {
		if !ok && len(l.entries) >= maxEntries {
			l.prune(now)
		}
		e = &limitEntry{first: now}
		l.entries[key] = e
	}
	e.fails++
	if e.fails >= l.Max {
		e.lockedUntil = now.Add(l.Lockout)
		e.fails = 0
		e.first = now
	}
}

// Success borra el historial de la clave.
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

// prune elimina las entradas que ya no bloquean ni cuentan.
func (l *Limiter) prune(now time.Time) {
	for k, e := range l.entries {
		if now.After(e.lockedUntil) && now.Sub(e.first) > l.Window {
			delete(l.entries, k)
		}
	}
}
