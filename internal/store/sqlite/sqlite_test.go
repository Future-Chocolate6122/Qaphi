package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qaphi.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestOpenConfiguraLaBase(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no se creó el fichero: %v", err)
	}
	pragmas := map[string]string{
		"journal_mode": "wal",
		"synchronous":  "2", // FULL
		"foreign_keys": "1",
		"cache_size":   "-2048",
		"temp_store":   "2", // MEMORY
	}
	for p, want := range pragmas {
		var got string
		if err := s.db.QueryRowContext(ctx, "PRAGMA "+p).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", p, err)
		}
		if !strings.EqualFold(got, want) {
			t.Errorf("PRAGMA %s = %s, se esperaba %s", p, got, want)
		}
	}
	if n := s.db.Stats().MaxOpenConnections; n != 1 {
		t.Errorf("MaxOpenConnections = %d, se esperaba 1", n)
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := loadMigrations(embeddedMigrations)
	if v != int64(len(ms)) {
		t.Errorf("SchemaVersion = %d, se esperaba %d", v, len(ms))
	}
}

func TestSettings(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	if _, ok, err := s.GetSetting(ctx, "public"); err != nil || ok {
		t.Fatalf("ajuste inexistente: ok=%v err=%v", ok, err)
	}
	if err := s.SetSetting(ctx, "public", "false"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "public", "true"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := s.GetSetting(ctx, "public")
	if err != nil || !ok || v != "true" {
		t.Fatalf("GetSetting = %q, %v, %v", v, ok, err)
	}
	if err := s.SetSetting(ctx, "vacío", ""); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.GetSetting(ctx, "vacío"); !ok || v != "" {
		t.Fatalf("un valor vacío debe existir: %q %v", v, ok)
	}
}

func TestPersisteTrasReabrir(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "qaphi.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Tras un cierre limpio el WAL queda integrado y eliminado.
	if _, err := os.Stat(path + "-wal"); !os.IsNotExist(err) {
		t.Errorf("el fichero -wal debería haberse eliminado al cerrar (err=%v)", err)
	}

	s2, err := Open(ctx, path) // reabrir no debe volver a aplicar migraciones
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if v, ok, _ := s2.GetSetting(ctx, "k"); !ok || v != "v" {
		t.Fatalf("tras reabrir: %q %v", v, ok)
	}
}

func TestOpenRutaNoValida(t *testing.T) {
	if _, err := Open(context.Background(), filepath.Join(t.TempDir(), "a?b.db")); err == nil {
		t.Fatal("se esperaba error por '?' en la ruta")
	}
	if _, err := Open(context.Background(), filepath.Join(t.TempDir(), "no", "existe", "q.db")); err == nil {
		t.Fatal("se esperaba error si el directorio no existe")
	}
}

func TestEmbeddedMigrationsValidas(t *testing.T) {
	ms, err := loadMigrations(embeddedMigrations)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no hay migraciones embebidas")
	}
}

// rawDB abre una base de datos temporal sin migraciones, para probar migrate.
func rawDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(filepath.Join(t.TempDir(), "m.db")))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrate(t *testing.T) {
	ctx := context.Background()
	mig := func(files map[string]string) fstest.MapFS {
		fsys := fstest.MapFS{}
		for name, body := range files {
			fsys["migrations/"+name] = &fstest.MapFile{Data: []byte(body)}
		}
		return fsys
	}
	v1 := map[string]string{"0001_a.sql": "CREATE TABLE a (x INTEGER);"}
	v2 := map[string]string{
		"0001_a.sql": "CREATE TABLE a (x INTEGER);",
		"0002_b.sql": "CREATE TABLE b (y INTEGER); INSERT INTO b VALUES (1);",
	}

	t.Run("aplica en orden e idempotente", func(t *testing.T) {
		db := rawDB(t)
		if n, err := migrate(ctx, db, mig(v1)); err != nil || n != 1 {
			t.Fatalf("primera pasada: n=%d err=%v", n, err)
		}
		if n, err := migrate(ctx, db, mig(v2)); err != nil || n != 1 {
			t.Fatalf("segunda pasada: n=%d err=%v", n, err)
		}
		if n, err := migrate(ctx, db, mig(v2)); err != nil || n != 0 {
			t.Fatalf("tercera pasada: n=%d err=%v", n, err)
		}
		if v, _ := schemaVersion(ctx, db); v != 2 {
			t.Fatalf("versión = %d", v)
		}
	})

	t.Run("migración fallida no deja rastro", func(t *testing.T) {
		db := rawDB(t)
		bad := map[string]string{
			"0001_a.sql": "CREATE TABLE a (x INTEGER);",
			"0002_b.sql": "CREATE TABLE b (y INTEGER); ESTO NO ES SQL;",
		}
		n, err := migrate(ctx, db, mig(bad))
		if err == nil || !strings.Contains(err.Error(), "0002_b.sql") {
			t.Fatalf("se esperaba error en 0002_b.sql: %v", err)
		}
		if n != 1 {
			t.Fatalf("aplicadas = %d, se esperaba 1", n)
		}
		if v, _ := schemaVersion(ctx, db); v != 1 {
			t.Fatalf("versión = %d, se esperaba 1", v)
		}
		var cnt int
		db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'b'`).Scan(&cnt)
		if cnt != 0 {
			t.Fatal("la tabla b no debería existir tras el rollback")
		}
	})

	t.Run("esquema más reciente que el binario", func(t *testing.T) {
		db := rawDB(t)
		if _, err := migrate(ctx, db, mig(v2)); err != nil {
			t.Fatal(err)
		}
		if _, err := migrate(ctx, db, mig(v1)); err == nil || !strings.Contains(err.Error(), "más reciente") {
			t.Fatalf("se esperaba error de versión futura: %v", err)
		}
	})

	t.Run("numeración con huecos", func(t *testing.T) {
		gap := map[string]string{"0001_a.sql": "SELECT 1;", "0003_c.sql": "SELECT 1;"}
		if _, err := migrate(ctx, rawDB(t), mig(gap)); err == nil || !strings.Contains(err.Error(), "consecutivas") {
			t.Fatalf("se esperaba error de numeración: %v", err)
		}
	})

	t.Run("nombre no válido", func(t *testing.T) {
		bad := map[string]string{"1_a.sql": "SELECT 1;"}
		if _, err := migrate(ctx, rawDB(t), mig(bad)); err == nil || !strings.Contains(err.Error(), "nombre de migración") {
			t.Fatalf("se esperaba error de nombre: %v", err)
		}
	})
}
