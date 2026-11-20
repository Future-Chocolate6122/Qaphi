// Package sqlite implementa store.Store sobre SQLite en Go puro
// (modernc.org/sqlite, sin CGO).
//
// Decisiones pensadas para almacenamiento delicado (microSD) y poca RAM:
//   - modo WAL: las escrituras son secuenciales y no bloquean las lecturas;
//   - synchronous=FULL: cada transacción confirmada sobrevive a un corte de
//     luz. Se escribe poco (ajustes al momento, resultados en lote), así que
//     el coste del fsync adicional es despreciable;
//   - una sola conexión: SQLite serializa las escrituras de todos modos y así
//     solo hay una caché de páginas en memoria;
//   - caché de páginas limitada a cacheSizeKiB.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registra el driver "sqlite"

	"qaphi/internal/store"
)

// cacheSizeKiB es el tamaño máximo de la caché de páginas de SQLite.
const cacheSizeKiB = 2048

// busyTimeout es lo que espera una operación si la base de datos está
// bloqueada por otro proceso (p. ej. una copia de seguridad en curso).
const busyTimeout = 5 * time.Second

// Store es la implementación SQLite de store.Store.
type Store struct {
	db   *sql.DB
	path string
}

var _ store.Store = (*Store)(nil)

// Open abre (o crea) la base de datos en path, la configura y aplica las
// migraciones pendientes. El directorio debe existir.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.Contains(path, "?") {
		return nil, errors.New("la ruta de la base de datos no puede contener '?'")
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("abrir %s: %w", path, err)
	}
	// Una única conexión, que se mantiene abierta durante toda la ejecución:
	// los PRAGMA de la DSN se aplican al abrirla.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	s := &Store{db: db, path: path}
	if err := s.init(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// dsn construye la cadena de conexión con los PRAGMA que se aplican a cada
// conexión nueva.
func dsn(path string) string {
	q := url.Values{}
	for _, p := range []string{
		"journal_mode(WAL)",
		"synchronous(FULL)",
		fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()),
		"foreign_keys(ON)",
		fmt.Sprintf("cache_size(-%d)", cacheSizeKiB), // negativo = KiB
		"temp_store(MEMORY)",
	} {
		q.Add("_pragma", p)
	}
	return path + "?" + q.Encode()
}

func (s *Store) init(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("abrir %s: %w", s.path, err)
	}
	var mode string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		return fmt.Errorf("comprobar modo de diario: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("no se pudo activar el modo WAL en %s (modo actual: %s); ¿está el directorio de datos en un sistema de ficheros de red?", s.path, mode)
	}
	if _, err := migrate(ctx, s.db, embeddedMigrations); err != nil {
		return err
	}
	return nil
}

// Path devuelve la ruta del fichero de base de datos.
func (s *Store) Path() string { return s.path }

// GetSetting implementa store.Store.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("leer ajuste %q: %w", key, err)
	}
	return v, true, nil
}

// SetSetting implementa store.Store.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("guardar ajuste %q: %w", key, err)
	}
	return nil
}

// SchemaVersion implementa store.Store.
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	return schemaVersion(ctx, s.db)
}

// Close implementa store.Store. Ejecuta PRAGMA optimize (recomendado por
// SQLite antes de cerrar) y cierra la conexión; al cerrarse la última
// conexión, SQLite integra el WAL en el fichero principal.
func (s *Store) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, optErr := s.db.ExecContext(ctx, `PRAGMA optimize`)
	return errors.Join(optErr, s.db.Close())
}
