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
	"strconv"
	"sync"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/auth"
	_ "github.com/future-chocolate6122/qaphi/internal/checks" // registra los tipos de check
	"github.com/future-chocolate6122/qaphi/internal/clock"
	"github.com/future-chocolate6122/qaphi/internal/config"
	"github.com/future-chocolate6122/qaphi/internal/scheduler"
	"github.com/future-chocolate6122/qaphi/internal/state"
	"github.com/future-chocolate6122/qaphi/internal/store/sqlite"
	"github.com/future-chocolate6122/qaphi/internal/version"
	"github.com/future-chocolate6122/qaphi/internal/web"
)

// DBFileName es el nombre del fichero de base de datos dentro del directorio
// de datos.
const DBFileName = "qaphi.db"

// SettingConcurrency es el ajuste de concurrencia máxima del planificador.
const SettingConcurrency = "scheduler.concurrency"

// shutdownTimeout es lo que se espera a que terminen las peticiones en curso
// durante la parada.
const shutdownTimeout = 10 * time.Second

// sessionCleanup es cada cuánto se borran las sesiones caducadas.
const sessionCleanup = time.Hour

// App es una instancia de Qaphi en marcha.
type App struct {
	cfg      *config.Config
	log      *slog.Logger
	store    *sqlite.Store
	sessions *auth.Sessions
	tracker  *state.Tracker
	sched    *scheduler.Scheduler
	web      *web.Server
	token    *auth.SetupToken
}

// New prepara el directorio de datos, abre la base de datos (aplicando las
// migraciones), restaura el estado y construye el servidor web. No empieza a
// escuchar ni a ejecutar checks.
func New(ctx context.Context, cfg *config.Config, log *slog.Logger) (_ *App, err error) {
	if err := prepareDataDir(cfg.DataDir, log); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(cfg.DataDir, DBFileName)
	st, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("base de datos: %w", err)
	}
	defer func() {
		if err != nil {
			st.Close()
		}
	}()
	schema, err := st.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	log.Info("base de datos lista", "ruta", dbPath, "esquema", schema)

	admin, err := auth.NewAdmin(ctx, st)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, log: log, store: st, sessions: auth.NewSessions(st, nil)}

	// Estado: checks configurados y último resultado de cada uno.
	checks, err := st.ListChecks(ctx)
	if err != nil {
		return nil, err
	}
	last, err := st.LastResults(ctx)
	if err != nil {
		return nil, err
	}
	a.tracker = state.New(st, log)
	a.tracker.Restore(checks, last)

	a.sched = scheduler.New(scheduler.Options{
		Log:         log,
		Recorder:    a.tracker,
		Clock:       clock.NewGuard(log, clock.MinValid(), nil),
		Concurrency: concurrency(ctx, st),
	})
	a.sched.Load(checks)
	log.Info("checks cargados", "cantidad", len(checks))

	if !admin.HasPassword() {
		a.token = auth.NewSetupToken()
	}
	a.web, err = web.New(web.Deps{
		Log: log, Store: st, Admin: admin, Sessions: a.sessions, SetupToken: a.token,
		Limiter: auth.DefaultLimiter(), Tracker: a.tracker, Scheduler: a.sched,
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

func concurrency(ctx context.Context, st *sqlite.Store) int {
	if v, ok, err := st.GetSetting(ctx, SettingConcurrency); err == nil && ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 64 {
			return n
		}
	}
	return scheduler.DefaultConcurrency
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
	if fi.Mode().Perm()&0o077 != 0 {
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

// Serve arranca el planificador y el volcado periódico, sirve peticiones en
// ln hasta que ctx se cancela y después detiene todo en orden: servidor HTTP,
// planificador (esperando a los checks en curso) y volcado final del buffer.
// No cierra la base de datos: eso lo hace Close.
func (a *App) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           a.web,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(a.log.Handler(), slog.LevelWarn),
	}

	// Contexto de los trabajos en segundo plano: se cancela después de
	// detener el servidor HTTP, no a la vez.
	bgCtx, stopBg := context.WithCancel(context.Background())
	defer stopBg()
	var bg sync.WaitGroup
	bg.Add(3)
	go func() { defer bg.Done(); a.sched.Run(bgCtx) }()
	go func() { defer bg.Done(); a.tracker.RunFlusher(bgCtx, a.cfg.FlushInterval) }()
	go func() { defer bg.Done(); a.cleanSessions(bgCtx) }()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	a.log.Info("qaphi en marcha", "versión", version.Get().Version, "escucha", ln.Addr().String(),
		"datos", a.cfg.DataDir, "volcado", a.cfg.FlushInterval.String())
	if a.token != nil {
		a.log.Warn("configuración inicial pendiente: abre /setup en el navegador e introduce este token",
			"token", a.token.String(), "url", setupURLs(ln.Addr()))
	}

	var serveErr error
	select {
	case err := <-errc:
		serveErr = fmt.Errorf("servidor HTTP: %w", err) // terminó por sí solo: es un error
	case <-ctx.Done():
		a.log.Info("deteniendo el servidor HTTP")
		shCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := srv.Shutdown(shCtx); err != nil {
			a.log.Warn("parada del servidor HTTP incompleta; se cierran las conexiones restantes", "error", err)
			srv.Close()
		}
		cancel()
		if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr = fmt.Errorf("servidor HTTP: %w", err)
		}
	}

	a.log.Info("deteniendo el planificador")
	stopBg()
	bg.Wait()
	flushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n := a.tracker.Pending()
	if err := a.tracker.Flush(flushCtx); err != nil {
		a.log.Error("no se pudo volcar el buffer en la parada", "error", err, "pendientes", n)
		return errors.Join(serveErr, err)
	}
	a.log.Info("buffer volcado", "resultados", n)
	return serveErr
}

func (a *App) cleanSessions(ctx context.Context) {
	t := time.NewTicker(sessionCleanup)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := a.sessions.Cleanup(ctx); err != nil && ctx.Err() == nil {
				a.log.Warn("no se pudieron borrar las sesiones caducadas", "error", err)
			} else if n > 0 {
				a.log.Debug("sesiones caducadas borradas", "cantidad", n)
			}
		}
	}
}

// setupURLs sugiere las URL de /setup a partir de la dirección de escucha.
func setupURLs(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return "/setup"
	}
	port := strconv.Itoa(tcp.Port)
	if !tcp.IP.IsUnspecified() {
		return "http://" + net.JoinHostPort(tcp.IP.String(), port) + "/setup"
	}
	var out string
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			if out != "" {
				out += " "
			}
			out += "http://" + net.JoinHostPort(ipn.IP.String(), port) + "/setup"
		}
	}
	if out == "" {
		out = "http://<ip-del-equipo>:" + port + "/setup"
	}
	return out
}

// Close libera los recursos. Debe llamarse después de Serve.
func (a *App) Close() error {
	if err := a.store.Close(); err != nil {
		return fmt.Errorf("cerrar la base de datos: %w", err)
	}
	a.log.Info("base de datos cerrada")
	return nil
}
