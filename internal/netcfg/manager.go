package netcfg

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

// ConfirmWindow es el tiempo para confirmar un cambio de red.
const ConfirmWindow = 2 * time.Minute

// Errores de Apply, Confirm y Cancel.
var (
	ErrLocked        = errors.New("network settings are fixed at startup (--listen)")
	ErrPending       = errors.New("a network change is already pending")
	ErrUnchanged     = errors.New("no network changes")
	ErrNoPending     = errors.New("no pending network change")
	ErrWrongEndpoint = errors.New("the confirmation must come from the new address")
)

type ctxKey struct{}

// EndpointID devuelve el identificador del punto de escucha por el que llegó
// la petición (0 si no se sabe).
func EndpointID(ctx context.Context) uint64 {
	id, _ := ctx.Value(ctxKey{}).(uint64)
	return id
}

// Options configura el gestor.
type Options struct {
	Log     *slog.Logger
	Handler http.Handler
	Store   store.Store
	Certs   *CertStore
	// Window es el plazo para confirmar un cambio (0 = ConfirmWindow).
	Window time.Duration
	// SwapDelay es la espera antes de cerrar un punto de escucha, para que la
	// respuesta en curso llegue al navegador (0 = 500 ms).
	SwapDelay time.Duration
}

// endpoint es un servidor HTTP escuchando en una dirección.
type endpoint struct {
	id   uint64
	cfg  Config
	addr string // dirección real (con el puerto efectivo)
	srv  *http.Server
	done chan struct{}
}

// pending es un cambio a la espera de confirmación.
type pending struct {
	cfg      Config
	ep       *endpoint // nil mientras se hace un cambio en el sitio
	old      *endpoint // con superposición: el anterior, que sigue sirviendo
	oldCfg   Config
	inPlace  bool // mismo puerto: el anterior se cierra antes de abrir el nuevo
	deadline time.Time
	timer    *time.Timer
}

// Manager mantiene el servidor HTTP en la dirección configurada y aplica los
// cambios de red en caliente:
//
//   - Si la dirección nueva usa otro puerto, se abre junto a la actual y las
//     dos conviven hasta que se confirma desde la nueva (entonces se cierra la
//     anterior) o vence el plazo (entonces se cierra la nueva).
//   - Si usa el mismo puerto (p. ej. solo cambia el protocolo), se cierra la
//     actual y se abre la nueva en su lugar; si no se confirma a tiempo, se
//     vuelve a la anterior.
//
// La configuración solo se guarda al confirmar: si el proceso se reinicia con
// un cambio pendiente, arranca con la configuración anterior.
type Manager struct {
	o      Options
	nextID atomic.Uint64

	mu       sync.Mutex
	cur      *endpoint
	pend     *pending
	retiring map[*endpoint]bool // puntos que se están cerrando en segundo plano
	locked   bool
	reverted bool
	closed   bool
}

// NewManager crea el gestor.
func NewManager(o Options) *Manager {
	if o.Window <= 0 {
		o.Window = ConfirmWindow
	}
	if o.SwapDelay <= 0 {
		o.SwapDelay = 500 * time.Millisecond
	}
	return &Manager{o: o}
}

// SetHandler fija el manejador HTTP. Debe llamarse antes de Start (permite
// crear el gestor antes que la web, que a su vez lo necesita).
func (m *Manager) SetHandler(h http.Handler) { m.o.Handler = h }

// SetTimings ajusta el plazo de confirmación y la espera antes de cerrar un
// punto de escucha (0 = sin cambios). Debe llamarse antes de Start.
func (m *Manager) SetTimings(window, swapDelay time.Duration) {
	if window > 0 {
		m.o.Window = window
	}
	if swapDelay > 0 {
		m.o.SwapDelay = swapDelay
	}
}

// Start empieza a servir. Si ln no es nil, se usa ese listener (dirección
// fijada al arrancar con --listen, o tests) y la configuración queda
// bloqueada: no se puede cambiar desde la web.
func (m *Manager) Start(ctx context.Context, cfg Config, ln net.Listener) error {
	locked := ln != nil
	if ln == nil {
		if cfg.TLS {
			if err := m.o.Certs.Ensure(ctx); err != nil {
				return err
			}
		}
		var err error
		if ln, err = listenRetry(cfg); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locked = locked
	m.cur = m.serve(cfg, ln)
	return nil
}

func (m *Manager) serve(cfg Config, ln net.Listener) *endpoint {
	id := m.nextID.Add(1)
	srv := &http.Server{
		Handler:           m.o.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          log.New(httpErrorWriter{m.o.Log}, "", 0),
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			return context.WithValue(ctx, ctxKey{}, id)
		},
	}
	if cfg.TLS {
		srv.TLSConfig = &tls.Config{
			GetCertificate: m.o.Certs.GetCertificate,
			MinVersion:     tls.VersionTLS12,
			NextProtos:     []string{"h2", "http/1.1"},
		}
		ln = tls.NewListener(ln, srv.TLSConfig)
	}
	ep := &endpoint{id: id, cfg: cfg, addr: ln.Addr().String(), srv: srv, done: make(chan struct{})}
	go func() {
		defer close(ep.done)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.o.Log.Error("http server stopped", "listen", ep.addr, "error", err)
		}
	}()
	m.o.Log.Info("listening", "listen", ep.addr, "scheme", cfg.Scheme())
	return ep
}

