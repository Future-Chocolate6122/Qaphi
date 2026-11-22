package checks

import (
	"context"
	"errors"

	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

func init() {
	msg.Register(map[string]msg.Text{
		"check.icmp.name": msg.Tr("Ping (ICMP)", "Ping (ICMP)", "Ping (ICMP)", "Ping (ICMP)"),
		"check.icmp.desc": msg.Tr(
			"Envía pings a un equipo y mide la latencia y la pérdida de paquetes.",
			"Pings a host and measures latency and packet loss.",
			"Sendet Pings an einen Host und misst Latenz und Paketverlust.",
			"Envoie des pings à un hôte et mesure la latence et la perte de paquets."),
		"check.icmp.host": msg.Tr("Host", "Host", "Host", "Hôte"),
		"check.icmp.host.help": msg.Tr(
			"Nombre o dirección IP, p. ej. 192.168.1.1",
			"Name or IP address, e.g. 192.168.1.1",
			"Name oder IP-Adresse, z. B. 192.168.1.1",
			"Nom ou adresse IP, p. ex. 192.168.1.1"),
		"check.icmp.err.host": msg.Tr("Nombre o dirección no válidos.", "Invalid name or address.", "Ungültiger Name oder ungültige Adresse.", "Nom ou adresse non valide."),
		"check.icmp.count":    msg.Tr("Número de paquetes", "Number of packets", "Anzahl der Pakete", "Nombre de paquets"),
		"check.icmp.timeout":  msg.Tr("Tiempo máximo", "Timeout", "Zeitlimit", "Délai maximal"),
		"check.icmp.msg.ok":   msg.Tr("%v recibidos de %v", "%v of %v received", "%v von %v empfangen", "%v sur %v reçus"),
		"check.icmp.msg.loss": msg.Tr(
			"pérdida del %v %% (%v de %v)", "%v%% loss (%v of %v)", "%v %% Verlust (%v von %v)", "perte de %v %% (%v sur %v)"),
		"check.icmp.msg.none": msg.Tr("sin respuesta", "no reply", "keine Antwort", "aucune réponse"),
		"check.icmp.msg.noperm": msg.Tr(
			"el sistema no permite enviar pings: concede CAP_NET_RAW al servicio o amplía net.ipv4.ping_group_range",
			"the system does not allow sending pings: grant CAP_NET_RAW to the service or widen net.ipv4.ping_group_range",
			"das System erlaubt keine Pings: Gewähren Sie dem Dienst CAP_NET_RAW oder erweitern Sie net.ipv4.ping_group_range",
			"le système n’autorise pas l’envoi de pings : accordez CAP_NET_RAW au service ou élargissez net.ipv4.ping_group_range"),
	})
	Registry.Register(module.Info{
		ID:          "icmp",
		Name:        "check.icmp.name",
		Description: "check.icmp.desc",
		Order:       30,
		Fields: []module.Field{
			{Key: "host", Label: "check.icmp.host", Help: "check.icmp.host.help", Type: module.Text, Required: true, MaxLen: 253},
			{Key: "count", Label: "check.icmp.count", Type: module.Int, Default: "3", Min: "1", Max: "10"},
			{Key: "timeout", Label: "check.icmp.timeout", Type: module.Duration, Default: "5s", Min: MinTimeout, Max: MaxTimeout},
		},
	}, icmpChecker{})
}

type icmpChecker struct{}

func (icmpChecker) Timeout(p module.Values) time.Duration { return p.Duration("timeout") }

func (icmpChecker) Target(p module.Values) string { return strings.Trim(p.String("host"), "[]") }

func (icmpChecker) Validate(p module.Values) module.Errors {
	if !validHost(p.String("host")) {
		return module.Errors{"host": msg.New("check.icmp.err.host")}
	}
	return nil
}

// errNoPerm indica que el sistema no permite abrir un socket ICMP.
var errNoPerm = errors.New("icmp: permission denied")

func (icmpChecker) Check(ctx context.Context, p module.Values) Result {
	ctx, cancel := context.WithTimeout(ctx, p.Duration("timeout"))
	defer cancel()

	ip, err := resolveOne(ctx, strings.Trim(p.String("host"), "[]"))
	if err != nil {
		return Result{Message: describeNetError(ctx, err)}
	}
	conn, raw, err := openICMP(ip)
	if errors.Is(err, errNoPerm) {
		return Result{NoPerm: true, Message: msg.New("check.icmp.msg.noperm")}
	}
	if err != nil {
		return Result{Message: msg.New("check.msg.error", err.Error())}
	}
	defer conn.Close()

	count := int(p.Int64("count"))
	id := os.Getpid() & 0xffff
	var (
		received int
		total    time.Duration
	)
	for seq := 1; seq <= count; seq++ {
		if ctx.Err() != nil {
			break
		}
		rtt, ok := pingOnce(ctx, conn, raw, ip, id, seq)
		if ok {
			received++
			total += rtt
		}
		if seq < count {
			select {
			case <-ctx.Done():
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	switch {
	case received == 0:
		return Result{Latency: 0, Message: msg.New("check.icmp.msg.none")}
	case received < count:
		loss := (count - received) * 100 / count
		return Result{OK: true, Degraded: true, Latency: total / time.Duration(received),
			Message: msg.New("check.icmp.msg.loss", loss, received, count)}
	default:
		return Result{OK: true, Latency: total / time.Duration(received), Message: msg.New("check.icmp.msg.ok", received, count)}
	}
}

// resolveOne resuelve el nombre y prefiere IPv4.
func resolveOne(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if a.IP.To4() != nil {
			return a.IP, nil
		}
	}
	if len(addrs) == 0 {
		return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
	}
	return addrs[0].IP, nil
}

// openICMP abre un socket ICMP: primero sin privilegios (si
// net.ipv4.ping_group_range lo permite) y, si no, raw (CAP_NET_RAW). raw
// indica cuál se abrió: con el raw llegan todas las respuestas del equipo y
// hay que filtrar por identificador.
func openICMP(ip net.IP) (*icmp.PacketConn, bool, error) {
	udpNet, rawNet, addr := "udp4", "ip4:icmp", "0.0.0.0"
	if ip.To4() == nil {
		udpNet, rawNet, addr = "udp6", "ip6:ipv6-icmp", "::"
	}
	conn, err := icmp.ListenPacket(udpNet, addr)
	if err == nil {
		return conn, false, nil
	}
	conn, err2 := icmp.ListenPacket(rawNet, addr)
	if err2 == nil {
		return conn, true, nil
	}
	if isPermission(err) && isPermission(err2) {
		return nil, false, errNoPerm
	}
	return nil, false, err2
}

func isPermission(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) || errors.Is(err, os.ErrPermission)
}

// pingOnce envía un eco y espera su respuesta.
func pingOnce(ctx context.Context, conn *icmp.PacketConn, raw bool, ip net.IP, id, seq int) (time.Duration, bool) {
	v6 := ip.To4() == nil
	var typ icmp.Type = ipv4.ICMPTypeEcho
	proto := 1
	if v6 {
		typ, proto = ipv6.ICMPTypeEchoRequest, 58
	}
	payload := []byte("qaphi-" + strconv.Itoa(seq))
	m := icmp.Message{Type: typ, Body: &icmp.Echo{ID: id, Seq: seq, Data: payload}}
	b, err := m.Marshal(nil)
	if err != nil {
		return 0, false
	}
	var dst net.Addr = &net.UDPAddr{IP: ip}
	if raw {
		dst = &net.IPAddr{IP: ip}
	}
	start := time.Now()
	if _, err := conn.WriteTo(b, dst); err != nil {
		return 0, false
	}
	deadline, _ := ctx.Deadline()
	if per := start.Add(2 * time.Second); deadline.IsZero() || per.Before(deadline) {
		deadline = per
	}
	_ = conn.SetReadDeadline(deadline)
	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return 0, false
		}
		rm, err := icmp.ParseMessage(proto, buf[:n])
		if err != nil {
			continue
		}
		if rm.Type != ipv4.ICMPTypeEchoReply && rm.Type != ipv6.ICMPTypeEchoReply {
			continue
		}
		echo, ok := rm.Body.(*icmp.Echo)
		// Con el socket sin privilegios el núcleo reescribe el identificador:
		// basta con la secuencia y el contenido.
		if !ok || echo.Seq != seq || string(echo.Data) != string(payload) || (raw && echo.ID != id) {
			continue
		}
		return time.Since(start), true
	}
}
