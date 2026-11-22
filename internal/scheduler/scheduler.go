// Package scheduler ejecuta los checks periódicamente.
//
//   - Jitter inicial: cada check empieza en un instante aleatorio dentro de
//     su primer intervalo, para no lanzarlos todos a la vez.
//   - Concurrencia máxima configurable.
//   - Ningún check bloquea el planificador: cada ejecución tiene un tiempo
//     máximo y, si el check no lo respeta, se da por fallida y se abandona.
//   - Un pánico en un check se recupera, se registra y cuenta como fallo.
//   - Un check nunca se solapa consigo mismo.
//   - Si la hora del sistema no es válida, no se ejecuta nada.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/checks"
	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/state"
)

// DefaultConcurrency es la concurrencia máxima por defecto.
const DefaultConcurrency = 8

// DefaultGrace es el margen sobre el tiempo máximo del check antes de
// abandonar una ejecución que no respeta su contexto.
const DefaultGrace = 5 * time.Second

const (
	// maxTimeout es el tope absoluto de cualquier ejecución.
	maxTimeout = 60 * time.Second
	// editDelay es el retraso máximo con el que se ejecuta un check recién
	// creado o modificado.
	editDelay = 2 * time.Second
)

// ClockGuard indica si la hora del sistema es válida.
type ClockGuard interface{ Valid() bool }

// Recorder recibe los resultados.
type Recorder interface {
	Record(o state.Observation) state.Transition
}

// Lookup resuelve el implementador de un tipo de check.
type Lookup func(typ string) (checks.Checker, bool)

// RegistryLookup busca en el registro global de checks.
func RegistryLookup(typ string) (checks.Checker, bool) {
	e, ok := checks.Registry.Lookup(typ)
	return e.Impl, ok
}

// Options configura el planificador.
type Options struct {
	Log         *slog.Logger
	Recorder    Recorder
	Clock       ClockGuard
	Lookup      Lookup           // nil = RegistryLookup
	Concurrency int              // <= 0 = DefaultConcurrency
	Now         func() time.Time // nil = time.Now (inyectable en tests)
	Grace       time.Duration    // <= 0 = DefaultGrace
}

type job struct {
	check   model.Check
	next    time.Time
	running bool
	gen     int64 // se incrementa en cada cambio, para ignorar ejecuciones obsoletas
}

// Scheduler es el planificador de checks.
type Scheduler struct {
	log    *slog.Logger
	rec    Recorder
	clock  ClockGuard
	lookup Lookup
	now    func() time.Time
	grace  time.Duration
	sem    chan struct{}

	mu      sync.Mutex
	jobs    map[int64]*job
	unknown map[string]bool // tipos desconocidos ya avisados
	wake    chan struct{}
	wg      sync.WaitGroup
}

// New crea un planificador sin checks.
func New(o Options) *Scheduler {
	if o.Lookup == nil {
		o.Lookup = RegistryLookup
	}
	if o.Concurrency <= 0 {
		o.Concurrency = DefaultConcurrency
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Grace <= 0 {
		o.Grace = DefaultGrace
	}
	return &Scheduler{
		log: o.Log, rec: o.Recorder, clock: o.Clock, lookup: o.Lookup, now: o.Now, grace: o.Grace,
		sem:     make(chan struct{}, o.Concurrency),
		jobs:    map[int64]*job{},
		unknown: map[string]bool{},
		wake:    make(chan struct{}, 1),
	}
}

// Load añade los checks iniciales con jitter dentro de su primer intervalo.
func (s *Scheduler) Load(cs []model.Check) {
	now := s.now()
	s.mu.Lock()
	for _, c := range cs {
		s.jobs[c.ID] = &job{check: c, next: now.Add(jitter(c.Interval))}
	}
	s.mu.Unlock()
	s.signal()
}

// Set añade o actualiza un check. Se ejecuta en un par de segundos, para que
// el resultado de un cambio se vea enseguida.
func (s *Scheduler) Set(c model.Check) {
	s.mu.Lock()
	j, ok := s.jobs[c.ID]
	if !ok {
		j = &job{}
		s.jobs[c.ID] = j
	}
	j.check = c
	j.gen++
	j.next = s.now().Add(jitter(editDelay))
	s.mu.Unlock()
	s.signal()
}

// Trigger adelanta la siguiente ejecución de un check a ahora mismo (p. ej.
// al recibir el pulso de un heartbeat).
func (s *Scheduler) Trigger(id int64) {
	s.mu.Lock()
	if j, ok := s.jobs[id]; ok && !j.check.Paused {
		j.next = s.now()
	}
	s.mu.Unlock()
	s.signal()
}

// Remove quita un check. Si está en ejecución, su resultado se descarta.
func (s *Scheduler) Remove(id int64) {
	s.mu.Lock()
	delete(s.jobs, id)
	s.mu.Unlock()
	s.signal()
}

func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return rand.N(d)
}

