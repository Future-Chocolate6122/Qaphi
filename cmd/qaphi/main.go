// Qaphi: monitor de servicios ultraligero con página de estado.
//
// Uso:
//
//	qaphi [serve] [opciones]   arranca el servidor (subcomando por defecto)
//	qaphi version              muestra la versión
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

	"qaphi/internal/app"
	"qaphi/internal/config"
	"qaphi/internal/logging"
	"qaphi/internal/version"
)

// Códigos de salida.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(args, stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, version.Get().String())
		return exitOK
	case "help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "qaphi: subcomando desconocido %q\n\n", cmd)
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Qaphi — monitor de servicios

Uso:
  qaphi [serve] [opciones]   arranca el servidor (por defecto)
  qaphi version              muestra la versión
  qaphi help                 muestra esta ayuda

Ejecuta «qaphi serve -h» para ver las opciones del servidor.
`)
}

func serve(args []string, stdout, stderr io.Writer) int {
	cfg, err := config.Parse("serve", args, os.Getenv, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "qaphi: configuración no válida:\n%v\n", err)
		return exitUsage
	}
	log := logging.New(stdout, logging.Options{
		Level:    cfg.LogLevel,
		Format:   cfg.LogFormat,
		OmitTime: logging.UnderJournald(os.Getenv),
	})

	// La primera señal inicia la parada ordenada. Después se restaura el
	// comportamiento por defecto, de modo que una segunda señal fuerza la
	// salida inmediata si la parada se atasca.
	// El contexto de la aplicación se cancela después de registrar la señal,
	// para que el log refleje el orden real de los hechos.
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-sigCtx.Done():
			stop()
			log.Info("señal recibida: parada ordenada en curso (repite la señal para forzar la salida)")
			cancel()
		case <-ctx.Done():
		}
	}()

	a, err := app.New(ctx, cfg, log)
	if err != nil {
		log.Error("no se pudo arrancar", "error", err)
		return exitError
	}
	runErr := a.Run(ctx)
	if err := a.Close(); err != nil {
		log.Error("error en la parada", "error", err)
		return exitError
	}
	if runErr != nil {
		log.Error("el servidor terminó con error", "error", runErr)
		return exitError
	}
	log.Info("qaphi detenido")
	return exitOK
}
