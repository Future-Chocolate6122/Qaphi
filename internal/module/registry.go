package module

import (
	"fmt"
	"sort"
	"sync"
)

// Entry es un módulo registrado: su declaración y su implementación.
type Entry[T any] struct {
	Info Info
	Impl T
}

// Registry guarda los módulos de una familia (checks, avisos, componentes).
// T es la interfaz que implementan los módulos de esa familia. Cada familia
// declara su registro como variable de paquete y sus módulos se añaden desde
// init().
type Registry[T any] struct {
	kind    string
	mu      sync.RWMutex
	entries map[string]Entry[T]
}

// NewRegistry crea un registro vacío. kind es un nombre descriptivo de la
// familia («check», «aviso»…) que aparece en los mensajes de error.
func NewRegistry[T any](kind string) *Registry[T] {
	return &Registry[T]{kind: kind, entries: map[string]Entry[T]{}}
}

// Register añade un módulo. Una declaración incoherente o un ID repetido son
// errores de programación y provocan un pánico al arrancar.
func (r *Registry[T]) Register(info Info, impl T) {
	if err := info.Validate(); err != nil {
		panic(fmt.Sprintf("%s registry: %v", r.kind, err))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.entries[info.ID]; dup {
		panic(fmt.Sprintf("%s registry: duplicate id %q", r.kind, info.ID))
	}
	r.entries[info.ID] = Entry[T]{Info: info, Impl: impl}
}

// Lookup busca un módulo por su ID.
func (r *Registry[T]) Lookup(id string) (Entry[T], bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	return e, ok
}

// List devuelve los módulos ordenados por Order y, a igualdad, por ID.
func (r *Registry[T]) List() []Entry[T] {
	r.mu.RLock()
	out := make([]Entry[T], 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Info.Order != out[j].Info.Order {
			return out[i].Info.Order < out[j].Info.Order
		}
		return out[i].Info.ID < out[j].Info.ID
	})
	return out
}

// Len devuelve el número de módulos registrados.
func (r *Registry[T]) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}