// Run ejecuta el bucle del planificador hasta que ctx se cancela. Después
// espera a que terminen (o se abandonen) las ejecuciones en curso.
func (s *Scheduler) Run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		wait := s.dispatch(ctx)
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return
		case <-s.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

// dispatch lanza los checks vencidos y devuelve cuánto esperar hasta el
// siguiente.
func (s *Scheduler) dispatch(ctx context.Context) time.Duration {
	now := s.now()
	clockOK := s.clock == nil || s.clock.Valid()
	wait := time.Hour

	s.mu.Lock()
	defer s.mu.Unlock()
	for id, j := range s.jobs {
		if j.check.Paused || j.running {
			continue
		}
		if !j.next.After(now) {
			if !clockOK {
				// Sin hora válida no se ejecuta; se reintenta en un rato.
				j.next = now.Add(min(j.check.Interval, 10*time.Second))
			} else {
				checker, ok := s.lookup(j.check.Type)
				if !ok {
					if !s.unknown[j.check.Type] {
						s.unknown[j.check.Type] = true
						s.log.Warn("check type not available in this binary; skipping", "type", j.check.Type, "check", j.check.Name)
					}
					j.next = now.Add(j.check.Interval)
				} else {
					j.running = true
					j.next = now.Add(j.check.Interval)
					s.wg.Add(1)
					go s.execute(ctx, id, j.gen, j.check, checker)
				}
			}
		}
		if d := j.next.Sub(now); d < wait {
			wait = d
		}
	}
	if wait < 0 {
		wait = 0
	}
	return wait
}

func (s *Scheduler) execute(ctx context.Context, id, gen int64, c model.Check, checker checks.Checker) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		if j, ok := s.jobs[id]; ok {
			j.running = false
		}
		s.mu.Unlock()
		s.signal()
	}()

	// Concurrencia máxima.
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-s.sem }()

	start := s.now()
	r := s.runCheck(ctx, c, checker)
	if ctx.Err() != nil {
		return // parada en curso: el resultado no es fiable
	}

	s.mu.Lock()
	j, ok := s.jobs[id]
	current := ok && j.gen == gen
	s.mu.Unlock()
	if !current {
		return // el check se borró o se modificó mientras se ejecutaba
	}

	tr := s.rec.Record(state.Observation{
		CheckID: c.ID, At: start, OK: r.OK, Degraded: r.Degraded, NoPerm: r.NoPerm,
		Latency: r.Latency, Message: r.Message, CertExpiry: r.CertExpiry,
	})
	// En info solo se registran los cambios de estado, nunca cada resultado
	// (journald también escribe en disco).
	switch {
	case tr.Opened != nil:
		s.log.Warn("check down", "check", c.Name, "reason", r.Message.String())
	case tr.Closed != nil:
		s.log.Info("check recovered", "check", c.Name, "downtime", tr.Closed.Duration(start).Round(time.Second).String())
	case tr.Changed && tr.To == model.StatusNoPerm:
		s.log.Warn("check cannot run: missing permission", "check", c.Name, "reason", r.Message.String())
	}
}

// runCheck ejecuta el check con tiempo máximo y recuperación de pánicos.
func (s *Scheduler) runCheck(ctx context.Context, c model.Check, checker checks.Checker) checks.Result {
	timeout := checker.Timeout(c.Params)
	if timeout <= 0 || timeout > maxTimeout {
		timeout = maxTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan checks.Result, 1) // con hueco: la goroutine nunca se queda bloqueada
	go func() {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic in check", "check", c.Name, "type", c.Type, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
				done <- checks.Result{Message: msg.New(checks.MsgPanic)}
			}
		}()
		done <- checker.Check(cctx, cloneParams(c.Params))
	}()

	abandon := time.NewTimer(timeout + s.grace)
	defer abandon.Stop()
	select {
	case r := <-done:
		return r
	case <-abandon.C:
		s.log.Warn("check ignored its timeout and was abandoned", "check", c.Name, "type", c.Type)
		return checks.Result{Latency: timeout, Message: msg.New(checks.MsgTimeout)}
	case <-ctx.Done():
		// Parada: el check recibe la cancelación; se le da un margen para
		// terminar limpiamente antes de abandonarlo.
		t := time.NewTimer(s.grace)
		defer t.Stop()
		select {
		case r := <-done:
			return r
		case <-t.C:
			return checks.Result{Message: msg.New(checks.MsgTimeout)}
		}
	}
}

func cloneParams(p module.Values) module.Values {
	out := make(module.Values, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}
