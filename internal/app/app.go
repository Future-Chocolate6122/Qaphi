// Package app ensambla los componentes de Qaphi y gestiona su ciclo de vida:
// arranque, servicio y parada ordenada.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/auth"
	_ "github.com/future-chocolate6122/qaphi/internal/checks" // registra los tipos de check
	"github.com/future-chocolate6122/qaphi/internal/clock"
	"github.com/future-chocolate6122/qaphi/internal/config"
	"github.com/future-chocolate6122/qaphi/internal/netcfg"
	"github.com/future-chocolate6122/qaphi/internal/scheduler"
	"github.com/future-chocolate6122/qaphi/internal/settings"
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
	net      *netcfg.Manager
	netCfg   netcfg.Config // configuración de red guardada
	settings *settings.Service
	token    *auth.SetupToken

	// NetWindow y NetSwapDelay permiten acortar los plazos del cambio de red
	// en los tests (0 = valores por defecto).
	NetWindow    time.Duration
	NetSwapDelay time.Duration
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
		return nil, fmt.Errorf("database: %w", err)
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
	log.Info("database ready", "path", dbPath, "schema", schema)

	admin, err := auth.NewAdmin(ctx, st)
	if err != nil {
		return nil, err
	}
	ui, err := settings.Load(ctx, st)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, log: log, store: st, sessions: auth.NewSessions(st, nil)}

	// Red. Una instalación que ya tenía contraseña (v0.1.0) y nunca guardó
	// la configuración de red conserva su puerto 8091.
	_, hasPort, err := st.GetSetting(ctx, netcfg.KeyPort)
	if err != nil {
		return nil, err
	}
	legacy := admin.HasPassword() && !hasPort
	if a.netCfg, err = netcfg.Load(ctx, st, legacy); err != nil {
		return nil, err
	}
	if legacy {
		if err := netcfg.Save(ctx, st, a.netCfg); err != nil {
			return nil, err
		}
		log.Info("existing installation: keeping port", "port", a.netCfg.Port)
	}
	certs, err := netcfg.LoadCertStore(ctx, st)
	if err != nil {
		return nil, err
	}

	// Estado: checks configurados y último resultado de cada uno.
	checks, err := st.ListChecks(ctx)
	if err != nil {
		return nil, err
	}
	recent, err := st.RecentResults(ctx, state.RecentBeats)
	if err != nil {
		return nil, err
	}
	open, err := st.OpenIncidents(ctx)
	if err != nil {
		return nil, err
	}
	a.tracker = state.New(st, log, nil)
	a.tracker.Restore(checks, recent, open)
	a.tracker.SetAggregatesOnly(ui.Data().AggregatesOnly)
	if err := a.tracker.RefreshUptime(ctx); err != nil {
		return nil, err
	}
	if err := web.SyncHeartbeats(ctx, st); err != nil {
		return nil, err
	}
	a.settings = ui

	a.sched = scheduler.New(scheduler.Options{
		Log:         log,
		Recorder:    a.tracker,
		Clock:       clock.NewGuard(log, clock.MinValid(), nil),
		Concurrency: concurrency(ctx, st),
	})
	a.sched.Load(checks)
	log.Info("checks loaded", "count", len(checks))

	if !admin.HasPassword() {
		a.token = auth.NewSetupToken()
	}
	// El gestor de red se crea antes que la web (que lo necesita) y recibe
	// su manejador después.
	a.net = netcfg.NewManager(netcfg.Options{Log: log, Store: st, Certs: certs})
	w, err := web.New(web.Deps{
		Log: log, Store: st, Settings: ui, Admin: admin, Sessions: a.sessions, SetupToken: a.token,
		Limiter: auth.DefaultLimiter(), Tracker: a.tracker, Scheduler: a.sched,
		Network: a.net, Certs: certs,
	})
	if err != nil {
		return nil, err
	}
	a.net.SetHandler(w)
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
		return fmt.Errorf("create data directory %s: %w", dir, err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("data directory %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		log.Warn("the data directory is accessible to other users; chmod 700 is recommended",
			"path", dir, "mode", fmt.Sprintf("%04o", fi.Mode().Perm()))
	}
	return nil
}

// Run sirve hasta que ctx se cancela. Si se fijó --listen, se usa esa
// dirección (HTTP) y la configuración de red de la web queda bloqueada.
func (a *App) Run(ctx context.Context) error {
	if a.cfg.Listen == "" {
		return a.Serve(ctx, nil)
	}
	ln, err := net.Listen("tcp", a.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", a.cfg.Listen, err)
	}
	a.log.Warn("listen address fixed at startup (--listen / QAPHI_LISTEN); web network settings are disabled", "listen", a.cfg.Listen)
	return a.Serve(ctx, ln)
}

