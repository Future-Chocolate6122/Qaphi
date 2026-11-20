package checks

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"qaphi/internal/module"
	"qaphi/internal/msg"
	"qaphi/internal/version"
)

// maxBodyRead es lo máximo que se lee del cuerpo de la respuesta.
const maxBodyRead = 64 << 10

func init() {
	msg.Register(map[string]string{
		"check.http.name":            "HTTP(S)",
		"check.http.desc":            "Comprueba que una URL responde con el código esperado.",
		"check.http.url":             "URL",
		"check.http.url.help":        "Dirección completa, p. ej. https://ejemplo.org/salud",
		"check.http.method":          "Método",
		"check.http.method.get":      "GET",
		"check.http.method.head":     "HEAD",
		"check.http.status":          "Códigos esperados",
		"check.http.status.help":     "Códigos o rangos separados por comas, p. ej. 200-299 o 200,301",
		"check.http.timeout":         "Tiempo máximo",
		"check.http.redirects":       "Seguir redirecciones",
		"check.http.insecure":        "Ignorar certificados TLS no válidos",
		"check.http.insecure.help":   "Solo para servicios internos con certificados autofirmados.",
		"check.http.err.url":         "Debe ser una URL http:// o https:// con servidor.",
		"check.http.err.status":      "Formato no válido. Ejemplos: 200, 200-299, 200,301.",
		"check.http.msg.status":      "HTTP %d",
		"check.http.msg.unexpected":  "HTTP %d (no esperado)",
		"check.http.msg.timeout":     "tiempo agotado",
		"check.http.msg.refused":     "conexión rechazada",
		"check.http.msg.dns":         "nombre no resuelto",
		"check.http.msg.tls":         "error TLS: %s",
		"check.http.msg.unreachable": "red inaccesible",
		"check.http.msg.error":       "error: %s",
	})
	Registry.Register(module.Info{
		ID:          "http",
		Name:        "check.http.name",
		Description: "check.http.desc",
		Order:       10,
		Fields: []module.Field{
			{Key: "url", Label: "check.http.url", Help: "check.http.url.help", Type: module.Text, Required: true, MaxLen: 2048},
			{Key: "method", Label: "check.http.method", Type: module.Select, Default: "GET", Options: []module.Option{
				{Value: "GET", Label: "check.http.method.get"}, {Value: "HEAD", Label: "check.http.method.head"},
			}},
			{Key: "expected_status", Label: "check.http.status", Help: "check.http.status.help", Type: module.Text, Default: "200-299", MaxLen: 100},
			{Key: "timeout", Label: "check.http.timeout", Type: module.Duration, Default: DefaultTimeout, Min: MinTimeout, Max: MaxTimeout},
			{Key: "follow_redirects", Label: "check.http.redirects", Type: module.Bool, Default: "true"},
			{Key: "insecure_tls", Label: "check.http.insecure", Help: "check.http.insecure.help", Type: module.Bool},
		},
	}, httpChecker{})
}

type httpChecker struct{}

// Transportes compartidos, sin conexiones persistentes: cada check abre una
// conexión nueva, así se mide de verdad la disponibilidad y la latencia.
var (
	httpTransport     = newTransport(false)
	httpTransportInsc = newTransport(true)
)

func newTransport(insecure bool) *http.Transport {
	return &http.Transport{
		Proxy:                  nil, // un monitor no debe depender de proxies del entorno
		DialContext:            (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSClientConfig:        &tls.Config{InsecureSkipVerify: insecure, MinVersion: tls.VersionTLS12},
		DisableKeepAlives:      true,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    30 * time.Second,
		ResponseHeaderTimeout:  60 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
}

func (httpChecker) Timeout(p module.Values) time.Duration { return p.Duration("timeout") }

// Target oculta la contraseña si la URL lleva credenciales (usuario:clave@).
func (httpChecker) Target(p module.Values) string {
	if u, err := url.Parse(p.String("url")); err == nil {
		return u.Redacted()
	}
	return p.String("url")
}

func (httpChecker) Validate(p module.Values) module.Errors {
	errs := module.Errors{}
	if u, err := url.Parse(p.String("url")); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs["url"] = msg.T("check.http.err.url")
	}
	if _, err := parseStatusRanges(p.String("expected_status")); err != nil {
		errs["expected_status"] = msg.T("check.http.err.status")
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func (httpChecker) Check(ctx context.Context, p module.Values) Result {
	ranges, err := parseStatusRanges(p.String("expected_status"))
	if err != nil {
		return Result{Message: msg.T("check.http.err.status")}
	}
	ctx, cancel := context.WithTimeout(ctx, p.Duration("timeout"))
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, p.String("method"), p.String("url"), nil)
	if err != nil {
		return Result{Message: msg.T("check.http.err.url")}
	}
	req.Header.Set("User-Agent", "Qaphi/"+version.Version)

	client := &http.Client{Transport: httpTransport}
	if p.Bool("insecure_tls") {
		client.Transport = httpTransportInsc
	}
	if !p.Bool("follow_redirects") {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Result{Latency: time.Since(start), Message: describeNetError(ctx, err)}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyRead))
	resp.Body.Close()
	latency := time.Since(start)

	if !matchStatus(ranges, resp.StatusCode) {
		return Result{Latency: latency, Message: msg.F("check.http.msg.unexpected", resp.StatusCode)}
	}
	return Result{OK: true, Latency: latency, Message: msg.F("check.http.msg.status", resp.StatusCode)}
}

// statusRange es un rango cerrado de códigos HTTP.
type statusRange struct{ lo, hi int }

// parseStatusRanges interpreta «200», «200-299» o listas separadas por comas.
func parseStatusRanges(s string) ([]statusRange, error) {
	var out []statusRange
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, err
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return nil, err
			}
		}
		if a < 100 || b > 599 || a > b {
			return nil, fmt.Errorf("rango no válido %q", part)
		}
		out = append(out, statusRange{a, b})
	}
	if len(out) == 0 {
		return nil, errors.New("sin códigos")
	}
	return out, nil
}

func matchStatus(ranges []statusRange, code int) bool {
	for _, r := range ranges {
		if code >= r.lo && code <= r.hi {
			return true
		}
	}
	return false
}

// describeNetError resume un error de red en un mensaje breve y legible.
func describeNetError(ctx context.Context, err error) string {
	var (
		dnsErr  *net.DNSError
		certErr *tls.CertificateVerificationError
		recErr  tls.RecordHeaderError
	)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded):
		return msg.T("check.http.msg.timeout")
	case errors.Is(err, syscall.ECONNREFUSED):
		return msg.T("check.http.msg.refused")
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return msg.T("check.http.msg.unreachable")
	case errors.As(err, &dnsErr):
		return msg.T("check.http.msg.dns")
	case errors.As(err, &certErr):
		return msg.F("check.http.msg.tls", certErr.Err.Error())
	case errors.As(err, &recErr):
		return msg.F("check.http.msg.tls", "respuesta no TLS")
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return msg.F("check.http.msg.error", truncate(err.Error(), 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
