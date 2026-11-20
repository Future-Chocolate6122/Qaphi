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
	Listen        string        // dirección de escucha HTTP, p. ej. ":8091"
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
	{"listen", "QAPHI_LISTEN", ":8091", "dirección de escucha HTTP (host:puerto)"},
	{"data-dir", "QAPHI_DATA_DIR", "./data", "directorio de datos (base de datos y socket local)"},
	{"log-level", "QAPHI_LOG_LEVEL", "info", "nivel de log: debug, info, warn o error"},
	{"log-format", "QAPHI_LOG_FORMAT", "text", "formato de log: text o json"},
	{"flush-interval", "QAPHI_FLUSH_INTERVAL", "5m", "intervalo de volcado de resultados a disco (10s–1h)"},
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
		fmt.Fprintf(output, "Uso: qaphi %s [opciones]\n\nOpciones (cada una admite su variable de entorno):\n", name)
		for _, o := range options {
			fmt.Fprintf(output, "  --%s=valor  [%s]\n      %s (por defecto: %s)\n", o.flag, o.env, o.usage, o.def)
		}
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("argumento inesperado: %q", fs.Arg(0))
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
			return "valor por defecto de --" + o.flag
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
			cfg.Listen, err = parseListen(v)
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

func parseListen(v string) (string, error) {
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return "", fmt.Errorf("dirección no válida %q (formato host:puerto, p. ej. \":8091\" o \"127.0.0.1:8091\")", v)
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", fmt.Errorf("puerto no válido %q", port)
	}
	if strings.ContainsAny(host, " \t") {
		return "", fmt.Errorf("host no válido %q", host)
	}
	return net.JoinHostPort(host, strconv.FormatUint(n, 10)), nil
}

func parseDataDir(v string) (string, error) {
	if v == "" {
		return "", errors.New("no puede estar vacío")
	}
	// El driver de SQLite separa los parámetros de conexión con '?'.
	if strings.Contains(v, "?") {
		return "", errors.New("la ruta no puede contener el carácter '?'")
	}
	abs, err := filepath.Abs(v)
	if err != nil {
		return "", fmt.Errorf("ruta no válida %q: %w", v, err)
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
	return 0, fmt.Errorf("nivel no válido %q (debug, info, warn o error)", v)
}

func parseLogFormat(v string) (string, error) {
	switch f := strings.ToLower(v); f {
	case "text", "json":
		return f, nil
	}
	return "", fmt.Errorf("formato no válido %q (text o json)", v)
}

func parseFlushInterval(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("duración no válida %q (p. ej. \"5m\" o \"30s\")", v)
	}
	if d < MinFlushInterval || d > MaxFlushInterval {
		return 0, fmt.Errorf("%s fuera de rango (entre %s y %s)", d, MinFlushInterval, MaxFlushInterval)
	}
	return d, nil
}
