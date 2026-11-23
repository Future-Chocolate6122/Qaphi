package install

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSys simula systemd y el sistema. Los «binarios» de los tests son
// ficheros de texto «BIN:<versión>»; el binario instalado responde a
// «version --short» con esa versión.
type fakeSys struct {
	root       string
	euid       int
	active     bool
	enabled    bool
	userExists bool
	exe        string
	missing    map[string]bool // órdenes que no existen
	cmds       []string
	// onStart se ejecuta al arrancar el servicio (p. ej. para simular una
	// migración de la base de datos).
	onStart func()
	// startedLine es la línea de arranque que el servicio deja en el log.
	startedLine string
	urls        []string // URL comprobadas
}

func (f *fakeSys) binVersion() string {
	b, _ := os.ReadFile(filepath.Join(f.root, BinPath))
	return strings.TrimPrefix(string(b), "BIN:")
}

// startOK: un binario «roto» no llega a arrancar.
func (f *fakeSys) start() {
	if f.onStart != nil {
		f.onStart()
	}
	f.active = !strings.Contains(f.binVersion(), "roto")
}

func (f *fakeSys) Euid() int { return f.euid }

func (f *fakeSys) Run(name string, args ...string) (string, error) {
	f.cmds = append(f.cmds, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	fail := errors.New("exit status 1")
	switch name {
	case "systemctl":
		switch args[0] {
		case "is-active":
			if f.active {
				return "", nil
			}
			return "", fail
		case "is-enabled":
			if f.enabled {
				return "", nil
			}
			return "", fail
		case "stop":
			f.active = false
		case "start":
			f.start()
		case "enable":
			f.enabled = true
			if len(args) > 1 && args[1] == "--now" {
				f.start()
			}
		case "disable":
			f.enabled, f.active = false, false
		}
		return "", nil
	case "useradd":
		f.userExists = true
		return "", nil
	case "userdel":
		f.userExists = false
		return "", nil
	case "journalctl":
		started := f.startedLine
		if started == "" {
			started = `level=INFO msg="qaphi started" version=v0.2.0 listen=0.0.0.0:9077 scheme=http`
		}
		return started + "\n" +
			`level=WARN msg="initial setup pending" token=ABCD-EFGH-IJKL-MNOP url="http://192.168.1.5:9077/setup"`, nil
	case filepath.Join(f.root, BinPath):
		return f.binVersion(), nil
	}
	return "", nil
}

func (f *fakeSys) LookPath(name string) (string, error) {
	if f.missing[name] {
		return "", errors.New("no encontrado")
	}
	return "/usr/bin/" + name, nil
}

func (f *fakeSys) Executable() (string, error) { return f.exe, nil }

func (f *fakeSys) LookupUser(string) (int, int, bool, error) {
	if f.userExists {
		return os.Getuid(), os.Getgid(), true, nil
	}
	return 0, 0, false, nil
}

func (f *fakeSys) Chown(string, int, int) error { return nil }

func (f *fakeSys) HTTPStatus(u string) (int, error) {
	f.urls = append(f.urls, u)
	if f.active {
		return 303, nil
	}
	return 0, errors.New("conexión rechazada")
}

func (f *fakeSys) Sleep(time.Duration) {}

func (f *fakeSys) ran(cmd string) bool {
	for _, c := range f.cmds {
		if c == cmd {
			return true
		}
	}
	return false
}

func (f *fakeSys) index(cmd string) int {
	for i, c := range f.cmds {
		if c == cmd {
			return i
		}
	}
	return -1
}

type env struct {
	t   *testing.T
	sys *fakeSys
	in  *Installer
	out *strings.Builder
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "/run/systemd/system"), 0o755); err != nil {
		t.Fatal(err)
	}
	sys := &fakeSys{root: root, euid: 0, missing: map[string]bool{}}
	out := &strings.Builder{}
	return &env{t: t, sys: sys, out: out, in: &Installer{Root: root, Sys: sys, Out: out, HealthTimeout: 20 * time.Millisecond}}
}

