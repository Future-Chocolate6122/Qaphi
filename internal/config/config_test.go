package config

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParse(t *testing.T) {
	absData, _ := filepath.Abs("./data")
	absOther, _ := filepath.Abs("/srv/qaphi")

	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		want    Config
		wantErr string // subcadena esperada en el error; vacío = sin error
	}{
		{
			name: "valores por defecto",
			want: Config{Listen: ":8091", DataDir: absData, LogLevel: slog.LevelInfo, LogFormat: "text", FlushInterval: 5 * time.Minute},
		},
		{
			name: "variables de entorno",
			env: map[string]string{
				"QAPHI_LISTEN": "127.0.0.1:9000", "QAPHI_DATA_DIR": "/srv/qaphi",
				"QAPHI_LOG_LEVEL": "DEBUG", "QAPHI_LOG_FORMAT": "json", "QAPHI_FLUSH_INTERVAL": "30s",
			},
			want: Config{Listen: "127.0.0.1:9000", DataDir: absOther, LogLevel: slog.LevelDebug, LogFormat: "json", FlushInterval: 30 * time.Second},
		},
		{
			name: "el flag gana a la variable",
			args: []string{"--listen", ":7000", "-log-level=warn"},
			env:  map[string]string{"QAPHI_LISTEN": ":9000", "QAPHI_LOG_LEVEL": "debug"},
			want: Config{Listen: ":7000", DataDir: absData, LogLevel: slog.LevelWarn, LogFormat: "text", FlushInterval: 5 * time.Minute},
		},
		{
			name: "variable vacía se ignora",
			env:  map[string]string{"QAPHI_LISTEN": "  "},
			want: Config{Listen: ":8091", DataDir: absData, LogLevel: slog.LevelInfo, LogFormat: "text", FlushInterval: 5 * time.Minute},
		},
		{
			name: "IPv6",
			args: []string{"--listen", "[::1]:8091"},
			want: Config{Listen: "[::1]:8091", DataDir: absData, LogLevel: slog.LevelInfo, LogFormat: "text", FlushInterval: 5 * time.Minute},
		},
		{name: "listen sin puerto", args: []string{"--listen", "localhost"}, wantErr: "--listen"},
		{name: "puerto fuera de rango", args: []string{"--listen", ":70000"}, wantErr: "puerto no válido"},
		{name: "nivel de log erróneo en variable", env: map[string]string{"QAPHI_LOG_LEVEL": "verbose"}, wantErr: "QAPHI_LOG_LEVEL"},
		{name: "formato de log erróneo", args: []string{"--log-format", "xml"}, wantErr: "formato no válido"},
		{name: "intervalo ilegible", args: []string{"--flush-interval", "cinco"}, wantErr: "duración no válida"},
		{name: "intervalo demasiado corto", args: []string{"--flush-interval", "1s"}, wantErr: "fuera de rango"},
		{name: "intervalo demasiado largo", args: []string{"--flush-interval", "2h"}, wantErr: "fuera de rango"},
		{name: "data-dir con interrogación", args: []string{"--data-dir", "/tmp/a?b"}, wantErr: "'?'"},
		{name: "argumento sobrante", args: []string{"extra"}, wantErr: "argumento inesperado"},
		{name: "flag desconocido", args: []string{"--nope"}, wantErr: "nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse("serve", tt.args, envFrom(tt.env), io.Discard)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, se esperaba que contuviera %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error inesperado: %v", err)
			}
			if *cfg != tt.want {
				t.Fatalf("config = %+v, se esperaba %+v", *cfg, tt.want)
			}
		})
	}
}

func TestParseVariosErrores(t *testing.T) {
	_, err := Parse("serve", []string{"--listen", "x", "--log-level", "y"}, envFrom(nil), io.Discard)
	if err == nil {
		t.Fatal("se esperaba error")
	}
	for _, s := range []string{"--listen", "--log-level"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("el error %q no menciona %s", err, s)
		}
	}
}

func TestParseHelp(t *testing.T) {
	var sb strings.Builder
	_, err := Parse("serve", []string{"-h"}, envFrom(nil), &sb)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, se esperaba flag.ErrHelp", err)
	}
	for _, s := range []string{"QAPHI_LISTEN", "QAPHI_DATA_DIR", "QAPHI_FLUSH_INTERVAL"} {
		if !strings.Contains(sb.String(), s) {
			t.Errorf("la ayuda no menciona %s", s)
		}
	}
}
