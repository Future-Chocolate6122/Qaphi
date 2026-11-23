// Package settings guarda los ajustes globales de la interfaz (idioma, tema
// y zona horaria) con una caché en memoria: se leen en cada petición sin
// tocar el disco y se escriben al momento cuando cambian.
package settings

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // base de datos de zonas incluida: no depende del sistema

	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/store"
	"github.com/future-chocolate6122/qaphi/internal/theme"
)

// Claves de los ajustes (estables).
const (
	KeyLanguage = "ui.language"
	KeyTheme    = "ui.theme"
	KeyTimezone = "ui.timezone" // vacío = la del sistema
)

//go:embed zones.txt
var zonesFile string

var zones = strings.Fields(zonesFile)

// Timezones devuelve las zonas horarias que se pueden elegir (IANA).
func Timezones() []string { return slices.Clone(zones) }

// UI son los ajustes globales de la interfaz.
type UI struct {
	Lang     msg.Lang
	LangSet  bool   // false hasta que se elige (antes del setup se negocia con el navegador)
	Theme    string // ID del tema
	Timezone string // nombre IANA; vacío = la del sistema
}

// Service guarda los ajustes con caché en memoria.
type Service struct {
	st store.Store

	mu   sync.RWMutex
	ui   UI
	loc  *time.Location
	data Data
}

// Load lee los ajustes guardados.
func Load(ctx context.Context, st store.Store) (*Service, error) {
	s := &Service{st: st, ui: UI{Lang: msg.Default, Theme: theme.DefaultID}, loc: time.Local}
	if v, ok, err := st.GetSetting(ctx, KeyLanguage); err != nil {
		return nil, err
	} else if l, valid := msg.Parse(v); ok && valid {
		s.ui.Lang, s.ui.LangSet = l, true
	}
	if v, ok, err := st.GetSetting(ctx, KeyTheme); err != nil {
		return nil, err
	} else if ok && theme.Exists(v) {
		s.ui.Theme = v
	}
	if v, ok, err := st.GetSetting(ctx, KeyTimezone); err != nil {
		return nil, err
	} else if ok && v != "" {
		if loc, err := time.LoadLocation(v); err == nil {
			s.ui.Timezone, s.loc = v, loc
		}
	}
	if err := s.loadData(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// UI devuelve los ajustes actuales.
func (s *Service) UI() UI {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ui
}

// Location devuelve la zona horaria con la que se muestran las fechas.
func (s *Service) Location() *time.Location {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loc
}

// SetLanguage guarda el idioma (lo usa /setup).
func (s *Service) SetLanguage(ctx context.Context, l msg.Lang) error {
	if _, ok := msg.Parse(string(l)); !ok {
		return fmt.Errorf("unknown language %q", l)
	}
	if err := s.st.SetSetting(ctx, KeyLanguage, string(l)); err != nil {
		return err
	}
	s.mu.Lock()
	s.ui.Lang, s.ui.LangSet = l, true
	s.mu.Unlock()
	return nil
}

// SetUI valida y guarda idioma, tema y zona horaria.
func (s *Service) SetUI(ctx context.Context, l msg.Lang, themeID, tz string) error {
	if _, ok := msg.Parse(string(l)); !ok {
		return fmt.Errorf("unknown language %q", l)
	}
	if !theme.Exists(themeID) {
		return fmt.Errorf("unknown theme %q", themeID)
	}
	loc := time.Local
	if tz != "" {
		if !slices.Contains(zones, tz) {
			return fmt.Errorf("unknown time zone %q", tz)
		}
		var err error
		if loc, err = time.LoadLocation(tz); err != nil {
			return err
		}
	}
	for k, v := range map[string]string{KeyLanguage: string(l), KeyTheme: themeID, KeyTimezone: tz} {
		if err := s.st.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.ui = UI{Lang: l, LangSet: true, Theme: themeID, Timezone: tz}
	s.loc = loc
	s.mu.Unlock()
	return nil
}

// SystemTimezone devuelve el nombre de la zona horaria del sistema, para
// mostrarlo en el selector («la del sistema (Europe/Madrid)»).
func SystemTimezone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return strings.TrimPrefix(tz, ":")
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			return target[i+len("zoneinfo/"):]
		}
	}
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(b)); tz != "" {
			return tz
		}
	}
	if name := filepath.Base(time.Local.String()); name != "Local" {
		return name
	}
	return "UTC"
}

// Claves de los ajustes de datos y de la página pública (estables).
const (
	KeyPublic         = "page.public"
	KeyHideGroups     = "page.hide_groups"
	KeyHideLatency    = "page.hide_latency"
	KeyHideIncidents  = "page.hide_incidents"
	KeyHideDetail     = "page.hide_detail"
	KeyRawHours       = "retention.raw_hours"
	KeyAggregateDays  = "retention.aggregate_days"
	KeyAggregatesOnly = "retention.aggregates_only"
	KeySessionTTL     = "session.ttl" // lo lee también auth
)

