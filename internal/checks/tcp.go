package checks

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"qaphi/internal/module"
	"qaphi/internal/msg"
)

func init() {
	msg.Register(map[string]string{
		"check.tcp.name":      "TCP",
		"check.tcp.desc":      "Comprueba que un puerto TCP acepta conexiones.",
		"check.tcp.host":      "Host",
		"check.tcp.host.help": "Nombre o dirección IP, p. ej. 192.168.1.10",
		"check.tcp.port":      "Puerto",
		"check.tcp.timeout":   "Tiempo máximo",
		"check.tcp.err.host":  "Nombre o dirección no válidos.",
		"check.tcp.msg.ok":    "conexión establecida",
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
		return module.Errors{"host": msg.T("check.tcp.err.host")}
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
	return Result{OK: true, Latency: latency, Message: msg.T("check.tcp.msg.ok")}
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
