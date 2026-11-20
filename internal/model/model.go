// Package model define los tipos de dominio compartidos entre el
// almacenamiento, la lógica y la web. No depende de ningún otro paquete de la
// aplicación salvo module (para los parámetros de los checks).
package model

import (
	"time"

	"qaphi/internal/module"
)

// Check es un servicio monitorizado.
type Check struct {
	ID        int64
	Name      string
	Type      string        // ID del módulo Checker ("http", "tcp"…)
	Group     string        // agrupación visual; vacío = sin grupo
	Interval  time.Duration // cada cuánto se comprueba
	Paused    bool
	Params    module.Values // parámetros propios del tipo de check
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Result es el resultado de una ejecución de un check.
type Result struct {
	CheckID int64
	At      time.Time
	OK      bool
	Latency time.Duration
	Message string // código HTTP, error… breve y sin secretos
}

// Session es una sesión iniciada del administrador. El token de la cookie no
// se guarda: solo su hash.
type Session struct {
	IDHash    []byte
	CSRF      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Status es el estado visible de un check.
type Status string

const (
	StatusUnknown Status = "unknown" // aún sin resultados
	StatusUp      Status = "up"
	StatusDown    Status = "down"
	StatusPaused  Status = "paused"
)
