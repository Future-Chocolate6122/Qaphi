// Package msg centraliza los textos de la interfaz para facilitar una
// traducción futura. Los textos generales están en este fichero; cada módulo
// (check, aviso, componente) registra los suyos con Register desde su propio
// fichero, de modo que añadir o quitar un módulo no obliga a tocar este.
//
// Los mensajes de log no pasan por aquí: van dirigidos al administrador del
// sistema y se escriben directamente en el código.
package msg

import (
	"fmt"
	"sync"
)

var mu sync.RWMutex

// es contiene los textos en castellano, indexados por clave estable.
var es = map[string]string{
	// Generales
	"app.name":    "Qaphi",
	"app.tagline": "Monitor de servicios",

	// Página provisional de la fase 0
	"index.title":   "Qaphi en marcha",
	"index.running": "El servidor está funcionando correctamente.",
	"index.pending": "La configuración inicial y la página de estado estarán disponibles en próximas versiones.",
	"index.version": "Versión",

	// Errores HTTP
	"http.internal": "Error interno del servidor",

	// Validación de campos de módulos
	"field.required":     "Este campo es obligatorio.",
	"field.too_long":     "Admite como máximo %d caracteres.",
	"field.int":          "Debe ser un número entero.",
	"field.int_min":      "Debe ser como mínimo %s.",
	"field.int_max":      "Debe ser como máximo %s.",
	"field.duration":     "Duración no válida (ejemplos: 30s, 5m, 1h).",
	"field.duration_min": "Debe ser como mínimo %s.",
	"field.duration_max": "Debe ser como máximo %s.",
	"field.bool":         "Valor no válido.",
	"field.select":       "Opción no válida.",
}

// T devuelve el texto asociado a key. Si no existe, devuelve la propia clave
// para que la ausencia sea visible sin romper la página.
func T(key string) string {
	mu.RLock()
	s, ok := es[key]
	mu.RUnlock()
	if ok {
		return s
	}
	return key
}

// F formatea el texto asociado a key con fmt.Sprintf.
func F(key string, args ...any) string {
	return fmt.Sprintf(T(key), args...)
}

// Has indica si existe un texto para key.
func Has(key string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := es[key]
	return ok
}

// Register añade textos propios de un módulo. Se llama desde init(); una clave
// repetida es un error de programación y provoca un pánico.
func Register(texts map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	for k, v := range texts {
		if _, dup := es[k]; dup {
			panic("msg: clave de texto duplicada: " + k)
		}
		es[k] = v
	}
}
