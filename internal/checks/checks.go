// Package checks define la interfaz de los tipos de check y su registro.
// Cada tipo vive en su propio fichero (http.go, tcp.go…), se registra solo
// desde init() y declara sus campos con module.Info.
package checks

import (
	"context"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

// Result es lo que devuelve una ejecución de un check.
type Result struct {
	OK      bool
	Latency time.Duration
	// Message resume el resultado («HTTP 200», «conexión rechazada»…) como
	// mensaje traducible. Se muestra en la interfaz: nunca debe contener
	// secretos.
	Message msg.Msg
}

// Checker es un tipo de check.
type Checker interface {
	// Check ejecuta la comprobación. Debe respetar ctx: cuando se cancela,
	// tiene que volver cuanto antes. Recibe los parámetros ya normalizados.
	Check(ctx context.Context, p module.Values) Result
	// Validate hace las comprobaciones que van más allá de cada campo por
	// separado (p. ej. que una URL sea http o https). Devuelve nil si todo es
	// correcto.
	Validate(p module.Values) module.Errors
	// Target devuelve el destino legible («https://…», «host:puerto»). Solo
	// se muestra con sesión iniciada.
	Target(p module.Values) string
	// Timeout devuelve el tiempo máximo configurado para el check.
	Timeout(p module.Values) time.Duration
}

// Registry contiene los tipos de check disponibles en este binario.
var Registry = module.NewRegistry[Checker]("check")

// Límites comunes del tiempo máximo de un check.
const (
	MinTimeout     = "1s"
	MaxTimeout     = "60s"
	DefaultTimeout = "10s"
)

// Claves de los mensajes comunes a todos los tipos de check.
const (
	MsgTimeout = "check.msg.timeout"
	MsgPanic   = "check.msg.panic"
)

func init() {
	msg.Register(map[string]msg.Text{
		"check.msg.timeout":     msg.Tr("tiempo agotado", "timed out", "Zeitüberschreitung", "délai dépassé"),
		"check.msg.refused":     msg.Tr("conexión rechazada", "connection refused", "Verbindung abgelehnt", "connexion refusée"),
		"check.msg.dns":         msg.Tr("nombre no resuelto", "name not resolved", "Name nicht aufgelöst", "nom non résolu"),
		"check.msg.unreachable": msg.Tr("red inaccesible", "network unreachable", "Netzwerk nicht erreichbar", "réseau inaccessible"),
		"check.msg.tls":         msg.Tr("error TLS: %v", "TLS error: %v", "TLS-Fehler: %v", "erreur TLS : %v"),
		"check.msg.tls_plain":   msg.Tr("el servidor no habla TLS", "the server does not speak TLS", "der Server spricht kein TLS", "le serveur ne parle pas TLS"),
		"check.msg.error":       msg.Tr("error: %v", "error: %v", "Fehler: %v", "erreur : %v"),
		"check.msg.panic":       msg.Tr("error interno del check", "internal check error", "interner Check-Fehler", "erreur interne du check"),
	})
}
