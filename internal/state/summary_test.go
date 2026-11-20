package state

import (
	"testing"

	"qaphi/internal/model"
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
	tests := []struct {
		statuses []model.Status
		want     string
		level    model.Status
	}{
		{[]model.Status{up, up, up, up}, "4/4 (todos up)", up},
		{[]model.Status{up, up, up, down}, "3/4 (1 caído)", down},
		{[]model.Status{up, down, down}, "1/3 (2 caídos)", down},
		{[]model.Status{up, unk, ""}, "1/3 (2 sin datos)", up},
		{[]model.Status{up, down, unk, paused}, "1/3 (1 caído, 1 sin datos)", down},
		{[]model.Status{paused}, "0/0 (sin checks activos)", unk},
		{nil, "0/0 (sin checks activos)", unk},
		{[]model.Status{unk}, "0/1 (1 sin datos)", unk},
	}
	for _, tt := range tests {
		cs, snap := mk(tt.statuses...)
		s := Summarize(cs, snap)
		if s.String() != tt.want || s.Level() != tt.level {
			t.Errorf("%v → %q / %s, se esperaba %q / %s", tt.statuses, s.String(), s.Level(), tt.want, tt.level)
		}
	}
}
