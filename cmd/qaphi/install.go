package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/future-chocolate6122/qaphi/internal/install"
)

func installCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "allow installing a version older than the installed one")
	values := map[string]*string{}
	for _, name := range install.InstallFlags() {
		values[name] = fs.String(name, "", "")
	}
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: sudo qaphi install [options]

Installs Qaphi as a systemd service or, if it is already installed, updates it
with this binary, keeping the data and the options. If the service was running,
it is started again; if the new version does not start, it is rolled back.

The listen address, port and protocol are configured in the web UI
(Settings > Network). Startup options (kept across updates):
  --listen=host:port          emergency override of the web network settings
                              (plain HTTP); --listen= removes the override
  --log-level=level           debug, info, warn or error
  --log-format=format         text or json
  --flush-interval=duration   how often results are flushed to disk (10s-1h)

Other:
  --force                     allow installing an older version
`)
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "qaphi install: unexpected argument %q\n", fs.Arg(0))
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
	purge := fs.Bool("purge", false, "also delete the data ("+install.DataDir+")")
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
