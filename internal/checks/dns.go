package checks

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

func init() {
	msg.Register(map[string]msg.Text{
		"check.dns.name": msg.Tr("DNS", "DNS", "DNS", "DNS"),
		"check.dns.desc": msg.Tr(
			"Resuelve un nombre y, opcionalmente, comprueba el valor obtenido.",
			"Resolves a name and optionally checks the returned value.",
			"Löst einen Namen auf und prüft optional den zurückgegebenen Wert.",
			"Résout un nom et vérifie éventuellement la valeur obtenue."),
		"check.dns.query":  msg.Tr("Nombre a resolver", "Name to resolve", "Aufzulösender Name", "Nom à résoudre"),
		"check.dns.type":   msg.Tr("Tipo de registro", "Record type", "Eintragstyp", "Type d’enregistrement"),
		"check.dns.server": msg.Tr("Servidor DNS", "DNS server", "DNS-Server", "Serveur DNS"),
		"check.dns.server.help": msg.Tr(
			"Opcional, p. ej. 1.1.1.1 o 192.168.1.1:53. Vacío = el del sistema.",
			"Optional, e.g. 1.1.1.1 or 192.168.1.1:53. Empty = the system’s.",
			"Optional, z. B. 1.1.1.1 oder 192.168.1.1:53. Leer = der des Systems.",
			"Facultatif, p. ex. 1.1.1.1 ou 192.168.1.1:53. Vide = celui du système."),
		"check.dns.expected": msg.Tr("Valor esperado", "Expected value", "Erwarteter Wert", "Valeur attendue"),
		"check.dns.expected.help": msg.Tr(
			"Opcional. Alguna respuesta debe contener este texto (p. ej. una IP).",
			"Optional. Some answer must contain this text (e.g. an IP).",
			"Optional. Eine Antwort muss diesen Text enthalten (z. B. eine IP).",
			"Facultatif. Une réponse doit contenir ce texte (p. ex. une IP)."),
		"check.dns.timeout":      msg.Tr("Tiempo máximo", "Timeout", "Zeitlimit", "Délai maximal"),
		"check.dns.err.query":    msg.Tr("Nombre no válido.", "Invalid name.", "Ungültiger Name.", "Nom non valide."),
		"check.dns.err.server":   msg.Tr("Servidor no válido (IP o IP:puerto).", "Invalid server (IP or IP:port).", "Ungültiger Server (IP oder IP:Port).", "Serveur non valide (IP ou IP:port)."),
		"check.dns.msg.ok":       msg.Tr("%v", "%v", "%v", "%v"),
		"check.dns.msg.empty":    msg.Tr("sin registros", "no records", "keine Einträge", "aucun enregistrement"),
		"check.dns.msg.mismatch": msg.Tr("no aparece el valor esperado (%v)", "expected value not found (%v)", "erwarteter Wert nicht gefunden (%v)", "valeur attendue introuvable (%v)"),
	})
	types := []module.Option{}
	for _, t := range []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS"} {
		key := "check.dns.type." + strings.ToLower(t)
		msg.Register(map[string]msg.Text{key: msg.Tr(t, t, t, t)})
		types = append(types, module.Option{Value: t, Label: key})
	}
	Registry.Register(module.Info{
		ID:          "dns",
		Name:        "check.dns.name",
		Description: "check.dns.desc",
		Order:       40,
		Fields: []module.Field{
			{Key: "query", Label: "check.dns.query", Type: module.Text, Required: true, MaxLen: 253},
			{Key: "record_type", Label: "check.dns.type", Type: module.Select, Default: "A", Options: types},
			{Key: "server", Label: "check.dns.server", Help: "check.dns.server.help", Type: module.Text, MaxLen: 64},
			{Key: "expected", Label: "check.dns.expected", Help: "check.dns.expected.help", Type: module.Text, MaxLen: 255},
			{Key: "timeout", Label: "check.dns.timeout", Type: module.Duration, Default: "5s", Min: MinTimeout, Max: MaxTimeout},
		},
	}, dnsChecker{})
}

type dnsChecker struct{}

func (dnsChecker) Timeout(p module.Values) time.Duration { return p.Duration("timeout") }

func (dnsChecker) Target(p module.Values) string {
	t := p.String("record_type") + " " + p.String("query")
	if srv := p.String("server"); srv != "" {
		t += " @" + srv
	}
	return t
}

func (dnsChecker) Validate(p module.Values) module.Errors {
	errs := module.Errors{}
	if q := strings.TrimSuffix(p.String("query"), "."); !validHost(q) || net.ParseIP(q) != nil {
		errs["query"] = msg.New("check.dns.err.query")
	}
	if srv := p.String("server"); srv != "" && serverAddr(srv) == "" {
		errs["server"] = msg.New("check.dns.err.server")
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

// serverAddr normaliza «ip» o «ip:puerto» a «ip:puerto»; vacío si no es válido.
func serverAddr(s string) string {
	if ip := net.ParseIP(strings.Trim(s, "[]")); ip != nil {
		return net.JoinHostPort(ip.String(), "53")
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil || net.ParseIP(host) == nil || port == "" {
		return ""
	}
	return net.JoinHostPort(host, port)
}

func (dnsChecker) Check(ctx context.Context, p module.Values) Result {
	ctx, cancel := context.WithTimeout(ctx, p.Duration("timeout"))
	defer cancel()

	r := net.DefaultResolver
	if srv := serverAddr(p.String("server")); srv != "" {
		r = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, srv)
			},
		}
	}
	name := p.String("query")
	start := time.Now()
	values, err := lookup(ctx, r, p.String("record_type"), name)
	latency := time.Since(start)
	if err != nil {
		return Result{Latency: latency, Message: describeNetError(ctx, err)}
	}
	if len(values) == 0 {
		return Result{Latency: latency, Message: msg.New("check.dns.msg.empty")}
	}
	if exp := strings.TrimSuffix(strings.ToLower(p.String("expected")), "."); exp != "" {
		found := false
		for _, v := range values {
			if strings.Contains(strings.ToLower(strings.TrimSuffix(v, ".")), exp) {
				found = true
				break
			}
		}
		if !found {
			return Result{Latency: latency, Message: msg.New("check.dns.msg.mismatch", truncate(strings.Join(values, ", "), 150))}
		}
	}
	return Result{OK: true, Latency: latency, Message: msg.New("check.dns.msg.ok", truncate(strings.Join(values, ", "), 150))}
}

// lookup hace la consulta del tipo pedido y devuelve los valores como texto.
func lookup(ctx context.Context, r *net.Resolver, typ, name string) ([]string, error) {
	var out []string
	switch typ {
	case "A", "AAAA":
		network := "ip4"
		if typ == "AAAA" {
			network = "ip6"
		}
		ips, err := r.LookupIP(ctx, network, name)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			out = append(out, ip.String())
		}
	case "CNAME":
		c, err := r.LookupCNAME(ctx, name)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	case "MX":
		mxs, err := r.LookupMX(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, mx := range mxs {
			out = append(out, fmt.Sprintf("%s %d", mx.Host, mx.Pref))
		}
	case "TXT":
		txts, err := r.LookupTXT(ctx, name)
		if err != nil {
			return nil, err
		}
		out = append(out, txts...)
	case "NS":
		nss, err := r.LookupNS(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, ns := range nss {
			out = append(out, ns.Host)
		}
	default:
		return nil, fmt.Errorf("unsupported record type %q", typ)
	}
	sort.Strings(out)
	return out, nil
}
