// Package checks define la interfaz de los tipos de check y su registro.
// Cada tipo vive en su propio fichero (http.go, tcp.go…), se registra solo
// desde init() y declara sus campos con module.Info.
package checks

import (
	"context"
	"time"

	"qaphi/internal/module"
)

// Result es lo que devuelve una ejecución de un check.
type Result struct {
	OK      bool
	Latency time.Duration
	// Message resume el resultado («HTTP 200», «conexión rechazada»…). Se
	// muestra en la interfaz: nunca debe contener secretos.
	Message string
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
