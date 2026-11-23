// Package model define los tipos de dominio compartidos entre el
// almacenamiento, la lógica y la web. No depende de ningún otro paquete de la
// aplicación salvo module (para los parámetros de los checks).
package model

import (
	"time"

	"github.com/future-chocolate6122/qaphi/internal/module"
)

// DefaultFailThreshold es el número de fallos consecutivos para dar un check
// por caído si no se indica otro.
const DefaultFailThreshold = 3

// Check es un servicio monitorizado.
type Check struct {
	ID       int64
	Name     string
	Type     string        // ID del módulo Checker ("http", "tcp"…)
	Group    string        // agrupación visual; vacío = sin grupo
	Interval time.Duration // cada cuánto se comprueba
	Paused   bool
	// Hidden oculta el check en la página pública (sin sesión).
	Hidden bool
	// FailThreshold es el número de fallos consecutivos para darlo por caído.
	FailThreshold int
	// DegradedAfter marca el check como lento si la respuesta tarda más
	// (0 = sin umbral).
	DegradedAfter time.Duration
	Params        module.Values // parámetros propios del tipo de check
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Threshold devuelve el umbral de fallos efectivo.
func (c Check) Threshold() int {
	if c.FailThreshold < 1 {
		return DefaultFailThreshold
	}
	return c.FailThreshold
}

// Result es el resultado de una ejecución de un check (un «pulso»).
type Result struct {
	CheckID int64
	At      time.Time
	OK      bool // resultado de la comprobación en sí
	// State es el estado del check tras este resultado (lo que se dibuja en
	// la barra de pulsos): up, degraded, pending, down o noperm.
	State   Status
	Latency time.Duration
	Message string // mensaje traducible codificado (msg.Msg.Encode)
}

// Hourly son los datos agregados de un check en una hora.
type Hourly struct {
	CheckID  int64
	Hour     time.Time // inicio de la hora (UTC)
	Total    int64     // pulsos contados (sin los de «sin permisos»)
	Down     int64     // pulsos con el check caído
	Degraded int64     // pulsos lentos
	LatCount int64     // pulsos con latencia (los correctos)
	LatSum   time.Duration
	LatMin   time.Duration
	LatMax   time.Duration
}

// Merge suma otra hora del mismo check.
func (h *Hourly) Merge(o Hourly) {
	h.Total += o.Total
	h.Down += o.Down
	h.Degraded += o.Degraded
	if o.LatCount > 0 {
		if h.LatCount == 0 || o.LatMin < h.LatMin {
			h.LatMin = o.LatMin
		}
		if o.LatMax > h.LatMax {
			h.LatMax = o.LatMax
		}
	}
	h.LatCount += o.LatCount
	h.LatSum += o.LatSum
}

// AvgLatency devuelve la latencia media (0 si no hay datos).
func (h Hourly) AvgLatency() time.Duration {
	if h.LatCount == 0 {
		return 0
	}
	return h.LatSum / time.Duration(h.LatCount)
}

// Bucket son los pulsos de un intervalo de tiempo (gráficas de 24 h).
type Bucket struct {
	Start    time.Time
	Total    int64
	Down     int64
	Degraded int64
	OK       int64         // pulsos correctos (los que tienen latencia)
	AvgLat   time.Duration // media de los correctos
	MaxLat   time.Duration
}

// Incident es una caída de un check: desde que se confirma hasta que se
// recupera.
type Incident struct {
	ID        int64
	CheckID   int64
	Start     time.Time // primer fallo de la racha
	End       time.Time // cero mientras sigue abierta
	LastError string    // mensaje traducible codificado
}

// Open indica si la incidencia sigue abierta.
func (i Incident) Open() bool { return i.End.IsZero() }

// Duration devuelve la duración (hasta now si sigue abierta).
func (i Incident) Duration(now time.Time) time.Duration {
	if i.Open() {
		return now.Sub(i.Start)
	}
	return i.End.Sub(i.Start)
}

// Session es una sesión iniciada del administrador. El token de la cookie no
// se guarda: solo su hash.
type Session struct {
	IDHash    []byte
	CSRF      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Status es el estado visible de un check (y de cada pulso).
type Status string

const (
	StatusUnknown  Status = "unknown"  // aún sin resultados
	StatusUp       Status = "up"       // operativo
	StatusDegraded Status = "degraded" // operativo pero lento (o con aviso, p. ej. certificado a punto de caducar)
	StatusPending  Status = "pending"  // falla, pero aún no se ha confirmado la caída
	StatusDown     Status = "down"     // caído (confirmado)
	StatusNoPerm   Status = "noperm"   // el sistema no permite hacer la comprobación (p. ej. ICMP)
	StatusPaused   Status = "paused"
)

// Códigos con los que se guarda el estado de cada pulso (estables).
var statusCodes = map[Status]int64{
	StatusDown: 0, StatusUp: 1, StatusPending: 2, StatusDegraded: 3, StatusNoPerm: 4, StatusUnknown: 9,
}

// Code devuelve el código numérico de un estado.
func (s Status) Code() int64 {
	if c, ok := statusCodes[s]; ok {
		return c
	}
	return statusCodes[StatusUnknown]
}

// StatusFromCode convierte un código guardado en estado.
func StatusFromCode(c int64) Status {
	for s, code := range statusCodes {
		if code == c {
			return s
		}
	}
	return StatusUnknown
}

// Operational indica si el estado cuenta como «funcionando».
func (s Status) Operational() bool {
	return s == StatusUp || s == StatusDegraded || s == StatusPending
}