// stop cierra un punto de escucha de forma ordenada. No debe llamarse con el
// cerrojo tomado: espera a que terminen las peticiones en curso.
func (m *Manager) stop(ep *endpoint) {
	if ep == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ep.srv.Shutdown(ctx); err != nil {
		ep.srv.Close()
	}
	<-ep.done
	m.o.Log.Info("stopped listening", "listen", ep.addr, "scheme", ep.cfg.Scheme())
}

// listenRetry abre la dirección, reintentando brevemente por si el puerto
// acaba de liberarse.
func listenRetry(cfg Config) (net.Listener, error) {
	var err error
	for i := 0; i < 10; i++ {
		var ln net.Listener
		if ln, err = net.Listen("tcp", cfg.Addr()); err == nil {
			return ln, nil
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, openError(cfg, err)
}

func openError(cfg Config, err error) error {
	if errors.Is(err, syscall.EADDRINUSE) {
		return &Error{Msg: msg.New("settings.net.err.inuse", cfg.Addr()), Err: err}
	}
	var opErr *net.OpError
	detail := err.Error()
	if errors.As(err, &opErr) && opErr.Err != nil {
		detail = opErr.Err.Error()
	}
	return &Error{Msg: msg.New("settings.net.err.open", cfg.Addr(), detail), Err: err}
}

// Current devuelve la configuración en uso.
func (m *Manager) Current() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pend != nil {
		return m.pend.oldCfg
	}
	if m.cur == nil {
		return Config{}
	}
	return m.cur.cfg
}

// Addr devuelve la dirección real en la que se escucha (con el puerto
// efectivo).
func (m *Manager) Addr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return ""
	}
	return m.cur.addr
}

// Locked indica si la dirección se fijó al arrancar y no se puede cambiar.
func (m *Manager) Locked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.locked
}

// Pending devuelve el cambio pendiente de confirmar, si lo hay.
func (m *Manager) Pending() (cfg Config, deadline time.Time, inPlace, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pend == nil {
		return Config{}, time.Time{}, false, false
	}
	return m.pend.cfg, m.pend.deadline, m.pend.inPlace, true
}

// TakeReverted indica (una sola vez) si el último cambio se deshizo por no
// confirmarse a tiempo.
func (m *Manager) TakeReverted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.reverted
	m.reverted = false
	return r
}

// Apply pone en marcha una configuración nueva a la espera de confirmación.
// inPlace indica que se usa el mismo puerto, así que la dirección actual
// dejará de responder en cuanto se aplique.
func (m *Manager) Apply(ctx context.Context, cfg Config) (inPlace bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.closed:
		return false, errors.New("shutting down")
	case m.locked:
		return false, ErrLocked
	case m.pend != nil:
		return false, ErrPending
	case m.cur != nil && cfg == m.cur.cfg:
		return false, ErrUnchanged
	}
	if cfg.TLS {
		if err := m.o.Certs.Ensure(ctx); err != nil {
			return false, err
		}
	}
	cur := m.cur
	p := &pending{cfg: cfg, oldCfg: cur.cfg, deadline: time.Now().Add(m.o.Window)}
	ln, err := net.Listen("tcp", cfg.Addr())
	switch {
	case err == nil:
		p.old = cur
		p.ep = m.serve(cfg, ln)
	case errors.Is(err, syscall.EADDRINUSE) && cfg.Port == cur.cfg.Port:
		// El puerto lo tenemos nosotros: cambio en el sitio.
		p.inPlace = true
		go m.swapInPlace(p, cur)
	default:
		return false, openError(cfg, err)
	}
	p.timer = time.AfterFunc(m.o.Window, func() { m.revert(p, true) })
	m.pend = p
	m.o.Log.Info("network change pending confirmation", "listen", cfg.Addr(), "scheme", cfg.Scheme(),
		"in_place", p.inPlace, "deadline", p.deadline.Format(time.RFC3339))
	return p.inPlace, nil
}

// swapInPlace cierra la dirección actual y abre la nueva en el mismo puerto.
// Si la nueva no se puede abrir, vuelve a abrir la anterior.
func (m *Manager) swapInPlace(p *pending, old *endpoint) {
	time.Sleep(m.o.SwapDelay)
	m.stop(old)
	ln, err := listenRetry(p.cfg)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		if ln != nil {
			ln.Close()
		}
		return
	}
	if err != nil {
		m.o.Log.Error("could not open the new address; restoring the previous one", "listen", p.cfg.Addr(), "error", err)
		if p.timer != nil {
			p.timer.Stop()
		}
		if m.pend == p {
			m.pend = nil
		}
		m.reverted = true
		if ln2, err2 := listenRetry(p.oldCfg); err2 == nil {
			m.cur = m.serve(p.oldCfg, ln2)
		} else {
			m.o.Log.Error("could not restore the previous address; restart qaphi or use --listen", "listen", p.oldCfg.Addr(), "error", err2)
		}
		return
	}
	p.ep = m.serve(p.cfg, ln)
	m.cur = p.ep
}

