package checks

import (
	"context"
	"crypto/rand"
	"maps"
	"regexp"
	"sync"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

func init() {
	msg.Register(map[string]msg.Text{
		"check.heartbeat.name": msg.Tr("Heartbeat", "Heartbeat", "Heartbeat", "Heartbeat"),
		"check.heartbeat.desc": msg.Tr(
			"Espera pulsos periódicos: un script o servicio llama a una URL y el check falla si deja de hacerlo.",
			"Waits for periodic pings: a script or service calls a URL and the check fails if it stops doing so.",
			"Wartet auf regelmäßige Pings: Ein Skript oder Dienst ruft eine URL auf; der Check schlägt fehl, wenn das ausbleibt.",
			"Attend des pings périodiques : un script ou un service appelle une URL et le check échoue s’il cesse de le faire."),
		"check.heartbeat.token": msg.Tr("Token", "Token", "Token", "Jeton"),
		"check.heartbeat.token.help": msg.Tr(
			"El script debe llamar (GET o POST) a /hb/<token> en esta misma dirección.",
			"The script must call (GET or POST) /hb/<token> on this same address.",
			"Das Skript muss /hb/<token> unter derselben Adresse aufrufen (GET oder POST).",
			"Le script doit appeler (GET ou POST) /hb/<token> à cette même adresse."),
		"check.heartbeat.period": msg.Tr("Periodo esperado", "Expected period", "Erwarteter Zeitraum", "Période attendue"),
		"check.heartbeat.period.help": msg.Tr(
			"Cada cuánto debe llegar un pulso.", "How often a ping must arrive.", "Wie oft ein Ping eintreffen muss.", "Fréquence à laquelle un ping doit arriver."),
		"check.heartbeat.grace":     msg.Tr("Margen", "Grace period", "Toleranz", "Marge"),
		"check.heartbeat.err.token": msg.Tr("Token no válido.", "Invalid token.", "Ungültiges Token.", "Jeton non valide."),
		"check.heartbeat.msg.last":  msg.Tr("último pulso hace %v", "last ping %v ago", "letzter Ping vor %v", "dernier ping il y a %v"),
		"check.heartbeat.msg.wait":  msg.Tr("esperando el primer pulso", "waiting for the first ping", "warte auf den ersten Ping", "en attente du premier ping"),
		"check.heartbeat.msg.late":  msg.Tr("sin pulsos desde hace %v", "no ping for %v", "seit %v kein Ping", "aucun ping depuis %v"),
		"check.heartbeat.msg.never": msg.Tr("no ha llegado ningún pulso", "no ping received", "kein Ping empfangen", "aucun ping reçu"),
	})
	Registry.Register(module.Info{
		ID:          "heartbeat",
		Name:        "check.heartbeat.name",
		Description: "check.heartbeat.desc",
		Order:       50,
		Fields: []module.Field{
			{Key: "token", Label: "check.heartbeat.token", Help: "check.heartbeat.token.help", Type: module.Text, Required: true, MaxLen: 64, ReadOnly: true},
			{Key: "period", Label: "check.heartbeat.period", Help: "check.heartbeat.period.help", Type: module.Duration, Default: "5m", Min: "10s", Max: "168h"},
			{Key: "grace", Label: "check.heartbeat.grace", Type: module.Duration, Default: "1m", Min: "1s", Max: "24h"},
		},
	}, heartbeatChecker{})
}

type heartbeatChecker struct{}

var tokenRe = regexp.MustCompile(`^[A-Z2-7]{26}$`)

// Prepare genera el token al crear el check.
func (heartbeatChecker) Prepare(p module.Values) module.Values {
	out := maps.Clone(p)
	if out == nil {
		out = module.Values{}
	}
	if out["token"] == "" {
		out["token"] = rand.Text()
	}
	return out
}

func (heartbeatChecker) Timeout(module.Values) time.Duration { return time.Second }

func (heartbeatChecker) Target(p module.Values) string { return "/hb/" + p.String("token") }

func (heartbeatChecker) Validate(p module.Values) module.Errors {
	if !tokenRe.MatchString(p.String("token")) {
		return module.Errors{"token": msg.New("check.heartbeat.err.token")}
	}
	return nil
}

func (heartbeatChecker) Check(_ context.Context, p module.Values) Result {
	now := Heartbeats.now()
	last := Heartbeats.Last(p.String("token"))
	limit := p.Duration("period") + p.Duration("grace")
	if last.IsZero() {
		// Sin pulsos desde que arrancó qaphi: se da un periodo completo de
		// margen antes de darlo por caído (los pulsos no se guardan en disco).
		if now.Sub(Heartbeats.Started()) > limit {
			return Result{Message: msg.New("check.heartbeat.msg.never")}
		}
		return Result{OK: true, Message: msg.New("check.heartbeat.msg.wait")}
	}
	age := now.Sub(last).Round(time.Second)
	if now.Sub(last) > limit {
		return Result{Message: msg.New("check.heartbeat.msg.late", age.String())}
	}
	return Result{OK: true, Message: msg.New("check.heartbeat.msg.last", age.String())}
}

// HeartbeatRegistry recibe los pulsos de /hb/<token>. Los pulsos solo viven en
// memoria: se escriben como resultados del check en cada comprobación.
type HeartbeatRegistry struct {
	mu      sync.RWMutex
	started time.Time
	now     func() time.Time
	valid   map[string]int64 // token → ID del check
	last    map[string]time.Time
}

// Heartbeats es el registro de pulsos del proceso.
var Heartbeats = NewHeartbeatRegistry(nil)

// NewHeartbeatRegistry crea un registro. now puede ser nil (time.Now).
func NewHeartbeatRegistry(now func() time.Time) *HeartbeatRegistry {
	if now == nil {
		now = time.Now
	}
	return &HeartbeatRegistry{started: now(), now: now, valid: map[string]int64{}, last: map[string]time.Time{}}
}

// Started devuelve cuándo empezó a recibir pulsos.
func (h *HeartbeatRegistry) Started() time.Time { return h.started }

// Sync fija los tokens válidos (token → ID del check).
func (h *HeartbeatRegistry) Sync(tokens map[string]int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.valid = maps.Clone(tokens)
	for t := range h.last {
		if _, ok := h.valid[t]; !ok {
			delete(h.last, t)
		}
	}
}

// Ping registra un pulso. Devuelve el ID del check, o false si el token no
// corresponde a ningún check.
func (h *HeartbeatRegistry) Ping(token string) (int64, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id, ok := h.valid[token]
	if !ok {
		return 0, false
	}
	h.last[token] = h.now()
	return id, true
}

// Last devuelve el último pulso recibido con ese token.
func (h *HeartbeatRegistry) Last(token string) time.Time {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.last[token]
}
