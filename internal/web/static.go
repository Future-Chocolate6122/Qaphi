package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// staticFiles sirve los ficheros estáticos embebidos. Como el contenido no
// cambia durante la ejecución, se calcula una huella por fichero al arrancar:
// las URL generadas con asset llevan la huella (?v=…) y se pueden cachear sin
// límite; además se envía ETag para revalidar sin descargar de nuevo.
type staticFiles struct {
	files map[string]staticFile
}

type staticFile struct {
	data  []byte
	etag  string
	ctype string
}

func loadStatic(fsys fs.FS, root string) (*staticFiles, error) {
	sf := &staticFiles{files: map[string]staticFile{}}
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		ctype := mime.TypeByExtension(path.Ext(p))
		if ctype == "" {
			ctype = http.DetectContentType(data)
		}
		name := strings.TrimPrefix(p, root+"/")
		sf.files[name] = staticFile{data: data, etag: hex.EncodeToString(sum[:8]), ctype: ctype}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load static files: %w", err)
	}
	return sf, nil
}

// url devuelve la ruta pública de un fichero estático con su huella. Se usa
// desde las plantillas: {{asset "style.css"}}. Un nombre inexistente es un
// error de programación y hace fallar la plantilla.
func (sf *staticFiles) url(name string) (string, error) {
	f, ok := sf.files[name]
	if !ok {
		return "", fmt.Errorf("unknown static file: %s", name)
	}
	return "/static/" + name + "?v=" + f.etag, nil
}

func (sf *staticFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f, ok := sf.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	h.Set("ETag", `"`+f.etag+`"`)
	if r.URL.Query().Get("v") == f.etag {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	// ServeContent gestiona If-None-Match (304), HEAD y rangos.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.data))
}
