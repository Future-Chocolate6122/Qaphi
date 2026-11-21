package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/checks"
	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/state"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

// checkRow es una fila de las tablas de /home y /admin.
type checkRow struct {
	ID          int64
	Name        string
	Group       string
	TypeName    string // clave de texto
	Target      string
	Status      model.Status
	StatusLabel string // clave de texto
	Latency     string
	Last        string
	Message     string
	Interval    string
}

func (s *Server) rows(cs []model.Check, snap map[int64]state.CheckState) []checkRow {
	now := s.Now()
	out := make([]checkRow, 0, len(cs))
	for _, c := range cs {
		row := checkRow{
			ID: c.ID, Name: c.Name, Group: c.Group, TypeName: c.Type,
			Status: model.StatusUnknown, Latency: "—", Last: msg.T("time.never"),
			Interval: shortDuration(c.Interval),
		}
		if e, ok := checks.Registry.Lookup(c.Type); ok {
			row.TypeName = e.Info.Name
			row.Target = e.Impl.Target(c.Params)
		}
		if st, ok := snap[c.ID]; ok {
			row.Status = st.Status
			if st.HasLast {
				row.Latency = formatLatency(st.Last.Latency)
				row.Last = ago(now, st.Last.At)
				row.Message = st.Last.Message
			}
		}
		if c.Paused {
			row.Status = model.StatusPaused
		}
		row.StatusLabel = "status." + string(row.Status)
		out = append(out, row)
	}
	return out
}

type homeView struct {
	Summary string
	Level   model.Status
	Rows    []checkRow
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Store.ListChecks(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	snap := s.Tracker.Snapshot()
	sum := state.Summarize(cs, snap)
	s.render(w, r, http.StatusOK, "home", pageData{
		Title: msg.T("home.title"), Active: "home", Refresh: 30,
		Body: homeView{Summary: sum.String(), Level: sum.Level(), Rows: s.rows(cs, snap)},
	})
}

type adminView struct {
	Rows  []checkRow
	Types []module.Info
}

func checkTypes() []module.Info {
	var out []module.Info
	for _, e := range checks.Registry.List() {
		out = append(out, e.Info)
	}
	return out
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Store.ListChecks(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin", pageData{
		Title: msg.T("admin.title"), Active: "admin", Flash: flash(r.URL.Query()),
		Body: adminView{Rows: s.rows(cs, s.Tracker.Snapshot()), Types: checkTypes()},
	})
}

// checkFormView son los datos del formulario de un check.
type checkFormView struct {
	Action   string
	TypeID   string
	TypeName string
	Common   []fieldView
	Params   []fieldView
	HasError bool
}

func (s *Server) renderCheckForm(w http.ResponseWriter, r *http.Request, status int, title string, v checkFormView) {
	s.render(w, r, status, "check_form", pageData{Title: title, Active: "admin", Body: v})
}

func (s *Server) handleCheckNew(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	e, ok := checks.Registry.Lookup(typ)
	if !ok {
		s.render(w, r, http.StatusOK, "check_type", pageData{
			Title: msg.T("admin.choose_type"), Active: "admin", Body: checkTypes(),
		})
		return
	}
	s.renderCheckForm(w, r, http.StatusOK, msg.F("check.new.title", msg.T(e.Info.Name)), checkFormView{
		Action: "/admin/checks", TypeID: typ, TypeName: e.Info.Name,
		Common: buildFields(commonInfo, "", nil, nil, nil),
		Params: buildFields(e.Info, paramPrefix, nil, nil, nil),
	})
}

// parseCheck valida el formulario. Si hay errores, devuelve la vista del
// formulario con los valores introducidos para volver a mostrarla.
func (s *Server) parseCheck(r *http.Request, e module.Entry[checks.Checker], saved module.Values) (model.Check, *checkFormView) {
	rawCommon := readValues(r.PostForm, commonInfo, "", nil)
	rawParams := readValues(r.PostForm, e.Info, paramPrefix, saved)

	common, cerrs := commonInfo.Normalize(rawCommon)
	params, perrs := e.Info.Normalize(rawParams)
	if perrs == nil {
		perrs = e.Impl.Validate(params)
	}
	if cerrs == nil && perrs == nil {
		if timeout := e.Impl.Timeout(params); timeout >= common.Duration("interval") {
			cerrs = module.Errors{"interval": msg.T("check.err.interval_timeout")}
		}
	}
	if cerrs != nil || perrs != nil {
		return model.Check{}, &checkFormView{
			TypeID: e.Info.ID, TypeName: e.Info.Name, HasError: true,
			Common: buildFields(commonInfo, "", rawCommon, cerrs, nil),
			Params: buildFields(e.Info, paramPrefix, rawParams, perrs, saved),
		}
	}
	return model.Check{
		Name:     common.String("name"),
		Group:    common.String("group"),
		Interval: common.Duration("interval"),
		Paused:   common.Bool("paused"),
		Type:     e.Info.ID,
		Params:   params,
	}, nil
}

