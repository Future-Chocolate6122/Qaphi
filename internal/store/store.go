// Package store define la interfaz de acceso a datos. El resto de la
// aplicación depende solo de esta interfaz, de modo que el motor (hoy SQLite)
// puede cambiarse sin tocar nada más. La interfaz crece en cada fase con las
// operaciones que se vayan necesitando.
package store

import "context"

// Store es el acceso a datos persistentes.
type Store interface {
	// GetSetting devuelve el valor de un ajuste. ok es false si no existe.
	GetSetting(ctx context.Context, key string) (value string, ok bool, err error)
	// SetSetting crea o sustituye un ajuste. Se escribe al momento.
	SetSetting(ctx context.Context, key, value string) error
	// SchemaVersion devuelve la versión de esquema aplicada.
	SchemaVersion(ctx context.Context) (int64, error)
	// Close libera los recursos. Debe llamarse al final de la parada ordenada.
	Close() error
}
