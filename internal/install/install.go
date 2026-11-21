// Package install instala, actualiza y desinstala Qaphi como servicio de
// systemd, de forma desatendida y con un solo comando.
//
//   - Instalación nueva: comprueba root y systemd (si falta algo, lo explica y
//     sale sin cambiar nada), crea el usuario de sistema «qaphi» (sin
//     contraseña, sin shell, sin home), copia el binario, crea el directorio de
//     datos, genera la unit, la habilita y la arranca, y muestra la URL y el
//     token de configuración inicial.
//   - Actualización (el mismo comando con un binario nuevo): anota si el
//     servicio estaba activo y habilitado, lo detiene de forma ordenada, copia
//     la base de datos y el binario anterior, sustituye el binario, regenera la
//     unit y arranca el servicio solo si estaba en marcha. Si la versión nueva
//     no arranca, vuelve atrás automáticamente.
package install

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/config"
	"github.com/future-chocolate6122/qaphi/internal/version"
)

// Rutas y nombres de la instalación.
const (
	ServiceName = "qaphi"
	UserName    = "qaphi"
	BinPath     = "/usr/local/bin/qaphi"
	PrevPath    = BinPath + ".prev"
	UnitPath    = "/etc/systemd/system/qaphi.service"
	EnvDir      = "/etc/qaphi"
	EnvPath     = EnvDir + "/qaphi.env"
	DataDir     = "/var/lib/qaphi"
	dbFile      = "qaphi.db"
	backupSufx  = ".bak"
)

// Opciones de arranque que se pueden fijar al instalar (variable → flag).
var installOptions = []struct{ Flag, Env string }{
	{"listen", "QAPHI_LISTEN"},
	{"log-level", "QAPHI_LOG_LEVEL"},
	{"log-format", "QAPHI_LOG_FORMAT"},
	{"flush-interval", "QAPHI_FLUSH_INTERVAL"},
}

// Installer realiza la instalación. Root es un prefijo para todas las rutas
// (vacío en producción; un directorio temporal en los tests).
type Installer struct {
	Root          string
	Sys           System
	Out           io.Writer
	Version       string        // versión del binario que se instala
	HealthTimeout time.Duration // espera máxima a que el servicio responda
}

// New devuelve un instalador para el sistema real.
func New(out io.Writer) *Installer {
	return &Installer{Sys: OS{}, Out: out, Version: version.Version, HealthTimeout: 90 * time.Second}
}

// Options son las opciones de Install.
type Options struct {
	Env   map[string]string // opciones de arranque indicadas (QAPHI_*)
	Force bool              // permite instalar una versión anterior
}

func (in *Installer) path(p string) string { return filepath.Join(in.Root, p) }

func (in *Installer) say(format string, args ...any) { fmt.Fprintf(in.Out, format+"\n", args...) }

