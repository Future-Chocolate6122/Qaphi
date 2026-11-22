package web

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/netcfg"
	"github.com/future-chocolate6122/qaphi/internal/settings"
	"github.com/future-chocolate6122/qaphi/internal/theme"
)

// textOption es una opción de un desplegable con su texto ya traducido.
type textOption struct {
	Value, Text string
	Selected    bool
}

type dataView struct {
	Public         bool
	RawHours       int
	AggregateDays  int
	AggregatesOnly bool
	SessionTTL     string
	Error          string
}

type settingsView struct {
	Data      dataView
	Languages []textOption
	Themes    []textOption
	Zones     []textOption
	Net       netView
	Cert      certView
	Pending   *pendingView
	NetError  string
}

type netView struct {
	Available bool // hay gestor de red
	Locked    bool // fijada al arrancar (--listen)
	Current   string
	Binds     []textOption
	Port      int
	TLS       bool
}

type certView struct {
	Has  bool
	Info string
}

type pendingView struct {
	URL      string
	Deadline string
	InPlace  bool
}

// settingsData prepara la página de ajustes. form, si no es nil, es la
// configuración de red que se acaba de enviar (para no perderla si hay error).
func (s *Server) settingsData(r *http.Request, form *netcfg.Config, netErr string) settingsView {
	l := s.lang(r)
	ui := s.Settings.UI()
	d := s.Settings.Data()
	v := settingsView{NetError: netErr, Data: dataView{
		Public: d.Public, RawHours: d.RawHours, AggregateDays: d.AggregateDays,
		AggregatesOnly: d.AggregatesOnly, SessionTTL: shortDuration(d.SessionTTL),
	}}
	for _, lg := range msg.Languages() {
		v.Languages = append(v.Languages, textOption{Value: string(lg.Code), Text: lg.Name, Selected: lg.Code == ui.Lang})
	}
	for _, e := range theme.Registry.List() {
		v.Themes = append(v.Themes, textOption{Value: e.Info.ID, Text: msg.T(l, e.Info.Name), Selected: e.Info.ID == ui.Theme})
	}
	v.Zones = append(v.Zones, textOption{Value: "", Text: msg.F(l, "settings.timezone.system", settings.SystemTimezone()), Selected: ui.Timezone == ""})
	for _, z := range settings.Timezones() {
		v.Zones = append(v.Zones, textOption{Value: z, Text: z, Selected: z == ui.Timezone})
	}

	if s.Network == nil {
		return v
	}
	cur := s.Network.Current()
	shown := cur
	if form != nil {
		shown = *form
	}
	v.Net = netView{Available: true, Locked: s.Network.Locked(), Current: cur.String(), Port: shown.Port, TLS: shown.TLS}
	v.Net.Binds = []textOption{
		{Value: "", Text: msg.T(l, "settings.net.bind.all"), Selected: shown.Bind == ""},
		{Value: netcfg.BindLocal, Text: msg.T(l, "settings.net.bind.local"), Selected: shown.Bind == netcfg.BindLocal},
	}
	for _, ip := range netcfg.LocalIPs() {
		v.Net.Binds = append(v.Net.Binds, textOption{Value: ip, Text: msg.F(l, "settings.net.bind.ip", ip), Selected: shown.Bind == ip})
	}
	if s.Certs != nil {
		if info, ok := s.Certs.Info(); ok {
			v.Cert = certView{Has: true, Info: msg.F(l, "settings.cert.info", formatDate(l, s.Settings.Location(), info.NotAfter), info.Fingerprint)}
		}
	}
	if cfg, deadline, inPlace, ok := s.Network.Pending(); ok {
		v.Pending = &pendingView{
			URL:      confirmURL(r, cfg),
			Deadline: deadline.In(s.Settings.Location()).Format("15:04:05"),
			InPlace:  inPlace,
		}
	}
	return v
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, status int, form *netcfg.Config, netErr, flashText string) {
	data := pageData{
		Title: s.T(r, "settings.title"), Active: "settings", Flash: flashText,
		Body: s.settingsData(r, form, netErr),
	}
	if s.Network != nil && s.Network.TakeReverted() {
		data.Alert = s.T(r, "flash.reverted")
	}
	s.render(w, r, status, "settings", data)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, r, http.StatusOK, nil, "", flash(s.lang(r), r.URL.Query()))
}

