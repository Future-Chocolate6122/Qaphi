package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migration es un fichero NNNN_descripcion.sql.
type migration struct {
	version int64
	name    string
	sql     string
}

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

// loadMigrations lee las migraciones de fsys (directorio "migrations") y
// comprueba que están numeradas de forma consecutiva desde 1.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("invalid migration name %q (expected NNNN_description.sql)", e.Name())
		}
		v, _ := strconv.ParseInt(m[1], 10, 64)
		body, err := fs.ReadFile(fsys, path.Join("migrations", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		ms = append(ms, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if m.version != int64(i+1) {
			return nil, fmt.Errorf("non-consecutive migrations: expected %04d, found %s", i+1, m.name)
		}
	}
	return ms, nil
}

// migrate aplica las migraciones pendientes, cada una en su propia
// transacción junto con su registro en schema_migrations: o se aplica entera
// o no se aplica.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) (applied int, err error) {
	ms, err := loadMigrations(fsys)
	if err != nil {
		return 0, err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER NOT NULL PRIMARY KEY,
		applied_at INTEGER NOT NULL
	) STRICT`); err != nil {
		return 0, fmt.Errorf("create schema_migrations: %w", err)
	}
	current, err := schemaVersion(ctx, db)
	if err != nil {
		return 0, err
	}
	if latest := int64(len(ms)); current > latest {
		return 0, fmt.Errorf("the database has schema version %d but this binary only knows up to %d: was it created by a newer qaphi version?", current, latest)
	}
	for _, m := range ms {
		if m.version <= current {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %s: %w", m.name, err)
	}
	defer tx.Rollback() // sin efecto si ya se confirmó
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().Unix()); err != nil {
		return fmt.Errorf("migration %s: record version: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %s: commit: %w", m.name, err)
	}
	return nil
}

func schemaVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return v.Int64, nil
}
