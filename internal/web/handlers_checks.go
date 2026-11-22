package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/future-chocolate6122/qaphi/internal/checks"
	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/store"
)

type adminView struct {
	Rows  []rowView
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
	snap := s.Tracker.Snapshot()
	rows := make([]rowView, 0, len(cs))
	for _, c := range cs {
		row := s.row(l, c, snap[c.ID], true)
		row.Beats = nil
		row.Group = c.Group
		row.Interval = shortDuration(c.Interval)
		rows = append(rows, row)
	}
	s.render(w, r, http.StatusOK, "admin", pageData{
		Title: msg.T(l, "admin.title"), Active: "admin", Flash: flash(l, r.URL.Query()),
		Body: adminView{Rows: rows, Types: checkTypes()},
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
		Params: buildFields(l, e.Info, paramPrefix, prepare(e.Impl, nil), nil, nil),
	})
}

// parseCheck valida el formulario. Si hay errores, devuelve la vista del
// formulario con los valores introducidos para volver a mostrarla.
func (s *Server) parseCheck(r *http.Request, e module.Entry[checks.Checker], saved module.Values) (model.Check, *checkFormView) {
	l := s.lang(r)
	rawCommon := readValues(r.PostForm, commonInfo, "", nil)
	rawParams := prepare(e.Impl, readValues(r.PostForm, e.Info, paramPrefix, saved))

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
		Name:          common.String("name"),
		Group:         common.String("group"),
		Interval:      common.Duration("interval"),
		Paused:        common.Bool("paused"),
		FailThreshold: int(common.Int64("fail_threshold")),
		DegradedAfter: common.Duration("degraded_after"),
		Type:          e.Info.ID,
		Params:        params,
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
	s.Tracker.SetCheck(c)
	s.Scheduler.Set(c)
	s.syncHeartbeats(r)
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
		"fail_threshold": strconv.Itoa(c.Threshold()), "degraded_after": "",
	}
	if c.DegradedAfter > 0 {
		common["degraded_after"] = c.DegradedAfter.String()
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
	s.Tracker.SetCheck(c)
	s.Scheduler.Set(c)
	s.syncHeartbeats(r)
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
	s.syncHeartbeats(r)
	s.Log.Info("check deleted", "check", c.Name)
	redirect(w, r, "/admin?m=deleted")
}

// prepare completa los parámetros con el gancho opcional del tipo de check
// (p. ej. el token de un heartbeat).
func prepare(c checks.Checker, p module.Values) module.Values {
	if pr, ok := c.(checks.Preparer); ok {
		return pr.Prepare(p)
	}
	return p
}

func (s *Server) syncHeartbeats(r *http.Request) {
	if err := SyncHeartbeats(r.Context(), s.Store); err != nil {
		s.Log.Error("could not refresh heartbeat tokens", "error", err)
	}
}
