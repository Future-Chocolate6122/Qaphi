// Package store define la interfaz de acceso a datos. El resto de la
// aplicación depende solo de esta interfaz, de modo que el motor (hoy SQLite)
// puede cambiarse sin tocar nada más. La interfaz crece en cada fase con las
// operaciones que se vayan necesitando.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/model"
)

// ErrNotFound indica que el elemento pedido no existe.
var ErrNotFound = errors.New("not found")

// ErrLocked indica que la base de datos está en uso por otro proceso.
var ErrLocked = errors.New("the database is in use by another process (is another qaphi instance running?)")

// Batch es un lote de escritura: resultados en bruto (vacío en el modo
// «solo agregados») y agregados por hora que se suman a los guardados.
type Batch struct {
	Results []model.Result
	Hourly  []model.Hourly
}

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

	// Resultados y agregados. WriteBatch escribe todo el lote en una sola
	// transacción; los datos de checks que ya no existen se descartan.
	WriteBatch(ctx context.Context, b Batch) error
	// RecentResults devuelve los últimos n resultados de cada check, del más
	// antiguo al más reciente (para restaurar la barra de pulsos al arrancar).
	RecentResults(ctx context.Context, n int) (map[int64][]model.Result, error)
	// ResultsRange devuelve los resultados de un check en [from, to).
	ResultsRange(ctx context.Context, checkID int64, from, to time.Time) ([]model.Result, error)
	// ResultBuckets agrupa los resultados de un check en intervalos de step.
	ResultBuckets(ctx context.Context, checkID int64, from, to time.Time, step time.Duration) ([]model.Bucket, error)
	// HourlyRange devuelve los agregados por hora de un check en [from, to).
	HourlyRange(ctx context.Context, checkID int64, from, to time.Time) ([]model.Hourly, error)
	// HourlyTotals suma los agregados de cada check desde since.
	HourlyTotals(ctx context.Context, since time.Time) (map[int64]model.Hourly, error)

	// Incidencias (se escriben al momento).
	OpenIncident(ctx context.Context, i *model.Incident) error // asigna ID
	CloseIncident(ctx context.Context, id int64, end time.Time, lastError string) error
	OpenIncidents(ctx context.Context) (map[int64]model.Incident, error)
	ListIncidents(ctx context.Context, checkID int64, limit int) ([]model.Incident, error) // checkID 0 = todos

	// Retención: borra los datos anteriores a before y devuelve cuántos.
	DeleteResultsBefore(ctx context.Context, before time.Time) (int64, error)
	DeleteHourlyBefore(ctx context.Context, before time.Time) (int64, error)
	DeleteIncidentsBefore(ctx context.Context, before time.Time) (int64, error)

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