// handleSettingsSave guarda idioma, tema y zona horaria.
func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	l, ok := msg.Parse(r.PostFormValue("language"))
	if !ok {
		s.renderSettings(w, r, http.StatusBadRequest, nil, "", "")
		return
	}
	if err := s.Settings.SetUI(r.Context(), l, r.PostFormValue("theme"), r.PostFormValue("timezone")); err != nil {
		s.Log.Warn("invalid settings", "error", err)
		s.renderSettings(w, r, http.StatusBadRequest, nil, "", "")
		return
	}
	s.Log.Info("settings saved", "language", string(l), "theme", r.PostFormValue("theme"), "timezone", r.PostFormValue("timezone"))
	redirect(w, r, "/admin/settings?m=settings")
}

// handleNetworkApply pone en marcha la configuración de red nueva, a la
// espera de confirmación desde la dirección nueva.
func (s *Server) handleNetworkApply(w http.ResponseWriter, r *http.Request) {
	if s.Network == nil {
		s.renderError(w, r, http.StatusNotFound, "http.notfound")
		return
	}
	l := s.lang(r)
	port, _ := strconv.Atoi(r.PostFormValue("port"))
	cfg := netcfg.Config{Bind: r.PostFormValue("bind"), Port: port, TLS: r.PostFormValue("protocol") == "https"}
	fail := func(status int, text string) {
		s.renderSettings(w, r, status, &cfg, text, "")
	}
	if err := netcfg.Validate(cfg, netcfg.LocalIPs()); err != nil {
		fail(http.StatusUnprocessableEntity, netErrorText(l, err))
		return
	}
	inPlace, err := s.Network.Apply(r.Context(), cfg)
	switch {
	case errors.Is(err, netcfg.ErrUnchanged):
		fail(http.StatusUnprocessableEntity, msg.T(l, "settings.net.unchanged"))
		return
	case errors.Is(err, netcfg.ErrPending):
		fail(http.StatusConflict, msg.T(l, "settings.net.err.pending"))
		return
	case errors.Is(err, netcfg.ErrLocked):
		fail(http.StatusConflict, msg.T(l, "settings.net.locked"))
		return
	case err != nil:
		var ne *netcfg.Error
		if errors.As(err, &ne) {
			fail(http.StatusUnprocessableEntity, ne.Msg.In(l))
			return
		}
		s.internalError(w, r, err)
		return
	}
	s.Log.Info("network change applied", "listen", cfg.Addr(), "scheme", cfg.Scheme(), "in_place", inPlace)
	// Se responde aquí mismo (sin redirección): con un cambio en el sitio,
	// esta dirección deja de responder en cuanto se envía la página.
	_, deadline, _, _ := s.Network.Pending()
	s.render(w, r, http.StatusOK, "net_pending", pageData{
		Title: msg.T(l, "settings.net.pending"), Active: "settings",
		Body: pendingView{
			URL:      confirmURL(r, cfg),
			Deadline: deadline.In(s.Settings.Location()).Format("15:04:05"),
			InPlace:  inPlace,
		},
	})
}

type confirmView struct {
	Pending bool
	Message string
}

func (s *Server) handleNetworkConfirmForm(w http.ResponseWriter, r *http.Request) {
	if s.Network == nil {
		s.renderError(w, r, http.StatusNotFound, "http.notfound")
		return
	}
	v := confirmView{Pending: true}
	if _, _, _, ok := s.Network.Pending(); !ok {
		v = confirmView{Message: s.T(r, "settings.net.confirm.none")}
	}
	s.render(w, r, http.StatusOK, "net_confirm", pageData{Title: s.T(r, "settings.net.confirm.title"), Active: "settings", Body: v})
}

