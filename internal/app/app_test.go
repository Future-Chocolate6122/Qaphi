package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qaphi/internal/config"
)

// TestCicloDeVida comprueba el criterio de la fase 0: arranca, crea
// qaphi.db, responde en / y se detiene limpiamente.
func TestCicloDeVida(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "datos") // aún no existe: debe crearse
	cfg := &config.Config{Listen: "127.0.0.1:0", DataDir: dir, FlushInterval: 5 * time.Minute}
	var logs strings.Builder
	log := slog.New(slog.NewTextHandler(&logs, nil))

	a, err := New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, DBFileName)); err != nil {
		t.Fatalf("no se creó la base de datos: %v", err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm()&0o077 != 0 && !strings.Contains(logs.String(), "accesible") {
		t.Errorf("permisos del directorio de datos = %o", fi.Mode().Perm())
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, ln) }()

	res, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "Qaphi") {
		t.Fatalf("GET /: estado %d, cuerpo %q", res.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve devolvió error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve no terminó tras cancelar el contexto")
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond); err == nil {
		t.Error("el puerto sigue aceptando conexiones tras la parada")
	}

	// Un segundo arranque sobre los mismos datos funciona.
	a2, err := New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("segundo arranque: %v", err)
	}
	a2.Close()
}

func TestDataDirEsUnFichero(t *testing.T) {
	f := filepath.Join(t.TempDir(), "fichero")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Listen: "127.0.0.1:0", DataDir: f}
	if _, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("se esperaba error si el directorio de datos es un fichero")
	}
}

func TestPuertoOcupado(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg := &config.Config{Listen: ln.Addr().String(), DataDir: t.TempDir()}
	a, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "escuchar") {
		t.Fatalf("se esperaba error al escuchar en un puerto ocupado: %v", err)
	}
}
