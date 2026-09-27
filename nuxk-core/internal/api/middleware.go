package api

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

// Unwrap lets http.ResponseController reach the real writer (flush, deadlines
// for the SSE stream).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/api/v1/healthz" {
			return // don't spam logs with liveness checks
		}
		slog.Debug("http",
			"method", r.Method, "path", r.URL.Path,
			"code", rec.code, "dur", time.Since(start).Round(time.Millisecond))
	})
}

// spaFallback serves static files from root, falling back to index.html for
// client-side routes (paths without an extension that don't exist on disk).
func spaFallback(root string, fs http.Handler) http.Handler {
	index := filepath.Join(root, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// the page is asked for again each time — after an update the new
		// one must load; the hashed files under /assets/ may be kept
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." || !fileExists(filepath.Join(root, clean)) {
			if !strings.Contains(filepath.Base(clean), ".") {
				http.ServeFile(w, r, index)
				return
			}
		}
		fs.ServeHTTP(w, r)
	})
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
