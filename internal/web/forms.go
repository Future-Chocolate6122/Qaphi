package web

import (
	"net/url"

	"github.com/future-chocolate6122/qaphi/internal/module"
	"github.com/future-chocolate6122/qaphi/internal/msg"
)

// commonInfo declara los campos comunes a todos los checks. Se valida con el
// mismo mecanismo que los parámetros de cada tipo.
var commonInfo = module.Info{
	ID:   "common",
	Name: "check.f.common",
	Fields: []module.Field{
		{Key: "name", Label: "check.f.name", Type: module.Text, Required: true, MaxLen: 100},
		{Key: "group", Label: "check.f.group", Help: "check.f.group.help", Type: module.Text, MaxLen: 50},
		{Key: "interval", Label: "check.f.interval", Help: "check.f.interval.help", Type: module.Duration, Default: "60s", Min: "10s", Max: "24h"},
		{Key: "fail_threshold", Label: "check.f.fail_threshold", Help: "check.f.fail_threshold.help", Type: module.Int, Default: "3", Min: "1", Max: "20"},
		{Key: "degraded_after", Label: "check.f.degraded_after", Help: "check.f.degraded_after.help", Type: module.Duration, Min: "1ms", Max: "60s"},
		{Key: "paused", Label: "check.f.paused", Type: module.Bool},
	},
}

func init() {
	if err := commonInfo.Validate(); err != nil {
		panic(err)
	}
}

// paramPrefix distingue en el formulario los parámetros del tipo de check de
// los campos comunes.
const paramPrefix = "p_"

// fieldView es un campo listo para la plantilla "field".
type fieldView struct {
	Name      string // nombre en el formulario
	Label     string // clave de texto
	Help      string // clave de texto
	Type      module.FieldType
	Value     string
	Checked   bool
	Required  bool
	MaxLen    int
	Error     string // ya traducido
	Options   []optionView
	HasSecret bool // secreto ya guardado: vacío = conservar
	ReadOnly  bool
}

type optionView struct {
	Value, Label string
	Selected     bool
}

// buildFields prepara los campos de una declaración con sus valores y
// errores (traducidos a l). saved indica qué secretos tienen ya un valor
// guardado.
func buildFields(l msg.Lang, info module.Info, prefix string, values module.Values, errs module.Errors, saved module.Values) []fieldView {
	out := make([]fieldView, 0, len(info.Fields))
	for _, f := range info.Fields {
		v, ok := values[f.Key]
		if !ok {
			v = f.Default
		}
		fv := fieldView{
			Name: prefix + f.Key, Label: f.Label, Help: f.Help, Type: f.Type,
			Value: v, Required: f.Required, MaxLen: f.MaxLen, Error: errs[f.Key].In(l), ReadOnly: f.ReadOnly,
		}
		switch f.Type {
		case module.Bool:
			fv.Checked = v == "true"
		case module.Secret:
			fv.Value = "" // los secretos nunca se devuelven al navegador
			fv.HasSecret = saved[f.Key] != ""
			if fv.HasSecret {
				fv.Required = false
			}
		case module.Select:
			for _, o := range f.Options {
				fv.Options = append(fv.Options, optionView{Value: o.Value, Label: o.Label, Selected: o.Value == v})
			}
		}
		out = append(out, fv)
	}
	return out
}

// readValues extrae del formulario los valores de una declaración. Para las
// casillas se toma el último valor enviado (el campo oculto "false" va antes
// que la casilla). Un secreto vacío conserva el valor guardado.
func readValues(form url.Values, info module.Info, prefix string, saved module.Values) module.Values {
	out := module.Values{}
	for _, f := range info.Fields {
		vs, ok := form[prefix+f.Key]
		if !ok || len(vs) == 0 {
			continue
		}
		v := vs[len(vs)-1]
		if f.Type == module.Secret && v == "" && saved[f.Key] != "" {
			v = saved[f.Key]
		}
		out[f.Key] = v
	}
	return out
}

// flashMessages son los mensajes de confirmación admitidos en ?m=.
var flashMessages = map[string]string{
	"created":   "flash.created",
	"updated":   "flash.updated",
	"deleted":   "flash.deleted",
	"welcome":   "flash.welcome",
	"settings":  "flash.settings",
	"network":   "flash.network",
	"cancelled": "flash.cancelled",
	"cert":      "flash.cert",
	"data":      "flash.data",
}

func flash(l msg.Lang, q url.Values) string {
	if k, ok := flashMessages[q.Get("m")]; ok {
		return msg.T(l, k)
	}
	return ""
}
