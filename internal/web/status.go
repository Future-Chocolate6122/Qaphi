package web

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/checks"
	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/settings"
	"github.com/future-chocolate6122/qaphi/internal/state"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

// statusAuth da acceso a las páginas de estado: con sesión, completas; sin
// sesión y con la página pública activada, sin destinos ni detalles de
// errores; sin sesión ni página pública, al login.
func (s *Server) statusAuth(next func(w http.ResponseWriter, r *http.Request, full bool)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sess, ok := s.currentSession(r); ok {
			next(w, withSession(r, sess), true)
			return
		}
		if s.Settings.Data().Public {
			next(w, r, false)
			return
		}
		redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()))
	})
}

// ---------------------------------------------------------------- vistas

type beatView struct {
	S model.Status `json:"s"` // estado (color)
	H float64      `json:"h"` // altura relativa (0–1) según el tiempo de respuesta
	T string       `json:"t"` // texto del tooltip
}

type rowView struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Target      string       `json:"target,omitempty"`
	TypeName    string       `json:"-"` // clave de texto
	Group       string       `json:"group,omitempty"`
	Interval    string       `json:"interval,omitempty"`
	Status      model.Status `json:"status"`
	StatusLabel string       `json:"status_label"`
	Latency     string       `json:"latency"`
	Last        string       `json:"last"`
	LastTitle   string       `json:"last_title"`
	Message     string       `json:"message,omitempty"`
	Uptime      string       `json:"uptime"`
	UptimeLevel string       `json:"uptime_level"`
	Beats       []beatView   `json:"beats"`
}

type groupView struct {
	Name string    `json:"name"`
	Rows []rowView `json:"checks"`
}

type incidentView struct {
	CheckID  int64  `json:"check_id"`
	Check    string `json:"check"`
	Start    string `json:"start"`
	Duration string `json:"duration"`
	Open     bool   `json:"open"`
	Error    string `json:"error,omitempty"`
}

type homeView struct {
	Summary    string         `json:"summary"`
	Level      model.Status   `json:"level"`
	Down       int            `json:"down"`
	Groups     []groupView    `json:"groups"`
	Incidents  []incidentView `json:"incidents"`
	Full       bool           `json:"-"`
	Grid       bool           `json:"-"` // vista de cuadrícula (cookie del visitante)
	Components []string       `json:"-"`
}

// viewCookie guarda en el navegador la vista elegida en /home («list» o
// «grid»). No es un dato sensible: solo decide cómo se pinta la página.
const viewCookie = "qaphi_view"

// uptimeText formatea un uptime (fracción) según el idioma: «99,95 %».
func uptimeText(l msg.Lang, v float64) string {
	if v < 0 {
		return msg.T(l, "uptime.none")
	}
	pct := v * 100
	prec := 2
	if pct == 100 || pct == 0 {
		prec = 0
	}
	txt := strconv.FormatFloat(math.Floor(pct*100)/100, 'f', prec, 64)
	if l == msg.EN {
		return txt + "%"
	}
	return decimal(l, txt) + " %"
}

// uptimeLevel clasifica un uptime para colorearlo.
func uptimeLevel(v float64) string {
	switch {
	case v < 0:
		return "unknown"
	case v >= 0.999:
		return "up"
	case v >= 0.95:
		return "degraded"
	default:
		return "down"
	}
}

// beats prepara la barra de pulsos: la altura de cada barra es proporcional
// al tiempo de respuesta (respecto al máximo de la serie); las caídas, a
// altura completa.
func (s *Server) beats(l msg.Lang, rs []model.Result, full bool) []beatView {
	loc := s.Settings.Location()
	var maxLat time.Duration
	for _, r := range rs {
		if r.OK && r.Latency > maxLat {
			maxLat = r.Latency
		}
	}
	out := make([]beatView, 0, len(rs))
	for _, r := range rs {
		b := beatView{S: r.State, H: 1}
		if r.OK && maxLat > 0 {
			b.H = 0.35 + 0.65*float64(r.Latency)/float64(maxLat)
		}
		detail := msg.T(l, "status."+string(r.State))
		if r.OK {
			detail = formatLatency(l, r.Latency)
		}
		if full {
			if m := msg.Decode(r.Message).In(l); m != "" {
				detail += " · " + m
			}
		}
		b.T = msg.F(l, "beat.tip", formatDateTime(l, loc, r.At), detail)
		out = append(out, b)
	}
	return out
}

