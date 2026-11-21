package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/future-chocolate6122/qaphi/internal/auth"
	"github.com/future-chocolate6122/qaphi/internal/model"
	"github.com/future-chocolate6122/qaphi/internal/store/sqlite"
	"github.com/future-chocolate6122/qaphi/internal/version"
)

func runCmd(args []string, stdin string) (int, string, string) {
	var out, errOut strings.Builder
	code := run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRun(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{name: "version", args: []string{"version"}, code: exitOK, stdout: "qaphi "},
		{name: "version corta", args: []string{"version", "--short"}, code: exitOK, stdout: version.Version + "\n"},
		{name: "help", args: []string{"help"}, code: exitOK, stdout: "reset-password"},
		{name: "subcomando desconocido", args: []string{"bailar"}, code: exitUsage, stderr: "subcomando desconocido"},
		{name: "serve -h", args: []string{"serve", "-h"}, code: exitOK, stderr: "QAPHI_LISTEN"},
		{name: "flags sin subcomando = serve", args: []string{"-h"}, code: exitOK, stderr: "--data-dir"},
		{name: "configuración no válida", args: []string{"serve", "--listen", "nada"}, code: exitUsage, stderr: "configuración no válida"},
		{name: "install -h", args: []string{"install", "-h"}, code: exitOK, stderr: "--listen"},
		{name: "install argumento sobrante", args: []string{"install", "extra"}, code: exitUsage, stderr: "argumento inesperado"},
		{name: "uninstall -h", args: []string{"uninstall", "-h"}, code: exitOK, stderr: "purge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := runCmd(tt.args, "")
			if code != tt.code {
				t.Errorf("código = %d, se esperaba %d (stderr: %s)", code, tt.code, errOut)
			}
			if tt.stdout != "" && !strings.Contains(out, tt.stdout) {
				t.Errorf("stdout = %q, se esperaba %q", out, tt.stdout)
			}
			if tt.stderr != "" && !strings.Contains(errOut, tt.stderr) {
				t.Errorf("stderr = %q, se esperaba %q", errOut, tt.stderr)
			}
		})
	}
}

func TestResetPassword(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "qaphi.db")
	st, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetPassword(ctx, st, "contraseña antigua"); err != nil {
		t.Fatal(err)
	}
	st.CreateSession(ctx, model.Session{IDHash: []byte("x"), CSRF: "c"})

	// Mientras la base de datos está abierta por otro proceso, no se puede.
	code, _, errOut := runCmd([]string{"reset-password", "--data-dir", dir, "--password-stdin"}, "contraseña nueva segura\n")
	if code != exitError || !strings.Contains(errOut, "en uso") {
		t.Fatalf("con la base de datos en uso: código %d, %s", code, errOut)
	}
	st.Close()

	code, out, errOut := runCmd([]string{"reset-password", "--data-dir", dir, "--password-stdin"}, "corta\n")
	if code != exitError || !strings.Contains(errOut, "10 caracteres") {
		t.Fatalf("contraseña corta: código %d, %s %s", code, out, errOut)
	}

	code, out, errOut = runCmd([]string{"reset-password", "--data-dir", dir, "--password-stdin"}, "contraseña nueva segura\n")
	if code != exitOK || !strings.Contains(out, "restablecida") {
		t.Fatalf("código %d: %s %s", code, out, errOut)
	}
	st, err = sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	admin, _ := auth.NewAdmin(ctx, st)
	if ok, _ := admin.Verify(ctx, "contraseña nueva segura"); !ok {
		t.Fatal("la contraseña nueva no funciona")
	}
	if ok, _ := admin.Verify(ctx, "contraseña antigua"); ok {
		t.Fatal("la contraseña antigua sigue funcionando")
	}
	if _, err := st.GetSession(ctx, []byte("x")); err == nil {
		t.Fatal("las sesiones deben cerrarse")
	}
}

func TestResetPasswordSinDatos(t *testing.T) {
	code, _, errOut := runCmd([]string{"reset-password", "--data-dir", t.TempDir(), "--password-stdin"}, "contraseña nueva segura\n")
	if code != exitError || !strings.Contains(errOut, "no hay base de datos") {
		t.Fatalf("código %d: %s", code, errOut)
	}
}

func TestResetPasswordSinTerminal(t *testing.T) {
	dir := t.TempDir()
	st, err := sqlite.Open(context.Background(), filepath.Join(dir, "qaphi.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	code, _, errOut := runCmd([]string{"reset-password", "--data-dir", dir}, "")
	if code != exitError || !strings.Contains(errOut, "--password-stdin") {
		t.Fatalf("código %d: %s", code, errOut)
	}
}
