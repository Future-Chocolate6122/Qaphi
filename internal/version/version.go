// Package version expone la versión del binario. Los valores se inyectan en
// tiempo de compilación con -ldflags (véase el Makefile).
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Valores inyectados con -X en el enlazado.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Info agrupa los datos de versión del binario en ejecución.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Date      string `json:"date,omitempty"`
	GoVersion string `json:"go"`
	Platform  string `json:"platform"`
}

// Get devuelve la información de versión. Si el commit no se inyectó, intenta
// obtenerlo de los metadatos de VCS que Go incrusta al compilar.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  platform(),
	}
	if info.Commit == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					info.Commit = shortRev(s.Value)
				case "vcs.time":
					if info.Date == "" {
						info.Date = s.Value
					}
				}
			}
		}
	}
	return info
}

// String devuelve una línea legible, p. ej.
// "qaphi 0.1.0 (a1b2c3d, 2026-11-20T10:00:00Z) go1.27.4 linux/arm v7".
func (i Info) String() string {
	s := "qaphi " + i.Version
	switch {
	case i.Commit != "" && i.Date != "":
		s += fmt.Sprintf(" (%s, %s)", i.Commit, i.Date)
	case i.Commit != "":
		s += fmt.Sprintf(" (%s)", i.Commit)
	}
	return s + " " + i.GoVersion + " " + i.Platform
}

func platform() string {
	p := runtime.GOOS + "/" + runtime.GOARCH
	if runtime.GOARCH == "arm" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				if s.Key == "GOARM" {
					p += " v" + s.Value
				}
			}
		}
	}
	return p
}

func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}
