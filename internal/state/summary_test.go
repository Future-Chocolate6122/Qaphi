package state

import (
	"testing"

	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

func TestSummary(t *testing.T) {
	mk := func(statuses ...model.Status) ([]model.Check, map[int64]CheckState) {
		var cs []model.Check
		snap := map[int64]CheckState{}
		for i, s := range statuses {
			id := int64(i + 1)
			c := model.Check{ID: id}
			if s == model.StatusPaused {
				c.Paused = true
			}
			cs = append(cs, c)
			if s != "" {
				snap[id] = CheckState{Status: s}
			}
		}
		return cs, snap
	}
	up, down, unk, paused := model.StatusUp, model.StatusDown, model.StatusUnknown, model.StatusPaused
	slow, pend := model.StatusDegraded, model.StatusPending
	tests := []struct {
		statuses []model.Status
		want     string // en castellano
		wantEN   string
		level    model.Status
	}{
		{[]model.Status{up, up, up, up}, "4/4 (todos up)", "4/4 (all up)", up},
		{[]model.Status{up, up, up, down}, "3/4 (1 caído)", "3/4 (1 down)", down},
		{[]model.Status{up, down, down}, "1/3 (2 caídos)", "1/3 (2 down)", down},
		{[]model.Status{up, unk, ""}, "1/3 (2 sin datos)", "1/3 (2 no data)", up},
		{[]model.Status{up, slow, pend}, "3/3 (1 con fallos, 1 lento)", "3/3 (1 failing, 1 slow)", model.StatusDegraded},
		{[]model.Status{up, model.StatusNoPerm}, "1/2 (1 sin permisos)", "1/2 (1 no permission)", up},
		{[]model.Status{up, down, unk, paused}, "1/3 (1 caído, 1 sin datos)", "1/3 (1 down, 1 no data)", down},
		{[]model.Status{paused}, "0/0 (sin checks activos)", "0/0 (no active checks)", unk},
		{nil, "0/0 (sin checks activos)", "0/0 (no active checks)", unk},
		{[]model.Status{unk}, "0/1 (1 sin datos)", "0/1 (1 no data)", unk},
	}
	for _, tt := range tests {
		cs, snap := mk(tt.statuses...)
		s := Summarize(cs, snap)
		if s.In(msg.ES) != tt.want || s.String() != tt.wantEN || s.Level() != tt.level {
			t.Errorf("%v → %q / %q / %s, se esperaba %q / %q / %s", tt.statuses, s.In(msg.ES), s.String(), s.Level(), tt.want, tt.wantEN, tt.level)
		}
	}
}
