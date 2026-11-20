package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func get(t *testing.T, h http.Handler, target string, hdr map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestIndex(t *testing.T) {
	res := get(t, newTestServer(t), "/", nil)
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("estado = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	for _, want := range []string{"<!DOCTYPE html>", "Qaphi en marcha", `href="/static/style.css?v=`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("la página no contiene %q", want)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t)
	for _, target := range []string{"/", "/static/style.css", "/no-existe"} {
		res := get(t, s, target, nil)
		for h, want := range map[string]string{
			"X-Frame-Options":        "DENY",
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "same-origin",
		} {
			if got := res.Header.Get(h); got != want {
				t.Errorf("%s: %s = %q, se esperaba %q", target, h, got, want)
			}
		}
		csp := res.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s: CSP = %q", target, csp)
		}
	}
}

func TestNotFound(t *testing.T) {
	s := newTestServer(t)
	for _, target := range []string{"/no-existe", "/static/nada.css"} {
		if res := get(t, s, target, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: estado = %d, se esperaba 404", target, res.StatusCode)
		}
	}
}

func TestMethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	newTestServer(t).ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("estado = %d, se esperaba 405", rec.Code)
	}
}

func TestStatic(t *testing.T) {
	s := newTestServer(t)
	url, err := s.static.url("style.css")
	if err != nil {
		t.Fatal(err)
	}
	res := get(t, s, url, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("estado = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("con huella correcta debe ser inmutable: %q", cc)
	}
	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Fatal("falta ETag")
	}

	// Sin huella: se revalida.
	res = get(t, s, "/static/style.css", nil)
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("sin huella: Cache-Control = %q", cc)
	}
	// Revalidación con ETag: 304.
	res = get(t, s, "/static/style.css", map[string]string{"If-None-Match": etag})
	if res.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: estado = %d, se esperaba 304", res.StatusCode)
	}
	if _, err := s.static.url("no-existe.js"); err == nil {
		t.Error("asset de un fichero inexistente debe fallar")
	}
}

func TestRecoverPanics(t *testing.T) {
	var logs strings.Builder
	log := slog.New(slog.NewTextHandler(&logs, nil))
	h := recoverPanics(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("algo salió mal")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("estado = %d, se esperaba 500", rec.Code)
	}
	if !strings.Contains(logs.String(), "algo salió mal") {
		t.Errorf("el pánico no se registró: %s", logs.String())
	}
}

func TestLogRequestsSinQuery(t *testing.T) {
	var logs strings.Builder
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := logRequests(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ruta?token=secreto", nil))
	out := logs.String()
	if strings.Contains(out, "secreto") {
		t.Errorf("la query string no debe aparecer en el log: %s", out)
	}
	if !strings.Contains(out, "/ruta") || !strings.Contains(out, "418") {
		t.Errorf("faltan ruta o estado en el log: %s", out)
	}
}