func (s *Server) handleNetworkConfirm(w http.ResponseWriter, r *http.Request) {
	if s.Network == nil {
		s.renderError(w, r, http.StatusNotFound, "http.notfound")
		return
	}
	l := s.lang(r)
	cfg, _, _, _ := s.Network.Pending()
	err := s.Network.Confirm(r)
	var v confirmView
	switch {
	case err == nil:
		redirect(w, r, "/admin/settings?m=network")
		return
	case errors.Is(err, netcfg.ErrWrongEndpoint):
		v = confirmView{Pending: true, Message: msg.F(l, "settings.net.confirm.wrong", confirmURL(r, cfg))}
	case errors.Is(err, netcfg.ErrNoPending):
		v = confirmView{Message: msg.T(l, "settings.net.confirm.none")}
	default:
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, http.StatusConflict, "net_confirm", pageData{Title: msg.T(l, "settings.net.confirm.title"), Active: "settings", Body: v})
}

func (s *Server) handleNetworkCancel(w http.ResponseWriter, r *http.Request) {
	if s.Network == nil {
		s.renderError(w, r, http.StatusNotFound, "http.notfound")
		return
	}
	if err := s.Network.Cancel(); err != nil && !errors.Is(err, netcfg.ErrNoPending) {
		s.internalError(w, r, err)
		return
	}
	redirect(w, r, "/admin/settings?m=cancelled")
}

func (s *Server) handleCertRegenerate(w http.ResponseWriter, r *http.Request) {
	if s.Certs == nil {
		s.renderError(w, r, http.StatusNotFound, "http.notfound")
		return
	}
	if err := s.Certs.Regenerate(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Log.Info("TLS certificate regenerated")
	redirect(w, r, "/admin/settings?m=cert")
}

// confirmURL construye la URL de confirmación en la dirección nueva. Si se
// escucha en todas las interfaces, se usa el mismo nombre con el que el
// navegador llegó hasta aquí.
func confirmURL(r *http.Request, cfg netcfg.Config) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	if cfg.Bind != "" {
		host = cfg.Bind
	}
	return cfg.Scheme() + "://" + net.JoinHostPort(host, strconv.Itoa(cfg.Port)) + "/admin/settings/network/confirm"
}

func netErrorText(l msg.Lang, err error) string {
	var ne *netcfg.Error
	if errors.As(err, &ne) {
		return ne.Msg.In(l)
	}
	return err.Error()
}

// handleDataSave guarda la página pública, la retención y la duración de la
// sesión.
func (s *Server) handleDataSave(w http.ResponseWriter, r *http.Request) {
	rawHours, err1 := strconv.Atoi(r.PostFormValue("raw_hours"))
	aggDays, err2 := strconv.Atoi(r.PostFormValue("aggregate_days"))
	ttl, err3 := time.ParseDuration(r.PostFormValue("session_ttl"))
	d := settings.Data{
		Public:         lastValue(r, "public") == "true",
		RawHours:       rawHours,
		AggregateDays:  aggDays,
		AggregatesOnly: lastValue(r, "aggregates_only") == "true",
		SessionTTL:     ttl,
	}
	if _, ok := d.Validate(); err1 != nil || err2 != nil || err3 != nil || !ok {
		data := pageData{Title: s.T(r, "settings.title"), Active: "settings", Body: s.settingsData(r, nil, "")}
		v := data.Body.(settingsView)
		v.Data.Error = s.T(r, "settings.data.invalid")
		data.Body = v
		s.render(w, r, http.StatusUnprocessableEntity, "settings", data)
		return
	}
	if err := s.Settings.SetData(r.Context(), d); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Tracker.SetAggregatesOnly(d.AggregatesOnly)
	s.Log.Info("data settings saved", "public", d.Public, "raw_hours", d.RawHours,
		"aggregate_days", d.AggregateDays, "aggregates_only", d.AggregatesOnly, "session_ttl", d.SessionTTL.String())
	redirect(w, r, "/admin/settings?m=data")
}

// lastValue devuelve el último valor enviado de un campo (las casillas van
// precedidas de un campo oculto «false»).
func lastValue(r *http.Request, name string) string {
	vs := r.PostForm[name]
	if len(vs) == 0 {
		return ""
	}
	return vs[len(vs)-1]
}
