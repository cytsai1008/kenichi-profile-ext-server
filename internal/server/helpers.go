package server

import (
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

// maxQueryStringBytes is the maximum raw query string length accepted.
const maxQueryStringBytes = 512

// rejectDangerousQuery is a middleware that blocks requests whose raw query
// string contains null bytes, newline characters (log-injection), or exceeds
// the allowed length.
func rejectDangerousQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qs := r.URL.RawQuery
		if len(qs) > maxQueryStringBytes {
			log.Printf("[request] query string too long (%d bytes): %s %s", len(qs), r.Method, r.URL.Path)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		for _, ch := range qs {
			if ch == 0 || ch == '\n' || ch == '\r' {
				log.Printf("[request] dangerous query string char %U: %s %s", ch, r.Method, r.URL.Path)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isValidRelPath returns true if p is safe to use as a relative path within a
// data directory: non-empty, no null bytes, canonical (no . or .. components),
// not absolute, and free of Windows path characters.
func isValidRelPath(p string) bool {
	if p == "" || strings.ContainsRune(p, 0) {
		return false
	}
	// Reject backslashes and colons (Windows paths, drive letters).
	if strings.ContainsAny(p, `\:`) {
		return false
	}
	// Use path.Clean (always forward-slash) to normalise, then require the path
	// to be unchanged. This rejects "a/../b" (cleans to "b"), "a//b", "./a", etc.
	// It also catches leading ".." after cleaning.
	clean := path.Clean(p)
	if clean != p {
		return false
	}
	// Belt-and-suspenders: cleaned form must not be ".", absolute, or a traversal.
	if clean == "." || path.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return false
	}
	return true
}

// safeJoin joins base and rel, then verifies the result is still under base.
// Returns "" if rel would escape base.
func safeJoin(base, rel string) string {
	joined := filepath.Join(base, filepath.FromSlash(rel))
	// Ensure joined is under base (add separator to avoid prefix false-positives).
	if !strings.HasPrefix(joined, base+string(filepath.Separator)) && joined != base {
		return ""
	}
	return joined
}

// detectContentType returns the MIME type for common image file extensions.
func detectContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".avif":
		return "image/avif"
	default:
		return "application/octet-stream"
	}
}