func (s *Server) handleCheckCreate(w http.ResponseWriter, r *http.Request) {
	e, ok := checks.Registry.Lookup(r.PostFormValue("type"))
	if !ok {
		s.renderError(w, r, http.StatusBadRequest, "http.notfound")
		return
	}
	c, fv := s.parseCheck(r, e, nil)
	if fv != nil {
		fv.Action = "/admin/checks"
		s.renderCheckForm(w, r, http.StatusUnprocessableEntity, msg.F("check.new.title", msg.T(e.Info.Name)), *fv)
		return
	}
	if err := s.Store.CreateCheck(r.Context(), &c); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Tracker.SetPaused(c.ID, c.Paused)
	s.Scheduler.Set(c)
	s.Log.Info("check creado", "check", c.Name, "tipo", c.Type)
	redirect(w, r, "/admin?m=created")
}

// loadCheck lee el check de la ruta; responde 404 si no existe.
func (s *Server) loadCheck(w http.ResponseWriter, r *http.Request) (model.Check, module.Entry[checks.Checker], bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.renderError(w, r, http.StatusNotFound, "http.notfound")
		return model.Check{}, module.Entry[checks.Checker]{}, false
	}
	c, err := s.Store.GetCheck(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "http.notfound")
		return model.Check{}, module.Entry[checks.Checker]{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return model.Check{}, module.Entry[checks.Checker]{}, false
	}
	e, ok := checks.Registry.Lookup(c.Type)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "http.notfound")
		return model.Check{}, module.Entry[checks.Checker]{}, false
	}
	return c, e, true
}

func (s *Server) handleCheckEdit(w http.ResponseWriter, r *http.Request) {
	c, e, ok := s.loadCheck(w, r)
	if !ok {
		return
	}
	common := module.Values{
		"name": c.Name, "group": c.Group, "interval": c.Interval.String(), "paused": strconv.FormatBool(c.Paused),
	}
	s.renderCheckForm(w, r, http.StatusOK, msg.F("check.edit.title", c.Name), checkFormView{
		Action: fmt.Sprintf("/admin/checks/%d", c.ID), TypeID: c.Type, TypeName: e.Info.Name,
		Common: buildFields(commonInfo, "", common, nil, nil),
		Params: buildFields(e.Info, paramPrefix, c.Params, nil, c.Params),
	})
}

func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	old, e, ok := s.loadCheck(w, r)
	if !ok {
		return
	}
	c, fv := s.parseCheck(r, e, old.Params)
	if fv != nil {
		fv.Action = fmt.Sprintf("/admin/checks/%d", old.ID)
		s.renderCheckForm(w, r, http.StatusUnprocessableEntity, msg.F("check.edit.title", old.Name), *fv)
		return
	}
	c.ID, c.CreatedAt = old.ID, old.CreatedAt
	if err := s.Store.UpdateCheck(r.Context(), &c); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Tracker.SetPaused(c.ID, c.Paused)
	s.Scheduler.Set(c)
	s.Log.Info("check modificado", "check", c.Name)
	redirect(w, r, "/admin?m=updated")
}

func (s *Server) handleCheckDeleteForm(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.loadCheck(w, r)
	if !ok {
		return
	}
	s.render(w, r, http.StatusOK, "check_delete", pageData{Title: msg.T("delete.title"), Active: "admin", Body: c})
}

func (s *Server) handleCheckDelete(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.loadCheck(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteCheck(r.Context(), c.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Scheduler.Remove(c.ID)
	s.Tracker.Remove(c.ID)
	s.Log.Info("check borrado", "check", c.Name)
	redirect(w, r, "/admin?m=deleted")
}

// ---------------------------------------------------------------- formato

// formatLatency: «0.4 ms», «12 ms», «1.2 s».
func formatLatency(d time.Duration) string {
	switch {
	case d <= 0:
		return "—"
	case d < 10*time.Millisecond:
		return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000)
	case d < 10*time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	default:
		return fmt.Sprintf("%.1f s", d.Seconds())
	}
}

// ago: «hace 5 s», «hace 3 min»…
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case t.IsZero():
		return msg.T("time.never")
	case d < time.Second:
		return msg.T("time.now")
	case d < time.Minute:
		return msg.F("time.ago.s", int64(d/time.Second))
	case d < time.Hour:
		return msg.F("time.ago.m", int64(d/time.Minute))
	case d < 48*time.Hour:
		return msg.F("time.ago.h", int64(d/time.Hour))
	default:
		return msg.F("time.ago.d", int64(d/(24*time.Hour)))
	}
}

// shortDuration: «1m0s» → «1m», «1h30m0s» → «1h30m».
func shortDuration(d time.Duration) string {
	s := d.String()
	for _, suf := range []string{"m0s", "h0m"} {
		if len(s) > len(suf) && s[len(s)-len(suf):] == suf {
			s = s[:len(s)-2]
		}
	}
	return s
}
