package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/future-chocolate6122/qaphi/internal/checks"
	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/state"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

// checkRow es una fila de las tablas de /home y /admin, ya traducida. Es
// también lo que se envía en el JSON de actualización en vivo.
type checkRow struct {
	ID          int64        `json:"id"`
	Name        string       `json:"-"`
	Group       string       `json:"-"`
	TypeName    string       `json:"-"` // clave de texto
	Target      string       `json:"-"`
	Status      model.Status `json:"status"`
	StatusLabel string       `json:"status_label"`
	Latency     string       `json:"latency"`
	Last        string       `json:"last"`
	LastTitle   string       `json:"last_title"` // fecha y hora absolutas
	Message     string       `json:"message"`
	Interval    string       `json:"-"`
}

func (s *Server) rows(l msg.Lang, cs []model.Check, snap map[int64]state.CheckState) []checkRow {
	now := s.Now()
	loc := s.Settings.Location()
	out := make([]checkRow, 0, len(cs))
	for _, c := range cs {
		row := checkRow{
			ID: c.ID, Name: c.Name, Group: c.Group, TypeName: c.Type,
			Status: model.StatusUnknown, Latency: "—", Last: msg.T(l, "time.never"),
			Interval: shortDuration(c.Interval),
		}
		if e, ok := checks.Registry.Lookup(c.Type); ok {
			row.TypeName = e.Info.Name
			row.Target = e.Impl.Target(c.Params)
		}
		if st, ok := snap[c.ID]; ok {
			row.Status = st.Status
			if st.HasLast {
				row.Latency = formatLatency(l, st.Last.Latency)
				row.Last = ago(l, now, st.Last.At)
				row.LastTitle = formatDateTime(l, loc, st.Last.At)
				row.Message = msg.Decode(st.Last.Message).In(l)
			}
		}
		if c.Paused {
			row.Status = model.StatusPaused
		}
		row.StatusLabel = msg.T(l, "status."+string(row.Status))
		out = append(out, row)
	}
	return out
}

type homeView struct {
	Summary string       `json:"summary"`
	Level   model.Status `json:"level"`
	Rows    []checkRow   `json:"checks"`
}

func (s *Server) homeData(r *http.Request) (homeView, error) {
	cs, err := s.Store.ListChecks(r.Context())
	if err != nil {
		return homeView{}, err
	}
	l := s.lang(r)
	snap := s.Tracker.Snapshot()
	sum := state.Summarize(cs, snap)
	return homeView{Summary: sum.In(l), Level: sum.Level(), Rows: s.rows(l, cs, snap)}, nil
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	v, err := s.homeData(r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "home", pageData{
		Title: s.T(r, "home.title"), Active: "home", Live: "/home/state.json", Body: v,
	})
}

// handleHomeState devuelve el estado de /home en JSON, ya traducido y
// formateado: el JavaScript de la página solo tiene que colocarlo.
func (s *Server) handleHomeState(w http.ResponseWriter, r *http.Request) {
	v, err := s.homeData(r)
	if err != nil {
		s.Log.Error("home state failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
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
	l := s.lang(r)
	s.render(w, r, http.StatusOK, "admin", pageData{
		Title: msg.T(l, "admin.title"), Active: "admin", Flash: flash(l, r.URL.Query()),
		Body: adminView{Rows: s.rows(l, cs, s.Tracker.Snapshot()), Types: checkTypes()},
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
	l := s.lang(r)
	typ := r.URL.Query().Get("type")
	e, ok := checks.Registry.Lookup(typ)
	if !ok {
		s.render(w, r, http.StatusOK, "check_type", pageData{
			Title: msg.T(l, "admin.choose_type"), Active: "admin", Body: checkTypes(),
		})
		return
	}
	s.renderCheckForm(w, r, http.StatusOK, msg.F(l, "check.new.title", msg.T(l, e.Info.Name)), checkFormView{
		Action: "/admin/checks", TypeID: typ, TypeName: e.Info.Name,
		Common: buildFields(l, commonInfo, "", nil, nil, nil),
		Params: buildFields(l, e.Info, paramPrefix, nil, nil, nil),
	})
}

// parseCheck valida el formulario. Si hay errores, devuelve la vista del
// formulario con los valores introducidos para volver a mostrarla.
func (s *Server) parseCheck(r *http.Request, e module.Entry[checks.Checker], saved module.Values) (model.Check, *checkFormView) {
	l := s.lang(r)
	rawCommon := readValues(r.PostForm, commonInfo, "", nil)
	rawParams := readValues(r.PostForm, e.Info, paramPrefix, saved)

	common, cerrs := commonInfo.Normalize(rawCommon)
	params, perrs := e.Info.Normalize(rawParams)
	if perrs == nil {
		perrs = e.Impl.Validate(params)
	}
	if cerrs == nil && perrs == nil {
		if timeout := e.Impl.Timeout(params); timeout >= common.Duration("interval") {
			cerrs = module.Errors{"interval": msg.New("check.err.interval_timeout")}
		}
	}
	if cerrs != nil || perrs != nil {
		return model.Check{}, &checkFormView{
			TypeID: e.Info.ID, TypeName: e.Info.Name, HasError: true,
			Common: buildFields(l, commonInfo, "", rawCommon, cerrs, nil),
			Params: buildFields(l, e.Info, paramPrefix, rawParams, perrs, saved),
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
	l := s.lang(r)
	c, fv := s.parseCheck(r, e, nil)
	if fv != nil {
		fv.Action = "/admin/checks"
		s.renderCheckForm(w, r, http.StatusUnprocessableEntity, msg.F(l, "check.new.title", msg.T(l, e.Info.Name)), *fv)
		return
	}
	if err := s.Store.CreateCheck(r.Context(), &c); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Tracker.SetPaused(c.ID, c.Paused)
	s.Scheduler.Set(c)
	s.Log.Info("check created", "check", c.Name, "type", c.Type)
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
	l := s.lang(r)
	common := module.Values{
		"name": c.Name, "group": c.Group, "interval": c.Interval.String(), "paused": strconv.FormatBool(c.Paused),
	}
	s.renderCheckForm(w, r, http.StatusOK, msg.F(l, "check.edit.title", c.Name), checkFormView{
		Action: fmt.Sprintf("/admin/checks/%d", c.ID), TypeID: c.Type, TypeName: e.Info.Name,
		Common: buildFields(l, commonInfo, "", common, nil, nil),
		Params: buildFields(l, e.Info, paramPrefix, c.Params, nil, c.Params),
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
		s.renderCheckForm(w, r, http.StatusUnprocessableEntity, msg.F(s.lang(r), "check.edit.title", old.Name), *fv)
		return
	}
	c.ID, c.CreatedAt = old.ID, old.CreatedAt
	if err := s.Store.UpdateCheck(r.Context(), &c); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Tracker.SetPaused(c.ID, c.Paused)
	s.Scheduler.Set(c)
	s.Log.Info("check updated", "check", c.Name)
	redirect(w, r, "/admin?m=updated")
}

func (s *Server) handleCheckDeleteForm(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.loadCheck(w, r)
	if !ok {
		return
	}
	s.render(w, r, http.StatusOK, "check_delete", pageData{Title: s.T(r, "delete.title"), Active: "admin", Body: c})
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
	s.Log.Info("check deleted", "check", c.Name)
	redirect(w, r, "/admin?m=deleted")
}
