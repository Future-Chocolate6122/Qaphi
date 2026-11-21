// Qaphi: monitor de servicios ultraligero con página de estado.
//
// Uso:
//
//	qaphi [serve] [opciones]    arranca el servidor (subcomando por defecto)
//	qaphi install [opciones]    instala o actualiza como servicio de systemd
//	qaphi uninstall [--purge]   desinstala (conserva los datos salvo --purge)
//	qaphi reset-password        restablece la contraseña de admin
//	qaphi version [--short]     muestra la versión
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/future-chocolate6122/qaphi/internal/app"
	"github.com/future-chocolate6122/qaphi/internal/config"
	"github.com/future-chocolate6122/qaphi/internal/instance"
	"github.com/future-chocolate6122/qaphi/internal/logging"
	"github.com/future-chocolate6122/qaphi/internal/version"
)

// Códigos de salida.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(args, stdout, stderr)
	case "install":
		return installCmd(args, stdout, stderr)
	case "uninstall":
		return uninstallCmd(args, stdout, stderr)
	case "reset-password":
		return resetPasswordCmd(args, stdin, stdout, stderr)
	case "version":
		return versionCmd(args, stdout, stderr)
	case "help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "qaphi: unknown subcommand %q\n\n", cmd)
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Qaphi - service monitor

Usage:
  qaphi [serve] [options]     start the server (default)
  qaphi install [options]     install or update Qaphi as a systemd service
  qaphi uninstall [--purge]   uninstall; keeps the data unless --purge is given
  qaphi reset-password        reset the admin password
  qaphi version [--short]     print the version
  qaphi help                  print this help

Run 'qaphi <subcommand> -h' to see its options.
`)
}

func versionCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	short := fs.Bool("short", false, "print only the version number")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *short {
		fmt.Fprintln(stdout, version.Version)
	} else {
		fmt.Fprintln(stdout, version.Get().String())
	}
	return exitOK
}

func serve(args []string, stdout, stderr io.Writer) int {
	cfg, err := config.Parse("serve", args, os.Getenv, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "qaphi: invalid configuration:\n%v\n", err)
		return exitUsage
	}
	log := logging.New(stdout, logging.Options{
		Level:    cfg.LogLevel,
		Format:   cfg.LogFormat,
		OmitTime: logging.UnderJournald(os.Getenv),
	})

	// Instancia única en todo el sistema.
	lock, err := instance.Acquire(instance.DefaultName)
	if err != nil {
		log.Error("cannot start", "error", err)
		return exitError
	}
	defer lock.Release()

	// El contexto de la aplicación se cancela después de registrar la señal,
	// para que el log refleje el orden real de los hechos. Tras la primera
	// señal se restaura el comportamiento por defecto: una segunda fuerza la
	// salida si la parada se atasca.
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-sigCtx.Done():
			stop()
			log.Info("signal received: graceful shutdown in progress (send it again to force exit)")
			cancel()
		case <-ctx.Done():
		}
	}()

	a, err := app.New(ctx, cfg, log)
	if err != nil {
		log.Error("startup failed", "error", err)
		return exitError
	}
	runErr := a.Run(ctx)
	if err := a.Close(); err != nil {
		log.Error("error during shutdown", "error", err)
		return exitError
	}
	if runErr != nil {
		log.Error("server stopped with an error", "error", runErr)
		return exitError
	}
	log.Info("qaphi stopped")
	return exitOK
}