// binary prepara un binario de prueba con la versión dada y lo usa como el
// «binario en ejecución».
func (e *env) binary(ver string) {
	e.t.Helper()
	p := filepath.Join(e.t.TempDir(), "qaphi-nuevo")
	if err := os.WriteFile(p, []byte("BIN:"+ver), 0o755); err != nil {
		e.t.Fatal(err)
	}
	e.sys.exe = p
	e.in.Version = ver
	e.sys.cmds = nil
	e.out.Reset()
}

func (e *env) read(p string) string {
	b, err := os.ReadFile(filepath.Join(e.sys.root, p))
	if err != nil {
		return ""
	}
	return string(b)
}

func (e *env) exists(p string) bool {
	_, err := os.Stat(filepath.Join(e.sys.root, p))
	return err == nil
}

func TestPreflight(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*env)
		want  string
	}{
		{"sin root", func(e *env) { e.sys.euid = 1000 }, "sudo"},
		{"sin systemd", func(e *env) { os.RemoveAll(filepath.Join(e.sys.root, "run")) }, "systemd"},
		{"sin systemctl", func(e *env) { e.sys.missing["systemctl"] = true }, "systemctl"},
		{"sin useradd", func(e *env) { e.sys.missing["useradd"] = true }, "useradd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.binary("v0.1.0")
			tt.setup(e)
			err := e.in.Install(Options{})
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "nothing was changed") {
				t.Fatalf("error = %v, se esperaba %q", err, tt.want)
			}
			for _, p := range []string{BinPath, UnitPath, EnvPath, DataDir} {
				if e.exists(p) {
					t.Errorf("se creó %s pese al error", p)
				}
			}
			if len(e.sys.cmds) > 0 {
				t.Errorf("se ejecutaron órdenes: %v", e.sys.cmds)
			}
		})
	}
}

func TestInstalacionNueva(t *testing.T) {
	e := newEnv(t)
	e.binary("v0.1.0")
	if err := e.in.Install(Options{Env: map[string]string{"QAPHI_LISTEN": ":9000"}}); err != nil {
		t.Fatalf("Install: %v\n%s", err, e.out)
	}
	if e.read(BinPath) != "BIN:v0.1.0" {
		t.Error("binario no copiado")
	}
	if e.read(UnitPath) != Unit() {
		t.Error("unit no escrita")
	}
	envFile := e.read(EnvPath)
	if !strings.Contains(envFile, `QAPHI_LISTEN=":9000"`) || !strings.Contains(envFile, `QAPHI_DATA_DIR="/var/lib/qaphi"`) {
		t.Errorf("fichero de opciones = %q", envFile)
	}
	fi, err := os.Stat(filepath.Join(e.sys.root, DataDir))
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("directorio de datos: %v %v", fi, err)
	}
	for _, c := range []string{"useradd --system --user-group --home-dir /var/lib/qaphi --no-create-home --shell /bin/false qaphi",
		"systemctl daemon-reload", "systemctl enable --now qaphi"} {
		if !e.sys.ran(c) {
			t.Errorf("no se ejecutó %q (órdenes: %v)", c, e.sys.cmds)
		}
	}
	if !e.sys.active || !e.sys.enabled {
		t.Error("el servicio debería quedar activo y habilitado")
	}
	out := e.out.String()
	for _, s := range []string{"installed and running", "ABCD-EFGH-IJKL-MNOP", "http://192.168.1.5:9077/setup"} {
		if !strings.Contains(out, s) {
			t.Errorf("la salida no contiene %q:\n%s", s, out)
		}
	}

	// Repetir con el mismo binario: no hay nada que hacer.
	e.binary("v0.1.0")
	if err := e.in.Install(Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), "up to date") || e.sys.ran("systemctl stop qaphi") {
		t.Errorf("reinstalar lo mismo no debe tocar nada:\n%s\n%v", e.out, e.sys.cmds)
	}
}