// Confirm guarda la configuración pendiente. Solo se acepta si la petición
// llegó por la dirección nueva: así se comprueba que es alcanzable.
func (m *Manager) Confirm(r *http.Request) error {
	m.mu.Lock()
	p := m.pend
	if p == nil {
		m.mu.Unlock()
		return ErrNoPending
	}
	if p.ep == nil || EndpointID(r.Context()) != p.ep.id {
		m.mu.Unlock()
		return ErrWrongEndpoint
	}
	if err := Save(r.Context(), m.o.Store, p.cfg); err != nil {
		m.mu.Unlock()
		return err
	}
	p.timer.Stop()
	m.pend = nil
	m.cur = p.ep
	old := p.old
	if old != nil {
		m.retire(old)
	}
	m.mu.Unlock()

	m.o.Log.Info("network settings confirmed", "listen", p.cfg.Addr(), "scheme", p.cfg.Scheme())
	if old != nil {
		go m.stopRetired(old)
	}
	return nil
}

// Cancel deshace el cambio pendiente.
func (m *Manager) Cancel() error {
	m.mu.Lock()
	p := m.pend
	m.mu.Unlock()
	if p == nil {
		return ErrNoPending
	}
	m.revert(p, false)
	return nil
}

// revert deshace un cambio pendiente (al vencer el plazo o al cancelarlo).
// Las operaciones con los puntos de escucha se hacen en segundo plano, tras
// una breve espera, porque la petición que lo pide puede llegar por el punto
// que se va a cerrar.
func (m *Manager) revert(p *pending, timeout bool) {
	m.mu.Lock()
	if m.pend != p || m.closed {
		m.mu.Unlock()
		return
	}
	if p.inPlace && p.ep == nil {
		// El cambio en el sitio aún no ha terminado: se reintenta en breve.
		p.timer = time.AfterFunc(100*time.Millisecond, func() { m.revert(p, timeout) })
		m.mu.Unlock()
		return
	}
	p.timer.Stop()
	m.pend = nil
	m.retire(p.ep)
	if timeout {
		m.reverted = true
		m.o.Log.Warn("network change not confirmed in time; restoring the previous settings",
			"listen", p.oldCfg.Addr(), "scheme", p.oldCfg.Scheme())
	} else {
		m.o.Log.Info("network change cancelled", "listen", p.oldCfg.Addr(), "scheme", p.oldCfg.Scheme())
	}
	m.mu.Unlock()

	go func() {
		m.stopRetired(p.ep)
		if !p.inPlace {
			return // la anterior siguió sirviendo todo el tiempo
		}
		ln, err := listenRetry(p.oldCfg)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed {
			if ln != nil {
				ln.Close()
			}
			return
		}
		if err != nil {
			m.o.Log.Error("could not restore the previous address; restart qaphi or use --listen", "listen", p.oldCfg.Addr(), "error", err)
			return
		}
		m.cur = m.serve(p.oldCfg, ln)
	}()
}

// retire marca un punto de escucha que se va a cerrar en segundo plano, para
// que Shutdown lo espere. Se llama con el cerrojo tomado.
func (m *Manager) retire(ep *endpoint) {
	if ep == nil {
		return
	}
	if m.retiring == nil {
		m.retiring = map[*endpoint]bool{}
	}
	m.retiring[ep] = true
}

// stopRetired cierra, tras la espera configurada, un punto marcado con retire.
func (m *Manager) stopRetired(ep *endpoint) {
	time.Sleep(m.o.SwapDelay)
	m.stop(ep)
	m.mu.Lock()
	delete(m.retiring, ep)
	m.mu.Unlock()
}

// httpErrorWriter recibe los errores internos de net/http. Los fallos del
// handshake TLS son normales con un certificado autofirmado (cada navegador
// lo rechaza hasta que se acepta la excepción), así que van a debug para no
// llenar el journal; el resto, a warn.
type httpErrorWriter struct{ log *slog.Logger }

func (w httpErrorWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if strings.Contains(line, "TLS handshake error") {
		w.log.Debug("http server", "detail", line)
	} else {
		w.log.Warn("http server", "detail", line)
	}
	return len(p), nil
}

// Shutdown cierra todos los puntos de escucha de forma ordenada. Un cambio
// pendiente se descarta (no se había guardado).
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.closed = true
	eps := []*endpoint{m.cur}
	for ep := range m.retiring {
		eps = append(eps, ep)
	}
	if p := m.pend; p != nil {
		if p.timer != nil {
			p.timer.Stop()
		}
		eps = append(eps, p.ep, p.old)
		m.pend = nil
	}
	m.mu.Unlock()

	seen := map[*endpoint]bool{}
	var wg sync.WaitGroup
	for _, ep := range eps {
		if ep == nil || seen[ep] {
			continue
		}
		seen[ep] = true
		wg.Add(1)
		go func(ep *endpoint) {
			defer wg.Done()
			m.stop(ep)
		}(ep)
	}
	wg.Wait()
}
