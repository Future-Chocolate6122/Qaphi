// Package config resuelve la configuración de arranque a partir de flags y
// variables de entorno. Precedencia: flag explícito > variable de entorno >
// valor por defecto. El resto de la configuración vive en la base de datos y
// se gestiona desde la web.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Límites del intervalo de volcado del buffer de resultados. Por debajo del
// mínimo se pierde el sentido de agrupar escrituras; por encima del máximo la
// pérdida de datos ante un corte de luz sería excesiva.
const (
	MinFlushInterval = 10 * time.Second
	MaxFlushInterval = time.Hour
)

// Config es la configuración de arranque ya validada.
type Config struct {
	// Listen es la dirección de escucha fijada al arrancar (--listen o
	// QAPHI_LISTEN). Vacía = usar la configuración de red guardada desde la
	// web. Es un recurso de emergencia: si se da, prevalece y fuerza HTTP.
	Listen        string
	DataDir       string        // directorio de datos, ruta absoluta
	LogLevel      slog.Level    // nivel mínimo de log
	LogFormat     string        // "text" o "json"
	FlushInterval time.Duration // cada cuánto se vuelca el buffer de resultados
}

// option describe un flag y su variable de entorno equivalente.
type option struct {
	flag  string
	env   string
	def   string
	usage string
}

var options = []option{
	{"listen", "QAPHI_LISTEN", "", "override the listen address configured in the web UI (host:port, plain HTTP); for emergencies"},
	{"data-dir", "QAPHI_DATA_DIR", "./data", "data directory (database and local socket)"},
	{"log-level", "QAPHI_LOG_LEVEL", "info", "log level: debug, info, warn or error"},
	{"log-format", "QAPHI_LOG_FORMAT", "text", "log format: text or json"},
	{"flush-interval", "QAPHI_FLUSH_INTERVAL", "5m", "how often results are flushed to disk (10s-1h)"},
}

// Parse interpreta los argumentos de un subcomando. getenv permite inyectar el
// entorno en los tests (en producción, os.Getenv). Devuelve flag.ErrHelp si se
// pidió la ayuda con -h.
func Parse(name string, args []string, getenv func(string) string, output io.Writer) (*Config, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(output)

	raw := make(map[string]*string, len(options))
	for _, o := range options {
		def := o.def
		if v := strings.TrimSpace(getenv(o.env)); v != "" {
			def = v
		}
		raw[o.flag] = fs.String(o.flag, def, o.usage)
	}
	fs.Usage = func() {
		fmt.Fprintf(output, "Usage: qaphi %s [options]\n\nOptions (each one can also be set with its environment variable):\n", name)
		for _, o := range options {
			def := o.def
			if def == "" {
				def = "none"
			}
			fmt.Fprintf(output, "  --%s=value  [%s]\n      %s (default: %s)\n", o.flag, o.env, o.usage, def)
		}
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument: %q", fs.Arg(0))
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	// source indica de dónde salió un valor, para que los errores sean claros.
	source := func(o option) string {
		switch {
		case set[o.flag]:
			return "--" + o.flag
		case strings.TrimSpace(getenv(o.env)) != "":
			return o.env
		default:
			return "default value of --" + o.flag
		}
	}

	var (
		cfg  Config
		errs []error
	)
	for _, o := range options {
		v := strings.TrimSpace(*raw[o.flag])
		var err error
		switch o.flag {
		case "listen":
			if v != "" {
				cfg.Listen, err = ParseListen(v)
			}
		case "data-dir":
			cfg.DataDir, err = parseDataDir(v)
		case "log-level":
			cfg.LogLevel, err = parseLogLevel(v)
		case "log-format":
			cfg.LogFormat, err = parseLogFormat(v)
		case "flush-interval":
			cfg.FlushInterval, err = parseFlushInterval(v)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", source(o), err))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &cfg, nil
}

// ParseListen valida y normaliza una dirección host:puerto.
func ParseListen(v string) (string, error) {
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return "", fmt.Errorf("invalid address %q (expected host:port, e.g. \":9077\" or \"127.0.0.1:9077\")", v)
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", fmt.Errorf("invalid port %q", port)
	}
	if strings.ContainsAny(host, " \t") {
		return "", fmt.Errorf("invalid host %q", host)
	}
	return net.JoinHostPort(host, strconv.FormatUint(n, 10)), nil
}

func parseDataDir(v string) (string, error) {
	if v == "" {
		return "", errors.New("cannot be empty")
	}
	// El driver de SQLite separa los parámetros de conexión con '?'.
	if strings.Contains(v, "?") {
		return "", errors.New("the path cannot contain the character '?'")
	}
	abs, err := filepath.Abs(v)
	if err != nil {
		return "", fmt.Errorf("invalid path %q: %w", v, err)
	}
	return abs, nil
}

func parseLogLevel(v string) (slog.Level, error) {
	switch strings.ToLower(v) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("invalid level %q (debug, info, warn or error)", v)
}

func parseLogFormat(v string) (string, error) {
	switch f := strings.ToLower(v); f {
	case "text", "json":
		return f, nil
	}
	return "", fmt.Errorf("invalid format %q (text or json)", v)
}

func parseFlushInterval(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (e.g. \"5m\" or \"30s\")", v)
	}
	if d < MinFlushInterval || d > MaxFlushInterval {
		return 0, fmt.Errorf("%s out of range (between %s and %s)", d, MinFlushInterval, MaxFlushInterval)
	}
	return d, nil
}
