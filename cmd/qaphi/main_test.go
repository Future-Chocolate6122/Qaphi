package main

import (
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		code    int
		stdout  string
		stderr  string
		envNone bool
	}{
		{name: "version", args: []string{"version"}, code: exitOK, stdout: "qaphi "},
		{name: "help", args: []string{"help"}, code: exitOK, stdout: "Uso:"},
		{name: "subcomando desconocido", args: []string{"bailar"}, code: exitUsage, stderr: "subcomando desconocido"},
		{name: "serve -h", args: []string{"serve", "-h"}, code: exitOK, stderr: "QAPHI_LISTEN"},
		{name: "flags sin subcomando = serve", args: []string{"-h"}, code: exitOK, stderr: "--data-dir"},
		{name: "configuración no válida", args: []string{"serve", "--listen", "nada"}, code: exitUsage, stderr: "configuración no válida"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut strings.Builder
			code := run(tt.args, &out, &errOut)
			if code != tt.code {
				t.Errorf("código = %d, se esperaba %d (stderr: %s)", code, tt.code, errOut.String())
			}
			if tt.stdout != "" && !strings.Contains(out.String(), tt.stdout) {
				t.Errorf("stdout = %q, se esperaba %q", out.String(), tt.stdout)
			}
			if tt.stderr != "" && !strings.Contains(errOut.String(), tt.stderr) {
				t.Errorf("stderr = %q, se esperaba %q", errOut.String(), tt.stderr)
			}
		})
	}
}