// row construye la fila de un check.
func (s *Server) row(l msg.Lang, c model.Check, st state.CheckState, full bool) rowView {
	row := rowView{
		ID: c.ID, Name: c.Name, TypeName: c.Type,
		Status: model.StatusUnknown, Latency: "—", Last: msg.T(l, "time.never"),
	}
	if e, ok := checks.Registry.Lookup(c.Type); ok {
		row.TypeName = e.Info.Name
		if full {
			row.Target = e.Impl.Target(c.Params)
		}
	}
	row.Status = st.Status
	if st.HasLast {
		if st.Last.OK {
			row.Latency = formatLatency(l, st.Last.Latency)
		}
		row.Last = ago(l, s.Now(), st.Last.At)
		row.LastTitle = formatDateTime(l, s.Settings.Location(), st.Last.At)
		if full {
			row.Message = msg.Decode(st.Last.Message).In(l)
		}
	}
	if c.Paused {
		row.Status = model.StatusPaused
	}
	if row.Status == "" {
		row.Status = model.StatusUnknown
	}
	row.StatusLabel = msg.T(l, "status."+string(row.Status))
	up := s.Tracker.Uptime(c.ID)
	row.Uptime, row.UptimeLevel = uptimeText(l, up[0]), uptimeLevel(up[0])
	row.Beats = s.beats(l, st.Recent, full)
	return row
}

// humanDuration: «45 s», «12 min», «3 h 5 min», «2 d 4 h».
func humanDuration(l msg.Lang, d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return msg.F(l, "dur.s", int64(d/time.Second))
	case d < time.Hour:
		return msg.F(l, "dur.m", int64(d/time.Minute))
	case d < 24*time.Hour:
		return msg.F(l, "dur.hm", int64(d/time.Hour), int64(d%time.Hour/time.Minute))
	default:
		return msg.F(l, "dur.dh", int64(d/(24*time.Hour)), int64(d%(24*time.Hour)/time.Hour))
	}
}

func (s *Server) incidents(ctx context.Context, l msg.Lang, checkID int64, names map[int64]string, full bool) ([]incidentView, error) {
	list, err := s.Store.ListIncidents(ctx, checkID, 10)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	out := make([]incidentView, 0, len(list))
	for _, i := range list {
		name, ok := names[i.CheckID]
		if !ok {
			continue
		}
		v := incidentView{
			CheckID: i.CheckID, Check: name, Open: i.Open(),
			Start:    formatDateTime(l, s.Settings.Location(), i.Start),
			Duration: humanDuration(l, i.Duration(now)),
		}
		if full {
			v.Error = msg.Decode(i.LastError).In(l)
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) homeData(r *http.Request, full bool) (homeView, error) {
	cs, err := s.Store.ListChecks(r.Context())
	if err != nil {
		return homeView{}, err
	}
	l := s.lang(r)
	snap := s.Tracker.Snapshot()
	sum := state.Summarize(cs, snap)
	v := homeView{Summary: sum.In(l), Level: sum.Level(), Down: sum.Down, Full: full, Components: pageComponents("home")}
	if c, err := r.Cookie(viewCookie); err == nil && c.Value == "grid" {
		v.Grid = true
	}

	// Grupos en orden alfabético; «sin grupo» al final.
	byGroup := map[string][]rowView{}
	names := map[int64]string{}
	for _, c := range cs {
		names[c.ID] = c.Name
		byGroup[c.Group] = append(byGroup[c.Group], s.row(l, c, snap[c.ID], full))
	}
	groups := make([]string, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if (groups[i] == "") != (groups[j] == "") {
			return groups[j] == ""
		}
		return groups[i] < groups[j]
	})
	for _, g := range groups {
		name := g
		if name == "" {
			name = msg.T(l, "group.none")
		}
		v.Groups = append(v.Groups, groupView{Name: name, Rows: byGroup[g]})
	}
	if v.Incidents, err = s.incidents(r.Context(), l, 0, names, full); err != nil {
		return homeView{}, err
	}
	return v, nil
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request, full bool) {
	v, err := s.homeData(r, full)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "home", pageData{
		Title: s.T(r, "home.title"), Active: "home", Live: "/home/state.json", Body: v,
	})
}

