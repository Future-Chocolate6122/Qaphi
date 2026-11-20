package main

import (
	"flag"
	"fmt"
	"io"

	"qaphi/internal/install"
)

func installCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "permite instalar una versión anterior a la instalada")
	values := map[string]*string{}
	for _, name := range install.InstallFlags() {
		values[name] = fs.String(name, "", "")
	}
	fs.Usage = func() {
		fmt.Fprint(stderr, `Uso: sudo qaphi install [opciones]

Instala Qaphi como servicio de systemd o, si ya está instalado, lo actualiza
con este binario conservando los datos y las opciones. Si el servicio estaba en
marcha, vuelve a arrancarlo; si la versión nueva no arranca, vuelve atrás.

Opciones de arranque (se guardan y se conservan en las actualizaciones):
  --listen=host:puerto        dirección de escucha (por defecto :8091)
  --log-level=nivel           debug, info, warn o error
  --log-format=formato        text o json
  --flush-interval=duración   volcado de resultados a disco (10s–1h)

Otras:
  --force                     permite instalar una versión anterior
`)
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "qaphi install: argumento inesperado %q\n", fs.Arg(0))
		return exitUsage
	}
	given := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if v, ok := values[f.Name]; ok {
			given[f.Name] = *v
		}
	})
	in := install.New(stdout)
	if err := in.Install(install.Options{Env: install.EnvFromFlags(given), Force: *force}); err != nil {
		fmt.Fprintf(stderr, "qaphi install: %v\n", err)
		return exitError
	}
	return exitOK
}

func uninstallCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	purge := fs.Bool("purge", false, "borra también los datos ("+install.DataDir+")")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitOK
		}
		return exitUsage
	}
	if err := install.New(stdout).Uninstall(*purge); err != nil {
		fmt.Fprintf(stderr, "qaphi uninstall: %v\n", err)
		return exitError
	}
	return exitOK
}
