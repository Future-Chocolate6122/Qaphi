// Package app ensambla los componentes de Qaphi y gestiona su ciclo de vida:
// arranque, servicio y parada ordenada.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"qaphi/internal/config"
	"qaphi/internal/store/sqlite"
	"qaphi/internal/version"
	"qaphi/internal/web"
)

// DBFileName es el nombre del fichero de base de datos dentro del directorio
// de datos.
const DBFileName = "qaphi.db"

// shutdownTimeout es lo que se espera a que terminen las peticiones en curso
// durante la parada.
const shutdownTimeout = 10 * time.Second

// App es una instancia de Qaphi en marcha.
type App struct {
	cfg   *config.Config
	log   *slog.Logger
	store *sqlite.Store
	web   *web.Server
}

// New prepara el directorio de datos, abre la base de datos (aplicando las
// migraciones) y construye el servidor web. No empieza a escuchar.
func New(ctx context.Context, cfg *config.Config, log *slog.Logger) (*App, error) {
	if err := prepareDataDir(cfg.DataDir, log); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(cfg.DataDir, DBFileName)
	st, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("base de datos: %w", err)
	}
	schema, err := st.SchemaVersion(ctx)
	if err != nil {
		st.Close()
		return nil, err
	}
	log.Info("base de datos lista", "ruta", dbPath, "esquema", schema)

	w, err := web.New(web.Deps{Log: log})
	if err != nil {
		st.Close()
		return nil, err
	}
	return &App{cfg: cfg, log: log, store: st, web: w}, nil
}

// prepareDataDir crea el directorio de datos con permisos restringidos.
func prepareDataDir(dir string, log *slog.Logger) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("crear el directorio de datos %s: %w", dir, err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("directorio de datos %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s no es un directorio", dir)
	}
	// En Windows los permisos Unix no son significativos.
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		log.Warn("el directorio de datos es accesible por otros usuarios; se recomienda chmod 700",
			"ruta", dir, "permisos", fmt.Sprintf("%04o", fi.Mode().Perm()))
	}
	return nil
}

// Run escucha en la dirección configurada y sirve hasta que ctx se cancela.
func (a *App) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", a.cfg.Listen)
	if err != nil {
		return fmt.Errorf("escuchar en %s: %w", a.cfg.Listen, err)
	}
	return a.Serve(ctx, ln)
}

// Serve sirve peticiones en ln hasta que ctx se cancela y después detiene el
// servidor HTTP de forma ordenada. No cierra la base de datos: eso lo hace
// Close, para que el llamante controle el orden de parada.
func (a *App) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           a.web,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(a.log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	a.log.Info("qaphi en marcha", "versión", version.Get().Version, "escucha", ln.Addr().String(),
		"datos", a.cfg.DataDir, "volcado", a.cfg.FlushInterval.String())

	select {
	case err := <-errc:
		// El servidor terminó por sí solo: es un error.
		return fmt.Errorf("servidor HTTP: %w", err)
	case <-ctx.Done():
	}

	a.log.Info("deteniendo el servidor HTTP")
	shCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		a.log.Warn("parada del servidor HTTP incompleta; se cierran las conexiones restantes", "error", err)
		srv.Close()
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("servidor HTTP: %w", err)
	}
	return nil
}

// Close libera los recursos en orden. En fases posteriores, aquí se volcará
// el buffer de resultados antes de cerrar la base de datos.
func (a *App) Close() error {
	if err := a.store.Close(); err != nil {
		return fmt.Errorf("cerrar la base de datos: %w", err)
	}
	a.log.Info("base de datos cerrada")
	return nil
}