// handleHomeState devuelve el modelo de datos de /home en JSON, ya traducido
// y formateado: el JavaScript de la página solo tiene que dibujarlo.
func (s *Server) handleHomeState(w http.ResponseWriter, r *http.Request, full bool) {
	v, err := s.homeData(r, full)
	if err != nil {
		s.Log.Error("home state failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

func writeJSON(w http.ResponseWriter, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// ---------------------------------------------------------------- detalle

type uptimeView struct {
	Label string  `json:"label"`
	Text  string  `json:"text"`
	Value float64 `json:"value"`
	Level string  `json:"level"`
}

type pointView struct {
	T   int64        `json:"t"` // marca de tiempo (ms)
	V   float64      `json:"v"` // tiempo de respuesta (ms); 0 si no hay
	S   model.Status `json:"s"`
	Tip string       `json:"tip"`
}

type dayView struct {
	D     string  `json:"d"` // fecha AAAA-MM-DD (zona configurada)
	V     float64 `json:"v"` // uptime del día; -1 sin datos
	Level string  `json:"level"`
	Tip   string  `json:"tip"`
}

type rangeView struct {
	Key, Label string
	Active     bool
}

type checkView struct {
	rowView
	Uptimes    []uptimeView   `json:"uptimes"`
	Cert       string         `json:"cert,omitempty"`
	CertLevel  string         `json:"cert_level,omitempty"`
	Range      string         `json:"range"`
	From       int64          `json:"from"` // inicio del rango (ms)
	To         int64          `json:"to"`   // fin del rango (ms)
	TZ         string         `json:"tz"`   // zona horaria IANA para las etiquetas de la gráfica
	Lang       msg.Lang       `json:"lang"`
	Ranges     []rangeView    `json:"-"`
	Avg        string         `json:"avg"`
	Points     []pointView    `json:"points"`
	Heatmap    []dayView      `json:"heatmap"`
	Incidents  []incidentView `json:"incidents"`
	Full       bool           `json:"-"`
	Components []string       `json:"-"`
}

// Rangos de la gráfica.
var chartRanges = []struct {
	Key string
	Dur time.Duration
}{{"1h", time.Hour}, {"24h", 24 * time.Hour}, {"7d", 7 * 24 * time.Hour}, {"30d", 30 * 24 * time.Hour}}

func (s *Server) checkData(r *http.Request, full bool) (checkView, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return checkView{}, store.ErrNotFound
	}
	c, err := s.Store.GetCheck(r.Context(), id)
	if err != nil {
		return checkView{}, err
	}
	l := s.lang(r)
	st, _ := s.Tracker.Get(c.ID)
	v := checkView{rowView: s.row(l, c, st, full), Full: full, Components: pageComponents("check")}
	v.Group = c.Group
	v.Interval = msg.F(l, "detail.interval", shortDuration(c.Interval))
	for i, up := range s.Tracker.Uptime(c.ID) {
		key := []string{"uptime.24h", "uptime.7d", "uptime.30d"}[i]
		v.Uptimes = append(v.Uptimes, uptimeView{Label: msg.T(l, key), Text: uptimeText(l, up), Value: up, Level: uptimeLevel(up)})
	}
	if !st.CertExpiry.IsZero() {
		left := st.CertExpiry.Sub(s.Now())
		date := formatDate(l, s.Settings.Location(), st.CertExpiry)
		switch {
		case left <= 0:
			v.Cert, v.CertLevel = msg.F(l, "detail.cert.expired", date), "down"
		default:
			days := int(left / (24 * time.Hour))
			v.Cert, v.CertLevel = msg.F(l, "detail.cert.expires", date, days), "up"
			if days < 14 {
				v.CertLevel = "degraded"
			}
		}
	}
	v.Range = "24h"
	if q := r.URL.Query().Get("range"); q != "" {
		v.Range = q
	}
	found := false
	for _, cr := range chartRanges {
		active := cr.Key == v.Range
		found = found || active
		v.Ranges = append(v.Ranges, rangeView{Key: cr.Key, Label: msg.T(l, "range."+cr.Key), Active: active})
	}
	if !found {
		v.Range = "24h"
		v.Ranges[1].Active = true
	}
	for _, cr := range chartRanges {
		if cr.Key == v.Range {
			v.From, v.To = s.Now().Add(-cr.Dur).UnixMilli(), s.Now().UnixMilli()
		}
	}
	v.Lang = l
	v.TZ = s.Settings.UI().Timezone
	if v.TZ == "" {
		v.TZ = settings.SystemTimezone()
	}
	if v.Points, v.Avg, err = s.chart(r.Context(), l, c, st, v.Range); err != nil {
		return checkView{}, err
	}
	if v.Heatmap, err = s.heatmap(r.Context(), l, c); err != nil {
		return checkView{}, err
	}
	if v.Incidents, err = s.incidents(r.Context(), l, c.ID, map[int64]string{c.ID: c.Name}, full); err != nil {
		return checkView{}, err
	}
	return v, nil
}

// chart prepara los puntos de la gráfica de respuesta del rango pedido:
//   - 1 h: cada pulso (resultados en bruto, o los de memoria en el modo
//     «solo agregados»);
//   - 24 h: intervalos de 5 minutos (o por hora en el modo «solo agregados»);
//   - 7 d: por hora; 30 d: intervalos de 4 horas.
func (s *Server) chart(ctx context.Context, l msg.Lang, c model.Check, st state.CheckState, rng string) ([]pointView, string, error) {
	loc := s.Settings.Location()
	now := s.Now()
	aggOnly := s.Settings.Data().AggregatesOnly
	var (
		pts      []pointView
		sum      time.Duration
		sumCount int64
	)
	addBeat := func(r model.Result) {
		p := pointView{T: r.At.UnixMilli(), S: r.State}
		detail := msg.T(l, "status."+string(r.State))
		if r.OK {
			p.V = float64(r.Latency.Microseconds()) / 1000
			detail = formatLatency(l, r.Latency)
			sum += r.Latency
			sumCount++
		}
		p.Tip = msg.F(l, "beat.tip", formatDateTime(l, loc, r.At), detail)
		pts = append(pts, p)
	}
	addAgg := func(start time.Time, total, down, degraded int64, avg time.Duration, latCount int64) {
		if total == 0 {
			return
		}
		p := pointView{T: start.UnixMilli(), S: model.StatusUp}
		switch {
		case down > 0:
			p.S = model.StatusDown
		case degraded > 0:
			p.S = model.StatusDegraded
		}
		label := formatDateTime(l, loc, start)
		if avg > 0 {
			p.V = float64(avg.Microseconds()) / 1000
			sum += avg * time.Duration(latCount)
			sumCount += latCount
		}
		p.Tip = msg.F(l, "beat.tip", label, formatLatency(l, avg))
		if down > 0 {
			p.Tip = msg.F(l, "chart.tip.failed", p.Tip, down*100/total)
		}
		pts = append(pts, p)
	}
	hourlyPoints := func(from time.Time, group int) error {
		hs, err := s.Store.HourlyRange(ctx, c.ID, from, now.Add(time.Hour))
		if err != nil {
			return err
		}
		hs = append(hs, s.Tracker.PendingHourly(c.ID)...)
		merged := map[int64]*model.Hourly{}
		var keys []int64
		for _, h := range hs {
			if h.Hour.Before(from) {
				continue
			}
			k := from.Add(h.Hour.Sub(from) / (time.Duration(group) * time.Hour) * (time.Duration(group) * time.Hour)).Unix()
			if merged[k] == nil {
				merged[k] = &model.Hourly{Hour: time.Unix(k, 0)}
				keys = append(keys, k)
			}
			merged[k].Merge(h)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, k := range keys {
			h := merged[k]
			addAgg(h.Hour, h.Total, h.Down, h.Degraded, h.AvgLatency(), h.LatCount)
		}
		return nil
	}

	switch rng {
	case "1h":
		from := now.Add(-time.Hour)
		var rs []model.Result
		if aggOnly {
			rs = st.Recent
		} else {
			var err error
			if rs, err = s.Store.ResultsRange(ctx, c.ID, from, now.Add(time.Minute)); err != nil {
				return nil, "", err
			}
			rs = append(rs, s.Tracker.PendingResults(c.ID)...)
		}
		sort.Slice(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
		for _, r := range rs {
			if !r.At.Before(from) {
				addBeat(r)
			}
		}
	case "24h":
		from := now.Add(-24 * time.Hour)
		if aggOnly {
			if err := hourlyPoints(from.Truncate(time.Hour), 1); err != nil {
				return nil, "", err
			}
			break
		}
		step := 5 * time.Minute
		from = from.Truncate(step)
		bs, err := s.Store.ResultBuckets(ctx, c.ID, from, now.Add(time.Minute), step)
		if err != nil {
			return nil, "", err
		}
		// Añadir los resultados aún no volcados a sus intervalos.
		type acc struct {
			b      model.Bucket
			latSum time.Duration
		}
		m := map[int64]*acc{}
		for _, b := range bs {
			m[b.Start.Unix()] = &acc{b: b, latSum: b.AvgLat * time.Duration(b.OK)}
		}
		for _, r := range s.Tracker.PendingResults(c.ID) {
			if r.At.Before(from) || r.State == model.StatusNoPerm {
				continue
			}
			start := from.Add(r.At.Sub(from) / step * step)
			a := m[start.Unix()]
			if a == nil {
				a = &acc{b: model.Bucket{Start: start}}
				m[start.Unix()] = a
			}
			a.b.Total++
			if r.State == model.StatusDown {
				a.b.Down++
			}
			if r.State == model.StatusDegraded {
				a.b.Degraded++
			}
			if r.OK {
				a.b.OK++
				a.latSum += r.Latency
			}
		}
		keys := make([]int64, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, k := range keys {
			a := m[k]
			var avg time.Duration
			if a.b.OK > 0 {
				avg = a.latSum / time.Duration(a.b.OK)
			}
			addAgg(a.b.Start, a.b.Total, a.b.Down, a.b.Degraded, avg, a.b.OK)
		}
	case "7d":
		if err := hourlyPoints(now.Add(-7*24*time.Hour).Truncate(time.Hour), 1); err != nil {
			return nil, "", err
		}
	case "30d":
		if err := hourlyPoints(now.Add(-30*24*time.Hour).Truncate(4*time.Hour), 4); err != nil {
			return nil, "", err
		}
	}
	avg := "—"
	if sumCount > 0 {
		avg = formatLatency(l, sum/time.Duration(sumCount))
	}
	return pts, avg, nil
}

// heatmap calcula el uptime de cada uno de los últimos 90 días (en la zona
// horaria configurada) a partir de los agregados por hora.
func (s *Server) heatmap(ctx context.Context, l msg.Lang, c model.Check) ([]dayView, error) {
	loc := s.Settings.Location()
	now := s.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	first := today.AddDate(0, 0, -89)
	hs, err := s.Store.HourlyRange(ctx, c.ID, first.UTC().Truncate(time.Hour), now.Add(time.Hour))
	if err != nil {
		return nil, err
	}
	hs = append(hs, s.Tracker.PendingHourly(c.ID)...)
	type agg struct{ total, down int64 }
	days := map[string]*agg{}
	for _, h := range hs {
		d := h.Hour.In(loc).Format("2006-01-02")
		if days[d] == nil {
			days[d] = &agg{}
		}
		days[d].total += h.Total
		days[d].down += h.Down
	}
	out := make([]dayView, 0, 90)
	for d := first; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		v := dayView{D: key, V: -1, Level: "unknown"}
		label := formatDate(l, loc, d)
		if a := days[key]; a != nil && a.total > 0 {
			v.V = 1 - float64(a.down)/float64(a.total)
			v.Level = uptimeLevel(v.V)
			v.Tip = label + " · " + uptimeText(l, v.V)
		} else {
			v.Tip = label + " · " + msg.T(l, "detail.heatmap.nodata")
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) handleCheckPage(w http.ResponseWriter, r *http.Request, full bool) {
	v, err := s.checkData(r, full)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "http.notfound")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "check", pageData{
		Title: v.Name, Active: "home", Live: "/check/" + strconv.FormatInt(v.ID, 10) + "/data.json?range=" + v.Range, Body: v,
	})
}

func (s *Server) handleCheckData(w http.ResponseWriter, r *http.Request, full bool) {
	v, err := s.checkData(r, full)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.Log.Error("check data failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

// ---------------------------------------------------------------- heartbeats

// handleHeartbeat recibe el pulso de un heartbeat. Responde 404 si el token
// no corresponde a ningún check, sin dar más pistas.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, ok := checks.Heartbeats.Ping(r.PathValue("token"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.Scheduler.Trigger(id)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}

// SyncHeartbeats actualiza los tokens válidos de /hb/ a partir de los checks.
func SyncHeartbeats(ctx context.Context, st store.Store) error {
	cs, err := st.ListChecks(ctx)
	if err != nil {
		return err
	}
	tokens := map[string]int64{}
	for _, c := range cs {
		if c.Type == "heartbeat" && c.Params["token"] != "" {
			tokens[c.Params["token"]] = c.ID
		}
	}
	checks.Heartbeats.Sync(tokens)
	return nil
}
