package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/app"
	"github.com/future-chocolate6122/qaphi/internal/auth"
	"github.com/future-chocolate6122/qaphi/internal/install"
	"github.com/future-chocolate6122/qaphi/internal/msg"
	"github.com/future-chocolate6122/qaphi/internal/store"
	"github.com/future-chocolate6122/qaphi/internal/store/sqlite"
)

// resetPasswordCmd restablece la contraseña de admin desde la consola.
//
// Por el bloqueo exclusivo de la base de datos, el servicio no puede estar en
// marcha: si está instalado y activo, el comando lo detiene (requiere root),
// cambia la contraseña y lo vuelve a arrancar. Los ficheros de la base de
// datos conservan el propietario del servicio.
func resetPasswordCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "", "data directory (default: the installation's, or ./data)")
	fromStdin := fs.Bool("password-stdin", false, "read the password from standard input (one line) instead of prompting")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitOK
		}
		return exitUsage
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "qaphi reset-password: "+format+"\n", a...)
		return exitError
	}

	dir := resolveDataDir(*dataDir)
	dbPath := filepath.Join(dir, app.DBFileName)
	if _, err := os.Stat(dbPath); err != nil {
		return fail("no database in %s (use --data-dir)", dir)
	}

	pw, err := readNewPassword(stdin, stdout, *fromStdin)
	if err != nil {
		return fail("%v", err)
	}
	if e := auth.ValidatePassword(pw, pw); e != "" {
		return fail("%s", msg.T(msg.EN, e))
	}

	// Servicio instalado y en marcha: se detiene y se vuelve a arrancar.
	sys := install.OS{}
	restart := false
	if _, err := os.Stat(install.UnitPath); err == nil {
		if _, err := sys.Run("systemctl", "is-active", "--quiet", install.ServiceName); err == nil {
			if os.Geteuid() != 0 {
				return fail("the %s service is running; run this command with sudo so it can stop and restart it", install.ServiceName)
			}
			fmt.Fprintln(stdout, "Stopping the service...")
			if _, err := sys.Run("systemctl", "stop", install.ServiceName); err != nil {
				return fail("stop the service: %v", err)
			}
			restart = true
		}
	}
	code := func() int {
		owner, ownerErr := os.Stat(dbPath)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		st, err := sqlite.Open(ctx, dbPath)
		if errors.Is(err, store.ErrLocked) {
			return fail("the database is in use: stop qaphi first (e.g. 'sudo systemctl stop %s')", install.ServiceName)
		}
		if err != nil {
			return fail("%v", err)
		}
		setErr := auth.SetPassword(ctx, st, pw)
		closeErr := st.Close()
		if ownerErr == nil {
			keepOwner(dbPath, owner)
		}
		if setErr != nil {
			return fail("%v", setErr)
		}
		if closeErr != nil {
			return fail("%v", closeErr)
		}
		fmt.Fprintln(stdout, "OK: admin password reset. All sessions have been closed.")
		return exitOK
	}()
	if restart {
		fmt.Fprintln(stdout, "Starting the service...")
		if _, err := sys.Run("systemctl", "start", install.ServiceName); err != nil {
			return fail("start the service: %v", err)
		}
	}
	return code
}

// resolveDataDir: flag > QAPHI_DATA_DIR > opciones de la instalación > ./data.
func resolveDataDir(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("QAPHI_DATA_DIR"); v != "" {
		return v
	}
	if b, err := os.ReadFile(install.EnvPath); err == nil {
		if v := install.ParseEnv(string(b))["QAPHI_DATA_DIR"]; v != "" {
			return v
		}
	}
	return "./data"
}

// keepOwner devuelve a los ficheros de la base de datos el propietario que
// tenían (si el comando se ejecutó como root, SQLite podría haber creado
// ficheros nuevos a nombre de root que el servicio no podría abrir).
func keepOwner(dbPath string, before os.FileInfo) {
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 {
		return
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", dbPath + "-journal"} {
		if _, err := os.Lstat(p); err == nil {
			_ = os.Lchown(p, int(st.Uid), int(st.Gid))
		}
	}
}

// readNewPassword lee la contraseña nueva: de la entrada estándar si se pide,
// o del terminal sin eco y con confirmación.
func readNewPassword(stdin io.Reader, stdout io.Writer, fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	f, ok := stdin.(*os.File)
	if !ok || !isTerminal(f.Fd()) {
		return "", errors.New("no terminal: use --password-stdin to pass the password on standard input")
	}
	fmt.Fprint(stdout, "New password: ")
	a, err := readNoEcho(f)
	fmt.Fprintln(stdout)
	if err != nil {
		return "", err
	}
	fmt.Fprint(stdout, "Repeat it: ")
	b, err := readNoEcho(f)
	fmt.Fprintln(stdout)
	if err != nil {
		return "", err
	}
	if a != b {
		return "", errors.New(msg.T(msg.EN, auth.ErrPasswordMismatch))
	}
	return a, nil
}
