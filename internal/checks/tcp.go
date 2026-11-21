package checks

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

func init() {
	msg.Register(map[string]msg.Text{
		"check.tcp.name": msg.Tr("TCP", "TCP", "TCP", "TCP"),
		"check.tcp.desc": msg.Tr(
			"Comprueba que un puerto TCP acepta conexiones.",
			"Checks that a TCP port accepts connections.",
			"Prüft, ob ein TCP-Port Verbindungen annimmt.",
			"Vérifie qu’un port TCP accepte les connexions."),
		"check.tcp.host": msg.Tr("Host", "Host", "Host", "Hôte"),
		"check.tcp.host.help": msg.Tr(
			"Nombre o dirección IP, p. ej. 192.168.1.10",
			"Name or IP address, e.g. 192.168.1.10",
			"Name oder IP-Adresse, z. B. 192.168.1.10",
			"Nom ou adresse IP, p. ex. 192.168.1.10"),
		"check.tcp.port":     msg.Tr("Puerto", "Port", "Port", "Port"),
		"check.tcp.timeout":  msg.Tr("Tiempo máximo", "Timeout", "Zeitlimit", "Délai maximal"),
		"check.tcp.err.host": msg.Tr("Nombre o dirección no válidos.", "Invalid name or address.", "Ungültiger Name oder ungültige Adresse.", "Nom ou adresse non valide."),
		"check.tcp.msg.ok":   msg.Tr("conexión establecida", "connection established", "Verbindung hergestellt", "connexion établie"),
	})
	Registry.Register(module.Info{
		ID:          "tcp",
		Name:        "check.tcp.name",
		Description: "check.tcp.desc",
		Order:       20,
		Fields: []module.Field{
			{Key: "host", Label: "check.tcp.host", Help: "check.tcp.host.help", Type: module.Text, Required: true, MaxLen: 253},
			{Key: "port", Label: "check.tcp.port", Type: module.Int, Required: true, Min: "1", Max: "65535"},
			{Key: "timeout", Label: "check.tcp.timeout", Type: module.Duration, Default: DefaultTimeout, Min: MinTimeout, Max: MaxTimeout},
		},
	}, tcpChecker{})
}

type tcpChecker struct{}

func (tcpChecker) Timeout(p module.Values) time.Duration { return p.Duration("timeout") }

func (tcpChecker) address(p module.Values) string {
	return net.JoinHostPort(strings.Trim(p.String("host"), "[]"), strconv.FormatInt(p.Int64("port"), 10))
}

func (c tcpChecker) Target(p module.Values) string { return c.address(p) }

func (tcpChecker) Validate(p module.Values) module.Errors {
	if !validHost(p.String("host")) {
		return module.Errors{"host": msg.New("check.tcp.err.host")}
	}
	return nil
}

func (c tcpChecker) Check(ctx context.Context, p module.Values) Result {
	ctx, cancel := context.WithTimeout(ctx, p.Duration("timeout"))
	defer cancel()
	var d net.Dialer
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", c.address(p))
	latency := time.Since(start)
	if err != nil {
		return Result{Latency: latency, Message: describeNetError(ctx, err)}
	}
	conn.Close()
	return Result{OK: true, Latency: latency, Message: msg.New("check.tcp.msg.ok")}
}

// validHost acepta una IP (v4 o v6, con o sin corchetes) o un nombre DNS.
func validHost(h string) bool {
	h = strings.Trim(h, "[]")
	if h == "" || len(h) > 253 {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return false
			}
		}
	}
	return true
}