// Límites y valores por defecto de los ajustes de datos.
const (
	DefaultRawHours      = 48
	MinRawHours          = 1
	MaxRawHours          = 720
	DefaultAggregateDays = 90
	MinAggregateDays     = 7
	MaxAggregateDays     = 730
	DefaultSessionTTL    = 7 * 24 * time.Hour
	MinSessionTTL        = time.Hour
	MaxSessionTTL        = 365 * 24 * time.Hour
)

// Data son los ajustes de la página pública, la retención y las sesiones.
type Data struct {
	Public bool
	// Información que se retira de la página pública (sin sesión). Los
	// destinos, mensajes y errores se ocultan siempre.
	HideGroups    bool // nombres de grupo: todos los checks juntos
	HideLatency   bool // tiempos de respuesta y gráfica
	HideIncidents bool // historial de incidencias
	HideDetail    bool // páginas de detalle (/check/<id>)

	RawHours       int
	AggregateDays  int
	AggregatesOnly bool
	SessionTTL     time.Duration
}

// DefaultData devuelve los ajustes de datos por defecto.
func DefaultData() Data {
	return Data{RawHours: DefaultRawHours, AggregateDays: DefaultAggregateDays, SessionTTL: DefaultSessionTTL}
}

// Validate comprueba los límites. Devuelve el nombre del campo erróneo.
func (d Data) Validate() (field string, ok bool) {
	switch {
	case d.RawHours < MinRawHours || d.RawHours > MaxRawHours:
		return "raw_hours", false
	case d.AggregateDays < MinAggregateDays || d.AggregateDays > MaxAggregateDays:
		return "aggregate_days", false
	case d.SessionTTL < MinSessionTTL || d.SessionTTL > MaxSessionTTL:
		return "session_ttl", false
	}
	return "", true
}

// LoadData lee los ajustes de datos (se llama desde Load).
func (s *Service) loadData(ctx context.Context) error {
	d := DefaultData()
	get := func(key string) (string, bool, error) { return s.st.GetSetting(ctx, key) }
	if v, ok, err := get(KeyPublic); err != nil {
		return err
	} else if ok {
		d.Public = v == "true"
	}
	for key, dst := range map[string]*bool{
		KeyAggregatesOnly: &d.AggregatesOnly, KeyHideGroups: &d.HideGroups, KeyHideLatency: &d.HideLatency,
		KeyHideIncidents: &d.HideIncidents, KeyHideDetail: &d.HideDetail,
	} {
		if v, ok, err := get(key); err != nil {
			return err
		} else if ok {
			*dst = v == "true"
		}
	}
	if v, ok, err := get(KeyRawHours); err != nil {
		return err
	} else if n, err := strconv.Atoi(v); ok && err == nil {
		d.RawHours = n
	}
	if v, ok, err := get(KeyAggregateDays); err != nil {
		return err
	} else if n, err := strconv.Atoi(v); ok && err == nil {
		d.AggregateDays = n
	}
	if v, ok, err := get(KeySessionTTL); err != nil {
		return err
	} else if t, err := time.ParseDuration(v); ok && err == nil {
		d.SessionTTL = t
	}
	if _, valid := d.Validate(); !valid {
		d = DefaultData()
	}
	s.mu.Lock()
	s.data = d
	s.mu.Unlock()
	return nil
}

// Data devuelve los ajustes de datos actuales.
func (s *Service) Data() Data {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

// SetData valida y guarda los ajustes de datos.
func (s *Service) SetData(ctx context.Context, d Data) error {
	if field, ok := d.Validate(); !ok {
		return fmt.Errorf("invalid value for %s", field)
	}
	for k, v := range map[string]string{
		KeyPublic:         strconv.FormatBool(d.Public),
		KeyAggregatesOnly: strconv.FormatBool(d.AggregatesOnly),
		KeyHideGroups:     strconv.FormatBool(d.HideGroups),
		KeyHideLatency:    strconv.FormatBool(d.HideLatency),
		KeyHideIncidents:  strconv.FormatBool(d.HideIncidents),
		KeyHideDetail:     strconv.FormatBool(d.HideDetail),
		KeyRawHours:       strconv.Itoa(d.RawHours),
		KeyAggregateDays:  strconv.Itoa(d.AggregateDays),
		KeySessionTTL:     d.SessionTTL.String(),
	} {
		if err := s.st.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.data = d
	s.mu.Unlock()
	return nil
}
