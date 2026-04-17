package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"kenichi-explicit-server/internal/config"

	"github.com/unrolled/secure"
)

// RunPublic starts the public HTTP server on cfg.PublicPort.
// It serves only /_viewer/ and /_thumbs/ paths.
// Returns a non-nil error if the listener fails.
func RunPublic(cfg *config.Config) error {
	mux := http.NewServeMux()

	viewerDir := filepath.Join(cfg.DataDir, "_viewer")
	thumbsDir := filepath.Join(cfg.DataDir, "_thumbs")
	mux.HandleFunc("/_viewer/{path...}", makeFileHandler(viewerDir))
	mux.HandleFunc("/_thumbs/{path...}", makeFileHandler(thumbsDir))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	sec := secure.New(secure.Options{
		// Prevent browsers from MIME-sniffing away from the declared Content-Type.
		ContentTypeNosniff: true,
		// Disallow embedding this server in any frame or iframe.
		FrameDeny: true,
		// Legacy XSS auditor header — still respected by older browsers.
		BrowserXssFilter: true,
		// Suppress referrer information on cross-origin requests.
		ReferrerPolicy: "no-referrer",
		// In dev mode, relax host enforcement so local docker works without extra config.
		IsDevelopment: cfg.DevMode,
		// No SSLRedirect / HSTS — TLS is terminated upstream by Cloudflare.
		// No CSP — this server never renders HTML.
	})

	addr := fmt.Sprintf(":%d", cfg.PublicPort)
	log.Printf("[public] listening on %s", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           rejectDangerousQuery(sec.Handler(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return srv.ListenAndServe()
}

// makeFileHandler returns a handler that serves files from dir using hashed-filename
// immutable cache headers.
func makeFileHandler(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			log.Printf("[public] method not allowed: %s %s", r.Method, r.URL.Path)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		rel := r.PathValue("path")
		if !isValidRelPath(rel) {
			log.Printf("[public] invalid path: %q", rel)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		abs := safeJoin(dir, rel)
		if abs == "" {
			log.Printf("[public] path escape attempt: %q", rel)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		f, err := os.Open(abs)
		if err != nil {
			if os.IsNotExist(err) {
				log.Printf("[public] not found: %s", rel)
				http.Error(w, "not found", http.StatusNotFound)
			} else {
				log.Printf("[public] open %s: %v", abs, err)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		defer func() { _ = f.Close() }()

		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			log.Printf("[public] not found (stat): %s", rel)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		log.Printf("[public] served %s", rel)
		// Hashed filenames are immutable — safe for a 1-year cache.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("Content-Type", detectContentType(rel))
		http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
	}
}