// Serve arranca el planificador y el volcado periódico, sirve peticiones
// hasta que ctx se cancela y después detiene todo en orden: servidor HTTP,
// planificador (esperando a los checks en curso) y volcado final del buffer.
// Si ln no es nil, se sirve en él (dirección fijada); si es nil, en la
// configuración de red guardada. No cierra la base de datos: eso lo hace
// Close.
func (a *App) Serve(ctx context.Context, ln net.Listener) error {
	a.net.SetTimings(a.NetWindow, a.NetSwapDelay)
	start := a.netCfg
	if ln != nil {
		start = netcfg.Config{Port: ln.Addr().(*net.TCPAddr).Port}
		if host, _, err := net.SplitHostPort(ln.Addr().String()); err == nil {
			start.Bind = host
		}
	}
	if err := a.net.Start(ctx, start, ln); err != nil {
		return fmt.Errorf("http server: %w", err)
	}

	// Trabajos en segundo plano: se detienen después del servidor HTTP.
	bgCtx, stopBg := context.WithCancel(context.Background())
	defer stopBg()
	var bg sync.WaitGroup
	bg.Add(4)
	go func() { defer bg.Done(); a.sched.Run(bgCtx) }()
	go func() { defer bg.Done(); a.tracker.RunFlusher(bgCtx, a.cfg.FlushInterval) }()
	go func() { defer bg.Done(); a.cleanSessions(bgCtx) }()
	go func() { defer bg.Done(); a.runRetention(bgCtx) }()

	scheme := a.net.Current().Scheme()
	if ln != nil {
		scheme = "http"
	}
	a.log.Info("qaphi started", "version", version.Get().Version, "listen", a.net.Addr(), "scheme", scheme,
		"data", a.cfg.DataDir, "flush", a.cfg.FlushInterval.String())
	if a.token != nil {
		a.log.Warn("initial setup pending: open /setup in a browser and enter this token",
			"token", a.token.String(), "url", setupURLs(scheme, a.net.Addr()))
	}

	<-ctx.Done()
	a.log.Info("stopping the http server")
	a.net.Shutdown()

	a.log.Info("stopping the scheduler")
	stopBg()
	bg.Wait()
	flushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n := a.tracker.Pending()
	if err := a.tracker.Flush(flushCtx); err != nil {
		a.log.Error("could not flush the buffer on shutdown", "error", err, "pending", n)
		return err
	}
	a.log.Info("buffer flushed", "results", n)
	return nil
}

// runRetention borra cada hora los datos que superan la retención
// configurada: resultados en bruto, agregados por hora e incidencias
// cerradas.
func (a *App) runRetention(ctx context.Context) {
	a.cleanData(ctx)
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.cleanData(ctx)
		}
	}
}

func (a *App) cleanData(ctx context.Context) {
	d := a.settings.Data()
	now := time.Now()
	raw, err1 := a.store.DeleteResultsBefore(ctx, now.Add(-time.Duration(d.RawHours)*time.Hour))
	keep := now.AddDate(0, 0, -d.AggregateDays)
	hourly, err2 := a.store.DeleteHourlyBefore(ctx, keep)
	inc, err3 := a.store.DeleteIncidentsBefore(ctx, keep)
	if err := errors.Join(err1, err2, err3); err != nil {
		if ctx.Err() == nil {
			a.log.Warn("retention cleanup failed", "error", err)
		}
		return
	}
	if raw+hourly+inc > 0 {
		a.log.Info("old data deleted", "results", raw, "hourly", hourly, "incidents", inc)
	}
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
				a.log.Warn("could not delete expired sessions", "error", err)
			} else if n > 0 {
				a.log.Debug("expired sessions deleted", "count", n)
			}
		}
	}
}

// setupURLs sugiere las URL de /setup a partir de la dirección de escucha.
func setupURLs(scheme, addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "/setup"
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		return scheme + "://" + net.JoinHostPort(host, port) + "/setup"
	}
	var out string
	for _, ip := range netcfg.LocalIPs() {
		if net.ParseIP(ip).To4() == nil {
			continue
		}
		if out != "" {
			out += " "
		}
		out += scheme + "://" + net.JoinHostPort(ip, port) + "/setup"
	}
	if out == "" {
		out = scheme + "://<machine-ip>:" + port + "/setup"
	}
	return out
}

// Close libera los recursos. Debe llamarse después de Serve.
func (a *App) Close() error {
	if err := a.store.Close(); err != nil {
		return fmt.Errorf("close the database: %w", err)
	}
	a.log.Info("database closed")
	return nil
}
