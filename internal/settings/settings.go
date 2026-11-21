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

	mu  sync.RWMutex
	ui  UI
	loc *time.Location
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
