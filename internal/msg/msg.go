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

	// Navegación
	"nav.home":   "Estado",
	"nav.admin":  "Administración",
	"nav.logout": "Cerrar sesión",

	// Configuración inicial
	"setup.title":      "Configuración inicial",
	"setup.intro":      "Elige la contraseña del usuario admin. Para comprobar que tienes acceso al equipo, introduce también el token de configuración que Qaphi escribe en su log al arrancar.",
	"setup.token":      "Token de configuración",
	"setup.token.help": "En una instalación con systemd: journalctl -u qaphi | grep token",
	"setup.password":   "Contraseña",
	"setup.confirm":    "Repite la contraseña",
	"setup.submit":     "Guardar y entrar",
	"setup.err.token":  "El token de configuración no es correcto.",

	// Inicio de sesión
	"login.title":    "Iniciar sesión",
	"login.user":     "Usuario",
	"login.password": "Contraseña",
	"login.submit":   "Entrar",
	"login.err":      "Contraseña incorrecta.",
	"login.locked":   "Demasiados intentos fallidos. Vuelve a intentarlo en %s.",

	// Contraseñas
	"auth.err.password_short":    "La contraseña debe tener al menos 10 caracteres.",
	"auth.err.password_long":     "La contraseña no puede superar los 72 bytes.",
	"auth.err.password_mismatch": "Las contraseñas no coinciden.",

	// Página de estado
	"home.title":       "Estado de los servicios",
	"home.empty":       "Todavía no hay checks configurados.",
	"home.empty.admin": "Créalos en Administración.",
	"home.recent":      "Se actualiza automáticamente cada 30 segundos.",
	"col.name":         "Nombre",
	"col.group":        "Grupo",
	"col.type":         "Tipo",
	"col.target":       "Destino",
	"col.status":       "Estado",
	"col.latency":      "Respuesta",
	"col.last":         "Última comprobación",
	"col.detail":       "Detalle",
	"col.interval":     "Intervalo",
	"col.actions":      "Acciones",
	"status.up":        "Operativo",
	"status.down":      "Caído",
	"status.unknown":   "Sin datos",
	"status.paused":    "Pausado",
	"time.never":       "nunca",
	"time.now":         "ahora",
	"time.ago.s":       "hace %d s",
	"time.ago.m":       "hace %d min",
	"time.ago.h":       "hace %d h",
	"time.ago.d":       "hace %d d",

	// Administración
	"admin.title":                "Administración",
	"admin.checks":               "Checks",
	"admin.new":                  "Nuevo check",
	"admin.edit":                 "Editar",
	"admin.delete":               "Borrar",
	"admin.empty":                "No hay checks todavía.",
	"admin.choose_type":          "Elige el tipo de check",
	"check.new.title":            "Nuevo check · %s",
	"check.edit.title":           "Editar check · %s",
	"check.f.common":             "Datos generales",
	"check.f.params":             "Parámetros",
	"check.f.name":               "Nombre",
	"check.f.group":              "Grupo",
	"check.f.group.help":         "Opcional, p. ej. Red, Servicios o Externos.",
	"check.f.interval":           "Intervalo",
	"check.f.interval.help":      "Cada cuánto se comprueba (entre 10s y 24h).",
	"check.f.paused":             "Pausado (no se comprueba)",
	"check.err.interval_timeout": "El intervalo debe ser mayor que el tiempo máximo del check.",
	"form.save":                  "Guardar",
	"form.cancel":                "Cancelar",
	"form.errors":                "Revisa los campos marcados.",
	"form.secret.keep":           "Déjalo vacío para conservar el valor actual.",
	"delete.title":               "Borrar check",
	"delete.confirm":             "¿Seguro que quieres borrar «%s»? También se borrará su historial.",
	"delete.submit":              "Borrar definitivamente",
	"flash.created":              "Check creado. Se comprobará en unos segundos.",
	"flash.updated":              "Check guardado.",
	"flash.deleted":              "Check borrado.",
	"flash.welcome":              "Contraseña guardada. Ya puedes crear tus checks.",

	// Errores HTTP
	"http.notfound":  "Página no encontrada.",
	"http.forbidden": "La sesión del formulario no es válida o ha caducado. Vuelve a cargar la página e inténtalo de nuevo.",
	"http.toolarge":  "El formulario es demasiado grande.",
	"http.error":     "Error",
	"http.internal":  "Error interno del servidor.",

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