func installed(t *testing.T, ver string) *env {
	t.Helper()
	e := newEnv(t)
	e.binary(ver)
	if err := e.in.Install(Options{Env: map[string]string{"QAPHI_LISTEN": ":9000"}}); err != nil {
		t.Fatalf("instalación inicial: %v", err)
	}
	// Una base de datos «con datos».
	if err := os.WriteFile(filepath.Join(e.sys.root, DataDir, dbFile), []byte("DATOS"), 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestActualizacionConServicioActivo(t *testing.T) {
	e := installed(t, "v0.1.0")
	e.binary("v0.2.0")
	if err := e.in.Install(Options{}); err != nil {
		t.Fatalf("Install: %v\n%s", err, e.out)
	}
	if e.read(BinPath) != "BIN:v0.2.0" || e.read(PrevPath) != "BIN:v0.1.0" {
		t.Errorf("binario = %q, anterior = %q", e.read(BinPath), e.read(PrevPath))
	}
	if e.read(filepath.Join(DataDir, dbFile)) != "DATOS" || e.read(filepath.Join(DataDir, dbFile+backupSufx)) != "DATOS" {
		t.Error("los datos o su copia no son correctos")
	}
	stop, start := e.sys.index("systemctl stop qaphi"), e.sys.index("systemctl start qaphi")
	if stop < 0 || start < 0 || stop > start {
		t.Errorf("se esperaba stop y luego start: %v", e.sys.cmds)
	}
	if e.sys.ran("useradd --system --user-group --home-dir /var/lib/qaphi --no-create-home --shell /bin/false qaphi") {
		t.Error("no debe volver a crear el usuario")
	}
	if !e.sys.active || !e.sys.enabled {
		t.Error("el servicio debe seguir activo y habilitado")
	}
	// Las opciones de la instalación se conservan.
	if !strings.Contains(e.read(EnvPath), `QAPHI_LISTEN=":9000"`) {
		t.Errorf("se perdieron las opciones: %q", e.read(EnvPath))
	}
	if !strings.Contains(e.out.String(), "v0.1.0 -> v0.2.0") || !strings.Contains(e.out.String(), "Data preserved") {
		t.Errorf("salida:\n%s", e.out)
	}
}

func TestActualizacionConServicioDetenido(t *testing.T) {
	e := installed(t, "v0.1.0")
	e.sys.active = false
	e.binary("v0.2.0")
	if err := e.in.Install(Options{}); err != nil {
		t.Fatal(err)
	}
	if e.sys.active || e.sys.ran("systemctl start qaphi") {
		t.Error("un servicio detenido no debe arrancarse")
	}
	if !strings.Contains(e.out.String(), "remains stopped") {
		t.Errorf("salida:\n%s", e.out)
	}
}

func TestActualizacionDeshabilitadoSigueDeshabilitado(t *testing.T) {
	e := installed(t, "v0.1.0")
	e.sys.enabled = false
	e.binary("v0.2.0")
	if err := e.in.Install(Options{}); err != nil {
		t.Fatal(err)
	}
	if e.sys.enabled {
		t.Error("un servicio deshabilitado no debe habilitarse al actualizar")
	}
}

func TestVersionAnterior(t *testing.T) {
	e := installed(t, "v0.3.0")
	e.binary("v0.2.0")
	err := e.in.Install(Options{})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("se esperaba rechazo sin --force: %v", err)
	}
	if e.read(BinPath) != "BIN:v0.3.0" || e.sys.ran("systemctl stop qaphi") {
		t.Fatal("el rechazo no debe tocar nada")
	}
	if err := e.in.Install(Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if e.read(BinPath) != "BIN:v0.2.0" {
		t.Fatal("con --force debe instalarse")
	}
}

func TestCompilacionDev(t *testing.T) {
	e := installed(t, "v0.3.0")
	e.binary("v0.3.0-2-gabc123")
	if err := e.in.Install(Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), "not a released version") {
		t.Errorf("falta el aviso de compilación sin etiqueta:\n%s", e.out)
	}
}

func TestCambiarOpciones(t *testing.T) {
	e := installed(t, "v0.1.0")
	e.binary("v0.1.0") // mismo binario, opción nueva
	if err := e.in.Install(Options{Env: map[string]string{"QAPHI_LISTEN": ":9100", "QAPHI_LOG_LEVEL": "debug"}}); err != nil {
		t.Fatal(err)
	}
	envFile := e.read(EnvPath)
	if !strings.Contains(envFile, `QAPHI_LISTEN=":9100"`) || !strings.Contains(envFile, `QAPHI_LOG_LEVEL="debug"`) {
		t.Errorf("opciones = %q", envFile)
	}
	if !e.sys.ran("systemctl stop qaphi") || !e.sys.ran("systemctl start qaphi") {
		t.Error("cambiar opciones debe reiniciar el servicio")
	}

	// Una opción no válida no cambia nada.
	e.binary("v0.1.0")
	err := e.in.Install(Options{Env: map[string]string{"QAPHI_LISTEN": "no-vale"}})
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(e.read(EnvPath), `":9100"`) || len(e.sys.cmds) > 3 {
		t.Errorf("una opción no válida no debe cambiar nada: %v", e.sys.cmds)
	}
}

func TestVueltaAtras(t *testing.T) {
	e := installed(t, "v0.1.0")
	db := filepath.Join(e.sys.root, DataDir, dbFile)
	// La versión rota «migra» la base de datos al arrancar y luego no responde.
	e.sys.onStart = func() {
		if strings.Contains(e.sys.binVersion(), "roto") {
			os.WriteFile(db, []byte("MIGRADA"), 0o600)
		}
	}
	e.binary("v0.2.0-roto")
	err := e.in.Install(Options{})
	if err == nil || !strings.Contains(err.Error(), "previous version was restored") {
		t.Fatalf("error = %v\n%s", err, e.out)
	}
	if e.read(BinPath) != "BIN:v0.1.0" {
		t.Errorf("binario tras la vuelta atrás = %q", e.read(BinPath))
	}
	if got := e.read(filepath.Join(DataDir, dbFile)); got != "DATOS" {
		t.Errorf("base de datos tras la vuelta atrás = %q", got)
	}
	if !e.sys.active {
		t.Error("tras la vuelta atrás el servicio debe quedar en marcha")
	}
}

func TestDesinstalar(t *testing.T) {
	e := installed(t, "v0.1.0")
	e.binary("v0.1.0")
	if err := e.in.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{BinPath, UnitPath, EnvPath} {
		if e.exists(p) {
			t.Errorf("%s no se borró", p)
		}
	}
	if e.sys.userExists || e.sys.active || e.sys.enabled {
		t.Error("usuario o servicio no eliminados")
	}
	if e.read(filepath.Join(DataDir, dbFile)) != "DATOS" {
		t.Error("sin --purge los datos deben conservarse")
	}
	if err := e.in.Uninstall(true); err != nil {
		t.Fatal(err)
	}
	if e.exists(DataDir) {
		t.Error("con --purge los datos deben borrarse")
	}
}

func TestUnitDeReferencia(t *testing.T) {
	const ref = "../../deploy/qaphi.service"
	if os.Getenv("QAPHI_UPDATE_UNIT") == "1" {
		if err := os.WriteFile(ref, []byte(Unit()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(ref)
	if err != nil {
		t.Fatalf("falta deploy/qaphi.service: %v", err)
	}
	if string(b) != Unit() {
		t.Fatal("deploy/qaphi.service no coincide con la unit del instalador; regenérala con «QAPHI_UPDATE_UNIT=1 go test ./internal/install -run TestUnitDeReferencia»")
	}
	for _, s := range []string{"AmbientCapabilities=CAP_NET_RAW", "After=network-online.target time-sync.target", "User=qaphi", "NoNewPrivileges=yes"} {
		if !strings.Contains(Unit(), s) {
			t.Errorf("la unit no contiene %q", s)
		}
	}
}

func TestLocalAddr(t *testing.T) {
	for in, want := range map[string]string{
		"": "127.0.0.1:9077", ":9000": "127.0.0.1:9000", "0.0.0.0:80": "127.0.0.1:80",
		"[::]:8091": "[::1]:8091", "192.168.1.5:8091": "192.168.1.5:8091", "basura": "127.0.0.1:9077",
	} {
		if got := localAddr(in); got != want {
			t.Errorf("localAddr(%q) = %q, se esperaba %q", in, got, want)
		}
	}
}

func TestEnvIdaYVuelta(t *testing.T) {
	in := map[string]string{"QAPHI_LISTEN": ":8091", "QAPHI_DATA_DIR": "/var/lib/qaphi", "RARO": `con "comillas" y espacios`}
	got := parseEnv(formatEnv(in))
	for k, v := range in {
		if got[k] != v {
			t.Errorf("%s = %q, se esperaba %q", k, got[k], v)
		}
	}
	if len(got) != len(in) {
		t.Errorf("claves de más: %v", got)
	}
}

func TestQuitarAnulacionDeListen(t *testing.T) {
	e := installed(t, "v0.1.0") // instalado con --listen :9000
	e.binary("v0.1.0")
	if err := e.in.Install(Options{Env: map[string]string{"QAPHI_LISTEN": ""}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.read(EnvPath), "QAPHI_LISTEN=") {
		t.Errorf("--listen= debe quitar la anulación: %q", e.read(EnvPath))
	}
}

func TestComprobacionUsaElLogDeArranque(t *testing.T) {
	e := installed(t, "v0.1.0")
	// La red se cambió desde la web a HTTPS en otro puerto.
	e.sys.startedLine = `msg="qaphi started" version=v0.2.0 listen=192.168.1.5:9443 scheme=https`
	e.binary("v0.2.0")
	if err := e.in.Install(Options{}); err != nil {
		t.Fatal(err)
	}
	if last := e.sys.urls[len(e.sys.urls)-1]; last != "https://192.168.1.5:9443/" {
		t.Errorf("URL comprobada = %q", last)
	}
	if !strings.Contains(e.out.String(), "https://192.168.1.5:9443") {
		t.Errorf("salida:\n%s", e.out)
	}
}

func TestStartedEndpoint(t *testing.T) {
	tests := map[string]endpoint{
		`level=INFO msg="qaphi started" version=v0.2.0 listen=:9077 scheme=http`:                                             {":9077", "http"},
		`msg="qaphi started" listen=[::]:9077 scheme=https` + "\n" + `msg="qaphi started" listen=127.0.0.1:9100 scheme=http`: {"127.0.0.1:9100", "http"},
	}
	for in, want := range tests {
		got, ok := startedEndpoint(in)
		if !ok || got != want {
			t.Errorf("startedEndpoint(%q) = %+v, %v", in, got, ok)
		}
	}
	if _, ok := startedEndpoint("nada"); ok {
		t.Error("sin línea de arranque no debe encontrarse nada")
	}
}

// TestHTTPStatusConHTTPS: la comprobación de arranque debe aceptar el
// certificado autofirmado de Qaphi (antes una actualización de una
// instalación en HTTPS fallaba y hacía la vuelta atrás).
func TestHTTPStatusConHTTPS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}))
	defer srv.Close()
	code, err := OS{}.HTTPStatus(srv.URL + "/")
	if err != nil || code != http.StatusSeeOther {
		t.Fatalf("HTTPS autofirmado: código %d, error %v", code, err)
	}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer plain.Close()
	if code, err := (OS{}).HTTPStatus(plain.URL + "/"); err != nil || code != http.StatusOK {
		t.Fatalf("HTTP: código %d, error %v", code, err)
	}
}
