package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"kenichi-explicit-server/internal/auth"
	"kenichi-explicit-server/internal/config"
	"kenichi-explicit-server/internal/manifest"
)

// maxUploadBytes is the per-request upload size cap (500 MB).
const maxUploadBytes = 500 << 20

// uploadResponse is the JSON body returned after a successful upload.
type uploadResponse struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// RunPrivate starts the private HTTP server on cfg.PrivatePort.
// Returns a non-nil error if the listener fails.
func RunPrivate(cfg *config.Config) error {
	verifier := auth.NewVerifier(cfg.Ed25519PublicKey, cfg.DevMode, cfg.DevSkipAuth)

	var store *manifest.Store
	if !cfg.DevMode {
		if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
			return fmt.Errorf("[private] create data dir: %w", err)
		}
		var err error
		store, err = manifest.NewStore(cfg.DataDir)
		if err != nil {
			return fmt.Errorf("[private] load manifest: %w", err)
		}
	}

	p := &privateHandler{cfg: cfg, verifier: verifier, store: store}

	mux := http.NewServeMux()

	// Manifest endpoints.
	mux.HandleFunc("GET /_manifest/gallery-explicit.json", p.withSmallBodyAuth(p.getManifest))
	mux.HandleFunc("PUT /_manifest/gallery-explicit.json", p.withSmallBodyAuth(p.putManifestEntry))

	// Original file endpoints.
	mux.HandleFunc("GET /_files/originals/{path...}", p.withSmallBodyAuth(p.getOriginal))
	mux.HandleFunc("PUT /_upload/originals/{path...}", p.handleOriginalUpload)
	mux.HandleFunc("DELETE /_files/originals/{path...}", p.withSmallBodyAuth(p.deleteOriginal))

	// Viewer / thumb upload endpoints (streaming auth: body verified inside handler).
	mux.HandleFunc("PUT /_upload/viewer/{path...}", p.handleDerivedUpload("_viewer"))
	mux.HandleFunc("PUT /_upload/thumbs/{path...}", p.handleDerivedUpload("_thumbs"))

	// Viewer / thumb delete endpoints.
	mux.HandleFunc("DELETE /_files/viewer/{path...}", p.withSmallBodyAuth(p.deleteFile("_viewer")))
	mux.HandleFunc("DELETE /_files/thumbs/{path...}", p.withSmallBodyAuth(p.deleteFile("_thumbs")))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	addr := fmt.Sprintf(":%d", cfg.PrivatePort)
	log.Printf("[private] listening on %s (dev=%v)", addr, cfg.DevMode)
	return http.ListenAndServe(addr, mux)
}

// privateHandler groups the cfg, verifier, and manifest store used by all handlers.
type privateHandler struct {
	cfg      *config.Config
	verifier *auth.Verifier
	store    *manifest.Store
}

// ---------------------------------------------------------------------------
// Auth middleware wrappers
// ---------------------------------------------------------------------------

// withSmallBodyAuth reads the entire request body (max 1 MB), verifies auth, then
// calls next with a body-restored request.
func (p *privateHandler) withSmallBodyAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		if err := p.verifier.VerifySmallBody(r, body); err != nil {
			log.Printf("[private] auth denied %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		next(w, r)
	}
}

// ---------------------------------------------------------------------------
// Manifest handlers
// ---------------------------------------------------------------------------

func (p *privateHandler) getManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if p.cfg.DevMode {
		_ = json.NewEncoder(w).Encode(devManifest())
		return
	}
	m := p.store.Get()
	_ = json.NewEncoder(w).Encode(m)
}

// putManifestEntry upserts a single manifest entry. The request body must be a
// JSON object matching manifest.Entry (without updatedAt, which the server sets).
func (p *privateHandler) putManifestEntry(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var entry manifest.Entry
	if err := json.Unmarshal(body, &entry); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if !isValidRelPath(entry.RelativePath) {
		http.Error(w, "invalid relativePath", http.StatusBadRequest)
		return
	}

	if p.cfg.DevMode {
		log.Printf("[private] dev: manifest upsert %s", entry.RelativePath)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := p.store.Upsert(entry); err != nil {
		log.Printf("[private] manifest upsert %s: %v", entry.RelativePath, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Original file handlers
// ---------------------------------------------------------------------------

func (p *privateHandler) getOriginal(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	if p.cfg.DevMode {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(placeholderJPEG())
		return
	}
	serveStoredFile(w, r, filepath.Join(p.cfg.DataDir, "_originals"), rel)
}

// handleOriginalUpload streams the upload body to _originals/<path>, then
// auto-registers the entry in the manifest. Auth is verified after streaming.
func (p *privateHandler) handleOriginalUpload(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	if !isValidRelPath(rel) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	if p.cfg.DevMode {
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxUploadBytes+1))
		sum := sha256.Sum256(body)
		hashHex := hex.EncodeToString(sum[:])
		log.Printf("[private] dev upload originals: path=%s bytes=%d", rel, len(body))
		respondJSON(w, http.StatusCreated, uploadResponse{Hash: hashHex, Size: int64(len(body))})
		return
	}

	hashHex, written, err := p.streamUpload(w, r, "_originals", rel)
	if err != nil {
		return // streamUpload already wrote the error response
	}

	// Auto-register in manifest (preserves existing viewerFile/thumbFile).
	entry := manifest.Entry{
		RelativePath: rel,
		SourceHash:   hashHex,
	}
	if err := p.store.Upsert(entry); err != nil {
		log.Printf("[private] manifest auto-register %s: %v", rel, err)
		// Non-fatal: file is stored, manifest update can be retried.
	}

	respondJSON(w, http.StatusCreated, uploadResponse{Hash: hashHex, Size: written})
}

func (p *privateHandler) deleteOriginal(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	if p.cfg.DevMode {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	deleteStoredFile(w, filepath.Join(p.cfg.DataDir, "_originals"), rel)
}

// ---------------------------------------------------------------------------
// Derived file (viewer / thumb) handlers
// ---------------------------------------------------------------------------

// handleDerivedUpload returns a handler that streams a viewer or thumb upload.
// Auth is verified after streaming (body hash checked against x-content-sha256).
func (p *privateHandler) handleDerivedUpload(subdir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rel := r.PathValue("path")
		if !isValidRelPath(rel) {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}

		if p.cfg.DevMode {
			body, _ := io.ReadAll(io.LimitReader(r.Body, maxUploadBytes+1))
			sum := sha256.Sum256(body)
			hashHex := hex.EncodeToString(sum[:])
			log.Printf("[private] dev upload %s: path=%s bytes=%d", subdir, rel, len(body))
			respondJSON(w, http.StatusCreated, uploadResponse{Hash: hashHex, Size: int64(len(body))})
			return
		}

		hashHex, written, err := p.streamUpload(w, r, subdir, rel)
		if err != nil {
			return
		}
		respondJSON(w, http.StatusCreated, uploadResponse{Hash: hashHex, Size: written})
	}
}

func (p *privateHandler) deleteFile(subdir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rel := r.PathValue("path")
		if p.cfg.DevMode {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		deleteStoredFile(w, filepath.Join(p.cfg.DataDir, subdir), rel)
	}
}

// ---------------------------------------------------------------------------
// Shared upload helper
// ---------------------------------------------------------------------------

// streamUpload streams r.Body to a temp file under dataDir/subdir/rel, computes
// SHA-256 of the body, verifies auth, then commits. Returns hex hash and byte count.
// On any error it writes an appropriate HTTP response and returns a non-nil error.
func (p *privateHandler) streamUpload(w http.ResponseWriter, r *http.Request, subdir, rel string) (hashHex string, written int64, err error) {
	destDir := filepath.Join(p.cfg.DataDir, subdir, filepath.Dir(filepath.FromSlash(rel)))
	if mkErr := os.MkdirAll(destDir, 0755); mkErr != nil {
		log.Printf("[private] mkdir %s: %v", destDir, mkErr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return "", 0, mkErr
	}

	destPath := filepath.Join(p.cfg.DataDir, subdir, filepath.FromSlash(rel))
	if safeJoin(filepath.Join(p.cfg.DataDir, subdir), rel) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return "", 0, fmt.Errorf("path escape")
	}

	// Reject overwrite of originals unless ?force=true.
	if subdir == "_originals" {
		if _, statErr := os.Stat(destPath); statErr == nil {
			if r.URL.Query().Get("force") != "true" && r.URL.Query().Get("force") != "1" {
				http.Error(w, "file exists; add ?force=true to overwrite", http.StatusConflict)
				return "", 0, fmt.Errorf("conflict")
			}
		}
	}

	tmpPath := destPath + ".tmp"
	tmpFile, createErr := os.Create(tmpPath)
	if createErr != nil {
		log.Printf("[private] create temp %s: %v", tmpPath, createErr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return "", 0, createErr
	}

	hasher := sha256.New()
	limited := io.LimitReader(r.Body, maxUploadBytes+1)
	n, copyErr := io.Copy(io.MultiWriter(tmpFile, hasher), limited)
	closeErr := tmpFile.Close()

	if copyErr != nil || closeErr != nil {
		if rmErr := os.Remove(tmpPath); rmErr != nil {
			log.Printf("[private] cleanup temp after write error %s: %v", tmpPath, rmErr)
		}
		if copyErr != nil {
			log.Printf("[private] stream %s: %v", rel, copyErr)
		} else {
			log.Printf("[private] close temp %s: %v", tmpPath, closeErr)
		}
		http.Error(w, "upload failed", http.StatusInternalServerError)
		if copyErr != nil {
			return "", 0, copyErr
		}
		return "", 0, closeErr
	}
	if n > maxUploadBytes {
		if rmErr := os.Remove(tmpPath); rmErr != nil {
			log.Printf("[private] cleanup oversized temp %s: %v", tmpPath, rmErr)
		}
		http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
		return "", 0, fmt.Errorf("too large")
	}

	computedHashHex := hex.EncodeToString(hasher.Sum(nil))

	// Full auth verification (signature covers the computed body hash).
	if authErr := p.verifier.VerifyUploadBody(r, computedHashHex); authErr != nil {
		if rmErr := os.Remove(tmpPath); rmErr != nil {
			log.Printf("[private] cleanup temp after auth failure %s: %v", tmpPath, rmErr)
		}
		log.Printf("[private] auth denied upload %s: %v", rel, authErr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", 0, authErr
	}

	if renameErr := os.Rename(tmpPath, destPath); renameErr != nil {
		_ = os.Remove(tmpPath)
		log.Printf("[private] rename %s: %v", tmpPath, renameErr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return "", 0, renameErr
	}

	return computedHashHex, n, nil
}

// ---------------------------------------------------------------------------
// Low-level file helpers
// ---------------------------------------------------------------------------

func serveStoredFile(w http.ResponseWriter, r *http.Request, dir, rel string) {
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
			log.Printf("[private] open %s: %v", abs, err)
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
	w.Header().Set("Content-Type", detectContentType(rel))
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
}

func deleteStoredFile(w http.ResponseWriter, dir, rel string) {
	if !isValidRelPath(rel) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	abs := safeJoin(dir, rel)
	if abs == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	err := os.Remove(abs)
	if err != nil && !os.IsNotExist(err) {
		log.Printf("[private] delete %s: %v", abs, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func respondJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
