// Package version expone la versión del binario. Los valores se inyectan en
// tiempo de compilación con -ldflags (véase el Makefile).
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"time"
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

// Semver es una versión de publicación vMAJOR.MINOR.PATCH.
type Semver struct{ Major, Minor, Patch int64 }

// ParseRelease interpreta una versión de publicación ("v0.1.0"). Cualquier
// otra forma (compilaciones sin etiqueta como "dev" o "v0.1.0-3-gabc123")
// no es una publicación y devuelve ok=false.
func ParseRelease(s string) (v Semver, ok bool) {
	if len(s) < 6 || s[0] != 'v' {
		return Semver{}, false
	}
	var parts [3]int64
	n, digits := 0, 0
	for _, c := range s[1:] {
		switch {
		case c >= '0' && c <= '9':
			if digits > 0 && parts[n] == 0 { // sin ceros a la izquierda
				return Semver{}, false
			}
			parts[n] = parts[n]*10 + int64(c-'0')
			digits++
			if digits > 9 {
				return Semver{}, false
			}
		case c == '.' && digits > 0 && n < 2:
			n++
			digits = 0
		default:
			return Semver{}, false
		}
	}
	if n != 2 || digits == 0 {
		return Semver{}, false
	}
	return Semver{parts[0], parts[1], parts[2]}, true
}

// Compare devuelve -1, 0 o 1 según a sea menor, igual o mayor que b.
func (a Semver) Compare(b Semver) int {
	for _, d := range [3]int64{a.Major - b.Major, a.Minor - b.Minor, a.Patch - b.Patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	return 0
}

func (a Semver) String() string { return fmt.Sprintf("v%d.%d.%d", a.Major, a.Minor, a.Patch) }

// IsRelease indica si el binario es una versión publicada (etiqueta semver).
func IsRelease() bool {
	_, ok := ParseRelease(Version)
	return ok
}

// BuildTime devuelve la fecha del commit con el que se compiló el binario, o
// el instante cero si no se conoce.
func BuildTime() time.Time {
	if t, err := time.Parse(time.RFC3339, Get().Date); err == nil {
		return t
	}
	return time.Time{}
}
