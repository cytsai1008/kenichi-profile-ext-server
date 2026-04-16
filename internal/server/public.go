package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"kenichi-explicit-server/internal/config"
)

// RunPublic starts the public HTTP server on cfg.PublicPort.
// It serves only /_viewer/ and /_thumbs/ paths.
// Returns a non-nil error if the listener fails.
func RunPublic(cfg *config.Config) error {
	mux := http.NewServeMux()

	if cfg.DevMode {
		mux.HandleFunc("/_viewer/{path...}", devImageHandler)
		mux.HandleFunc("/_thumbs/{path...}", devImageHandler)
	} else {
		viewerDir := filepath.Join(cfg.DataDir, "_viewer")
		thumbsDir := filepath.Join(cfg.DataDir, "_thumbs")
		mux.HandleFunc("/_viewer/{path...}", makeFileHandler(viewerDir))
		mux.HandleFunc("/_thumbs/{path...}", makeFileHandler(thumbsDir))
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	addr := fmt.Sprintf(":%d", cfg.PublicPort)
	log.Printf("[public] listening on %s (dev=%v)", addr, cfg.DevMode)
	return http.ListenAndServe(addr, mux)
}

// makeFileHandler returns a handler that serves files from dir using hashed-filename
// immutable cache headers.
func makeFileHandler(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		rel := r.PathValue("path")
		if !isValidRelPath(rel) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		abs := safeJoin(dir, rel)
		if abs == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		f, err := os.Open(abs)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "not found", http.StatusNotFound)
			} else {
				log.Printf("[public] open %s: %v", abs, err)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		defer f.Close()

		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Hashed filenames are immutable — safe for a 1-year cache.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("Content-Type", detectContentType(rel))
		http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
	}
}

// devImageHandler returns a 1×1 placeholder JPEG for any /_viewer/ or /_thumbs/ request.
func devImageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "image/jpeg")
	if r.Method == http.MethodGet {
		_, _ = w.Write(placeholderJPEG())
	}
}