// preflight comprueba los requisitos sin cambiar nada.
func (in *Installer) preflight(needUseradd bool) error {
	var problems []string
	if in.Sys.Euid() != 0 {
		problems = append(problems, "hace falta ejecutarlo como root: usa «sudo»")
	}
	if fi, err := os.Stat(in.path("/run/systemd/system")); err != nil || !fi.IsDir() {
		problems = append(problems, "el sistema no se ha arrancado con systemd (solo se admite systemd; en otros sistemas ejecuta «qaphi serve» a mano)")
	}
	if _, err := in.Sys.LookPath("systemctl"); err != nil {
		problems = append(problems, "no se encuentra «systemctl»")
	}
	if needUseradd {
		if _, err := in.Sys.LookPath("useradd"); err != nil {
			problems = append(problems, "no se encuentra «useradd» para crear el usuario qaphi")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("no se puede continuar; no se ha cambiado nada:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

func (in *Installer) systemctl(args ...string) error {
	_, err := in.Sys.Run("systemctl", args...)
	return err
}

func (in *Installer) isActive() bool { return in.systemctl("is-active", "--quiet", ServiceName) == nil }
func (in *Installer) isEnabled() bool {
	return in.systemctl("is-enabled", "--quiet", ServiceName) == nil
}

// installedVersion pregunta su versión al binario instalado.
func (in *Installer) installedVersion() string {
	out, err := in.Sys.Run(in.path(BinPath), "version", "--short")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Install instala o actualiza.
func (in *Installer) Install(opts Options) error {
	_, _, userExists, err := in.Sys.LookupUser(UserName)
	if err != nil {
		return fmt.Errorf("consultar el usuario %s: %w", UserName, err)
	}
	if err := in.preflight(!userExists); err != nil {
		return err
	}
	src, err := in.Sys.Executable()
	if err != nil {
		return fmt.Errorf("localizar este binario: %w", err)
	}
	newBin, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("leer este binario: %w", err)
	}

	// ---- Plan: se calcula todo antes de tocar nada.
	oldBin, _ := os.ReadFile(in.path(BinPath))
	_, unitErr := os.Stat(in.path(UnitPath))
	upgrade := oldBin != nil || unitErr == nil
	selfInstall := filepath.Clean(src) == filepath.Clean(in.path(BinPath))

	oldVersion := ""
	if oldBin != nil && !selfInstall {
		oldVersion = in.installedVersion()
		if err := checkVersions(in.Version, oldVersion, opts.Force); err != nil {
			return err
		}
	}
	if _, ok := version.ParseRelease(in.Version); !ok {
		in.say("Aviso: esta compilación (%s) no es una versión publicada; se instala igualmente.", in.Version)
	}

	oldEnvRaw, _ := os.ReadFile(in.path(EnvPath))
	env := parseEnv(string(oldEnvRaw))
	for k, v := range opts.Env {
		env[k] = v
	}
	env["QAPHI_DATA_DIR"] = DataDir
	if _, err := config.Parse("serve", nil, func(k string) string { return env[k] }, io.Discard); err != nil {
		return fmt.Errorf("opciones de arranque no válidas; no se ha cambiado nada:\n%w", err)
	}
	newEnvRaw := []byte(formatEnv(env))
	oldUnit, _ := os.ReadFile(in.path(UnitPath))
	newUnit := []byte(Unit())

	binChanged := !selfInstall && !bytes.Equal(sha(oldBin), sha(newBin))
	envChanged := !bytes.Equal(oldEnvRaw, newEnvRaw)
	unitChanged := !bytes.Equal(oldUnit, newUnit)

	wasActive, wasEnabled := false, false
	if upgrade {
		wasActive, wasEnabled = in.isActive(), in.isEnabled()
	}
	if upgrade && !binChanged && !envChanged && !unitChanged {
		state := "detenido"
		if wasActive {
			state = "en marcha"
		}
		in.say("Qaphi %s ya está instalado y al día; no hay nada que cambiar (servicio %s).", in.Version, state)
		return nil
	}

	if upgrade {
		from := oldVersion
		if from == "" {
			from = "versión desconocida"
		}
		in.say("Actualizando Qaphi: %s → %s", from, in.Version)
	} else {
		in.say("Instalando Qaphi %s", in.Version)
	}

	// ---- Usuario y directorios.
	uid, gid, err := in.ensureUser(userExists)
	if err != nil {
		return err
	}
	if err := in.ensureDataDir(uid, gid); err != nil {
		return err
	}

	// ---- Parada ordenada (vuelca el buffer de resultados).
	if wasActive {
		in.say("· Deteniendo el servicio (se vuelcan los resultados pendientes)…")
		if err := in.systemctl("stop", ServiceName); err != nil {
			return fmt.Errorf("detener el servicio: %w", err)
		}
	}

	// ---- Copias para la vuelta atrás.
	var backup *rollbackState
	if upgrade {
		backup, err = in.backup(oldEnvRaw, oldUnit, oldBin != nil && binChanged, uid, gid)
		if err != nil {
			if wasActive {
				_ = in.systemctl("start", ServiceName)
			}
			return err
		}
	}

	// ---- Ficheros nuevos.
	if err := in.apply(binChanged || !upgrade, newBin, envChanged, newEnvRaw, unitChanged, newUnit); err != nil {
		if backup != nil {
			in.rollback(backup, wasActive)
		}
		return err
	}

	// ---- Arranque.
	shouldRun := !upgrade || wasActive
	started := time.Now()
	switch {
	case !upgrade:
		in.say("· Habilitando y arrancando el servicio…")
		err = in.systemctl("enable", "--now", ServiceName)
	default:
		if wasEnabled {
			err = in.systemctl("enable", ServiceName)
		}
		if err == nil && wasActive {
			in.say("· Arrancando el servicio…")
			err = in.systemctl("start", ServiceName)
		}
	}
	if err == nil && shouldRun {
		err = in.waitHealthy(env["QAPHI_LISTEN"])
	}
	if err != nil {
		if backup != nil {
			in.say("La versión nueva no arranca correctamente: %v", err)
			in.rollback(backup, wasActive)
			return fmt.Errorf("actualización fallida; se ha restaurado la versión anterior. Revisa el log: journalctl -u %s", ServiceName)
		}
		return fmt.Errorf("el servicio no arranca correctamente: %w. Revisa el log: journalctl -u %s", err, ServiceName)
	}

	// ---- Resumen.
	switch {
	case !upgrade:
		in.say("✓ Qaphi %s instalado y en marcha.", in.Version)
		in.printSetupHint(env["QAPHI_LISTEN"], started)
	case wasActive:
		in.say("✓ Qaphi actualizado a %s y en marcha. Datos conservados.", in.Version)
	default:
		in.say("✓ Qaphi actualizado a %s. El servicio estaba detenido y sigue detenido (arráncalo con «sudo systemctl start %s»).", in.Version, ServiceName)
	}
	return nil
}

// checkVersions impide instalar una versión anterior a la instalada sin --force.
func checkVersions(newV, oldV string, force bool) error {
	nv, nok := version.ParseRelease(newV)
	ov, ook := version.ParseRelease(oldV)
	if nok && ook && nv.Compare(ov) < 0 && !force {
		return fmt.Errorf("la versión instalada (%s) es más reciente que esta (%s); no se ha cambiado nada. Usa --force para instalar una anterior", oldV, newV)
	}
	return nil
}

func sha(b []byte) []byte {
	if b == nil {
		return nil
	}
	h := sha256.Sum256(b)
	return h[:]
}

func (in *Installer) ensureUser(exists bool) (int, int, error) {
	if !exists {
		shell := "/bin/false"
		for _, s := range []string{"/usr/sbin/nologin", "/sbin/nologin"} {
			if _, err := os.Stat(in.path(s)); err == nil {
				shell = s
				break
			}
		}
		in.say("· Creando el usuario de sistema %s (sin contraseña ni shell)…", UserName)
		if _, err := in.Sys.Run("useradd", "--system", "--user-group", "--home-dir", DataDir,
			"--no-create-home", "--shell", shell, UserName); err != nil {
			return 0, 0, fmt.Errorf("crear el usuario %s: %w", UserName, err)
		}
	}
	uid, gid, ok, err := in.Sys.LookupUser(UserName)
	if err != nil || !ok {
		return 0, 0, fmt.Errorf("el usuario %s no existe tras crearlo: %v", UserName, err)
	}
	return uid, gid, nil
}

// ensureDataDir crea el directorio de datos y asegura que todo lo que contiene
// pertenece al usuario del servicio (p. ej. tras reinstalar con otro UID).
func (in *Installer) ensureDataDir(uid, gid int) error {
	dir := in.path(DataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("crear %s: %w", DataDir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	paths := []string{dir}
	for _, e := range entries {
		if e.Type().IsRegular() {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	for _, p := range paths {
		if err := in.Sys.Chown(p, uid, gid); err != nil {
			return fmt.Errorf("asignar propietario a %s: %w", p, err)
		}
	}
	return nil
}

// rollbackState guarda lo necesario para volver atrás.
type rollbackState struct {
	oldEnv, oldUnit []byte
	hadBinary       bool
	dbBackedUp      bool
	uid, gid        int
}

func (in *Installer) backup(oldEnv, oldUnit []byte, binary bool, uid, gid int) (*rollbackState, error) {
	st := &rollbackState{oldEnv: oldEnv, oldUnit: oldUnit, hadBinary: binary, uid: uid, gid: gid}
	if binary {
		if err := copyFile(in.path(BinPath), in.path(PrevPath), 0o755); err != nil {
			return nil, fmt.Errorf("guardar el binario anterior: %w", err)
		}
	}
	db := in.path(filepath.Join(DataDir, dbFile))
	if _, err := os.Stat(db); err == nil {
		in.say("· Copiando la base de datos (%s%s)…", dbFile, backupSufx)
		for _, suffix := range []string{"", "-wal"} {
			src := db + suffix
			dst := db + suffix + backupSufx
			_ = os.Remove(dst)
			if _, err := os.Stat(src); err != nil {
				continue
			}
			if err := copyFile(src, dst, 0o600); err != nil {
				return nil, fmt.Errorf("copiar la base de datos: %w", err)
			}
			_ = in.Sys.Chown(dst, uid, gid)
		}
		st.dbBackedUp = true
	}
	return st, nil
}

func (in *Installer) apply(writeBin bool, bin []byte, writeEnv bool, env []byte, writeUnit bool, unit []byte) error {
	if writeBin {
		in.say("· Copiando el binario a %s…", BinPath)
		if err := writeFileAtomic(in.path(BinPath), bin, 0o755); err != nil {
			return fmt.Errorf("instalar el binario: %w", err)
		}
	}
	if writeEnv {
		if err := os.MkdirAll(in.path(EnvDir), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(in.path(EnvPath), env, 0o644); err != nil {
			return fmt.Errorf("guardar las opciones de arranque: %w", err)
		}
	}
	if writeUnit {
		if err := writeFileAtomic(in.path(UnitPath), unit, 0o644); err != nil {
			return fmt.Errorf("guardar la unit de systemd: %w", err)
		}
	}
	if writeEnv || writeUnit {
		if err := in.systemctl("daemon-reload"); err != nil {
			return fmt.Errorf("recargar systemd: %w", err)
		}
	}
	return nil
}

// rollback restaura binario, base de datos, opciones y unit anteriores, y
// arranca el servicio si estaba en marcha.
func (in *Installer) rollback(st *rollbackState, start bool) {
	in.say("· Volviendo a la versión anterior…")
	_ = in.systemctl("stop", ServiceName)
	report := func(what string, err error) {
		if err != nil {
			in.say("  ! no se pudo restaurar %s: %v", what, err)
		}
	}
	if st.hadBinary {
		report("el binario", copyFileAtomic(in.path(PrevPath), in.path(BinPath), 0o755))
	}
	if st.dbBackedUp {
		db := in.path(filepath.Join(DataDir, dbFile))
		_ = os.Remove(db + "-wal")
		for _, suffix := range []string{"", "-wal"} {
			if _, err := os.Stat(db + suffix + backupSufx); err == nil {
				err := copyFileAtomic(db+suffix+backupSufx, db+suffix, 0o600)
				report("la base de datos", err)
				_ = in.Sys.Chown(db+suffix, st.uid, st.gid)
			}
		}
	}
	if st.oldEnv != nil {
		report("las opciones", writeFileAtomic(in.path(EnvPath), st.oldEnv, 0o644))
	}
	if st.oldUnit != nil {
		report("la unit", writeFileAtomic(in.path(UnitPath), st.oldUnit, 0o644))
	}
	_ = in.systemctl("daemon-reload")
	if start {
		report("el servicio", in.systemctl("start", ServiceName))
	}
}

// waitHealthy espera a que systemd dé el servicio por activo y a que responda
// por HTTP (cualquier código que no sea un error del servidor).
func (in *Installer) waitHealthy(listen string) error {
	url := "http://" + localAddr(listen) + "/"
	deadline := time.Now().Add(in.HealthTimeout)
	var last error
	for {
		if in.isActive() {
			code, err := in.Sys.HTTPStatus(url)
			if err == nil && code < 500 {
				return nil
			}
			if err != nil {
				last = err
			} else {
				last = fmt.Errorf("HTTP %d", code)
			}
		} else {
			last = errors.New("systemd no da el servicio por activo")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no responde tras %s: %v", in.HealthTimeout, last)
		}
		in.Sys.Sleep(time.Second)
	}
}

// localAddr convierte la dirección de escucha en una dirección local
// alcanzable desde el propio equipo.
func localAddr(listen string) string {
	if listen == "" {
		listen = ":8091"
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "127.0.0.1:8091"
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		if ip != nil && ip.To4() == nil {
			host = "::1"
		} else {
			host = "127.0.0.1"
		}
	}
	return net.JoinHostPort(host, port)
}

var setupLogRe = regexp.MustCompile(`token=([A-Z2-7]{4}(?:-[A-Z2-7]{4}){3})`)
var urlLogRe = regexp.MustCompile(`url="?([^"\n]+)"?`)

// printSetupHint muestra cómo hacer la configuración inicial, leyendo el
// token del log del servicio.
func (in *Installer) printSetupHint(listen string, since time.Time) {
	var out string
	for i := 0; i < 10; i++ {
		out, _ = in.Sys.Run("journalctl", "-u", ServiceName+".service", "-o", "cat", "--no-pager",
			"--since", "@"+strconv.FormatInt(since.Add(-5*time.Second).Unix(), 10))
		if setupLogRe.MatchString(out) {
			break
		}
		in.Sys.Sleep(500 * time.Millisecond)
	}
	m := setupLogRe.FindStringSubmatch(out)
	if m == nil {
		_, port, _ := net.SplitHostPort(localAddr(listen))
		in.say("  Abre http://<ip-del-equipo>:%s/ en el navegador.", port)
		in.say("  Si es la primera vez, el token de configuración está en: journalctl -u %s | grep token", ServiceName)
		return
	}
	urls := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, m[1]) {
			if u := urlLogRe.FindStringSubmatch(line); u != nil {
				urls = u[1]
			}
		}
	}
	in.say("")
	in.say("  Configuración inicial:")
	if urls != "" {
		in.say("    Abre en el navegador: %s", urls)
	}
	in.say("    Token de configuración: %s", m[1])
}

// Uninstall detiene y elimina el servicio, la unit, el binario, las opciones
// y el usuario. Los datos se conservan salvo con purge.
func (in *Installer) Uninstall(purge bool) error {
	if err := in.preflight(false); err != nil {
		return err
	}
	in.say("Desinstalando Qaphi")
	if _, err := os.Stat(in.path(UnitPath)); err == nil {
		in.say("· Deteniendo y deshabilitando el servicio…")
		_ = in.systemctl("disable", "--now", ServiceName)
		if err := os.Remove(in.path(UnitPath)); err != nil {
			return err
		}
		_ = in.systemctl("daemon-reload")
	}
	for _, p := range []string{BinPath, PrevPath, EnvPath} {
		if err := os.Remove(in.path(p)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	_ = os.Remove(in.path(EnvDir)) // solo si está vacío
	if _, _, ok, _ := in.Sys.LookupUser(UserName); ok {
		if _, err := in.Sys.LookPath("userdel"); err == nil {
			in.say("· Eliminando el usuario %s…", UserName)
			if _, err := in.Sys.Run("userdel", UserName); err != nil {
				in.say("  ! no se pudo eliminar el usuario: %v", err)
			}
		}
	}
	if purge {
		in.say("· Borrando los datos de %s…", DataDir)
		if err := os.RemoveAll(in.path(DataDir)); err != nil {
			return err
		}
		in.say("✓ Qaphi desinstalado y datos borrados.")
		return nil
	}
	in.say("✓ Qaphi desinstalado. Los datos se conservan en %s (bórralos con «qaphi uninstall --purge»).", DataDir)
	return nil
}

// ---------------------------------------------------------------- opciones

// ParseEnv interpreta un fichero de variables KEY=VALOR (como el que genera
// el instalador). Ignora comentarios y líneas vacías.
func ParseEnv(s string) map[string]string { return parseEnv(s) }

func parseEnv(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if uq, err := strconv.Unquote(v); err == nil {
			v = uq
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}

func formatEnv(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Opciones de arranque de Qaphi, generadas por «qaphi install».\n")
	b.WriteString("# No hace falta editarlo: para cambiar una opción, vuelve a ejecutar\n")
	b.WriteString("# «sudo qaphi install --opción=valor».\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, strconv.Quote(env[k]))
	}
	return b.String()
}

// EnvFromFlags convierte los flags de install en variables de entorno.
func EnvFromFlags(values map[string]string) map[string]string {
	out := map[string]string{}
	for _, o := range installOptions {
		if v, ok := values[o.Flag]; ok {
			out[o.Env] = v
		}
	}
	return out
}

// InstallFlags devuelve los nombres de los flags que admite install.
func InstallFlags() []string {
	out := make([]string, 0, len(installOptions))
	for _, o := range installOptions {
		out = append(out, o.Flag)
	}
	return out
}

// ---------------------------------------------------------------- ficheros

// writeFileAtomic escribe en un temporal del mismo directorio, sincroniza y
// renombra: el fichero nunca queda a medias, ni siquiera ante un corte de luz.
// Funciona también con un binario en ejecución (se sustituye el nombre, no el
// contenido del fichero abierto).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // sin efecto tras el renombrado
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func copyFile(src, dst string, perm os.FileMode) error { return copyFileAtomic(src, dst, perm) }

func copyFileAtomic(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, data, perm)
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }
