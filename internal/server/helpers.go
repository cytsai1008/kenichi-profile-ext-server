package server

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

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

var (
	placeholderOnce  sync.Once
	cachedPlaceholder []byte
)

// placeholderJPEG returns a minimal 1×1 grey JPEG for dev mode responses.
func placeholderJPEG() []byte {
	placeholderOnce.Do(func() {
		img := image.NewGray(image.Rect(0, 0, 1, 1))
		img.SetGray(0, 0, color.Gray{Y: 128})
		var buf bytes.Buffer
		_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 1})
		cachedPlaceholder = buf.Bytes()
	})
	return cachedPlaceholder
}

// devManifest returns a canned manifest for dev mode.
func devManifest() map[string]interface{} {
	return map[string]interface{}{
		"version": 1,
		"entries": []map[string]interface{}{
			{
				"relativePath": "gallery-explicit/test-image.jpg",
				"sourceHash":   "0000000000000000000000000000000000000000000000000000000000000000",
				"viewerFile":   "test-image.00000000.jpg",
				"thumbFile":    "test-image.00000000.jpg",
				"updatedAt":    "2026-01-01T00:00:00Z",
			},
		},
	}
}
