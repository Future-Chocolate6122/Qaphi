// Package netcfg gestiona la configuración de red de Qaphi: dirección de
// escucha, un único puerto y protocolo (HTTP o HTTPS con certificado
// autofirmado), y su cambio en caliente con confirmación y vuelta atrás.
package netcfg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"

	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

// Claves de ajustes (estables).
const (
	KeyBind = "net.bind"
	KeyPort = "net.port"
	KeyTLS  = "net.tls"
)

// Puertos.
const (
	DefaultPort = 9077
	// LegacyPort es el puerto de las instalaciones de v0.1.0, que lo conservan
	// al actualizar.
	LegacyPort = 8091
	// MinPort: el servicio no tiene CAP_NET_BIND_SERVICE, así que no puede
	// usar puertos por debajo de 1024.
	MinPort = 1024
	MaxPort = 65535
)

// BindLocal es la dirección «solo este equipo».
const BindLocal = "127.0.0.1"

// Config es la configuración de red. Bind vacío = todas las interfaces.
type Config struct {
	Bind string
	Port int
	TLS  bool
}

// Default devuelve la configuración por defecto.
func Default() Config { return Config{Port: DefaultPort} }

// Addr devuelve la dirección host:puerto para escuchar.
func (c Config) Addr() string { return net.JoinHostPort(c.Bind, strconv.Itoa(c.Port)) }

// Scheme devuelve "https" o "http".
func (c Config) Scheme() string {
	if c.TLS {
		return "https"
	}
	return "http"
}

// String devuelve la configuración legible: «https://0.0.0.0:9077».
func (c Config) String() string {
	host := c.Bind
	if host == "" {
		host = "0.0.0.0"
	}
	return c.Scheme() + "://" + net.JoinHostPort(host, strconv.Itoa(c.Port))
}

// Error es un error de validación o de apertura con un mensaje traducible.
type Error struct {
	Msg msg.Msg
	Err error // causa técnica, si la hay
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg.String() + ": " + e.Err.Error()
	}
	return e.Msg.String()
}

func (e *Error) Unwrap() error { return e.Err }

// Validate comprueba la configuración. localIPs son las IP del equipo; una IP
// concreta debe ser una de ellas.
func Validate(c Config, localIPs []string) error {
	if c.Port < MinPort || c.Port > MaxPort {
		return &Error{Msg: msg.New("settings.net.err.port")}
	}
	if c.Bind != "" && c.Bind != BindLocal && !slices.Contains(localIPs, c.Bind) {
		return &Error{Msg: msg.New("settings.net.err.bind")}
	}
	return nil
}

// LocalIPs devuelve las IP del equipo que se pueden elegir como dirección de
// escucha (sin la de loopback, que es «solo este equipo», ni las de enlace
// local).
func LocalIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() || ipn.IP.IsMulticast() {
			continue
		}
		out = append(out, ipn.IP.String())
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Load lee la configuración guardada. Si no hay ninguna, devuelve la de por
// defecto; legacy indica una instalación anterior (v0.1.0), que conserva el
// puerto 8091.
func Load(ctx context.Context, st store.Store, legacy bool) (Config, error) {
	c := Default()
	if legacy {
		c.Port = LegacyPort
	}
	if v, ok, err := st.GetSetting(ctx, KeyBind); err != nil {
		return c, err
	} else if ok {
		c.Bind = v
	}
	if v, ok, err := st.GetSetting(ctx, KeyPort); err != nil {
		return c, err
	} else if ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("invalid stored port %q", v)
		}
		c.Port = n
	}
	if v, ok, err := st.GetSetting(ctx, KeyTLS); err != nil {
		return c, err
	} else if ok {
		c.TLS = v == "true"
	}
	return c, nil
}

// Save guarda la configuración.
func Save(ctx context.Context, st store.Store, c Config) error {
	return errors.Join(
		st.SetSetting(ctx, KeyBind, c.Bind),
		st.SetSetting(ctx, KeyPort, strconv.Itoa(c.Port)),
		st.SetSetting(ctx, KeyTLS, strconv.FormatBool(c.TLS)),
	)
}
