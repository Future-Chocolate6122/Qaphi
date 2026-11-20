// Package store define la interfaz de acceso a datos. El resto de la
// aplicación depende solo de esta interfaz, de modo que el motor (hoy SQLite)
// puede cambiarse sin tocar nada más. La interfaz crece en cada fase con las
// operaciones que se vayan necesitando.
package store

import (
	"context"
	"errors"
	"time"

	"qaphi/internal/model"
)

// ErrNotFound indica que el elemento pedido no existe.
var ErrNotFound = errors.New("no encontrado")

// ErrLocked indica que la base de datos está en uso por otro proceso.
var ErrLocked = errors.New("la base de datos está en uso por otro proceso (¿hay otra instancia de qaphi en marcha?)")

// Store es el acceso a datos persistentes. Todas las escrituras de
// configuración son inmediatas; los resultados llegan en lotes.
type Store interface {
	// Ajustes clave/valor.
	GetSetting(ctx context.Context, key string) (value string, ok bool, err error)
	SetSetting(ctx context.Context, key, value string) error

	// Checks.
	ListChecks(ctx context.Context) ([]model.Check, error)
	GetCheck(ctx context.Context, id int64) (model.Check, error)
	CreateCheck(ctx context.Context, c *model.Check) error // asigna ID y fechas
	UpdateCheck(ctx context.Context, c *model.Check) error
	DeleteCheck(ctx context.Context, id int64) error // borra también sus resultados

	// Resultados.
	InsertResults(ctx context.Context, rs []model.Result) error // en una sola transacción
	LastResults(ctx context.Context) (map[int64]model.Result, error)

	// Sesiones.
	CreateSession(ctx context.Context, s model.Session) error
	GetSession(ctx context.Context, idHash []byte) (model.Session, error)
	DeleteSession(ctx context.Context, idHash []byte) error
	DeleteAllSessions(ctx context.Context) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error)

	// SchemaVersion devuelve la versión de esquema aplicada.
	SchemaVersion(ctx context.Context) (int64, error)
	// Close libera los recursos. Debe llamarse al final de la parada ordenada.
	Close() error
}
