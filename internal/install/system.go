package install

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// System abstrae las operaciones del sistema que hace el instalador, para
// poder probarlo sin systemd ni privilegios.
type System interface {
	Euid() int
	// Run ejecuta una orden y devuelve su salida combinada.
	Run(name string, args ...string) (string, error)
	LookPath(name string) (string, error)
	// Executable devuelve la ruta real del binario en ejecución.
	Executable() (string, error)
	// LookupUser devuelve el UID y GID de un usuario, y si existe.
	LookupUser(name string) (uid, gid int, ok bool, err error)
	Chown(path string, uid, gid int) error
	// HTTPStatus hace un GET sin seguir redirecciones y devuelve el código.
	HTTPStatus(url string) (int, error)
	Sleep(d time.Duration)
}

// OS es la implementación real de System.
type OS struct{}

func (OS) Euid() int { return os.Geteuid() }

func (OS) Run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s != "" {
			return s, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, s)
		}
		return s, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return s, nil
}

func (OS) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (OS) Executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

func (OS) LookupUser(name string) (int, int, bool, error) {
	u, err := user.Lookup(name)
	if err != nil {
		var unknown user.UnknownUserError
		if errorsAs(err, &unknown) {
			return 0, 0, false, nil
		}
		return 0, 0, false, err
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return 0, 0, false, fmt.Errorf("UID/GID no numéricos para %s", name)
	}
	return uid, gid, true, nil
}

func (OS) Chown(path string, uid, gid int) error { return os.Lchown(path, uid, gid) }

func (OS) HTTPStatus(url string) (int, error) {
	c := &http.Client{
		Timeout:       3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	res, err := c.Get(url)
	if err != nil {
		return 0, err
	}
	res.Body.Close()
	return res.StatusCode, nil
}

func (OS) Sleep(d time.Duration) { time.Sleep(d) }
