// Package manifest handles reading and atomic writing of gallery-explicit.json.
package manifest

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry mirrors the RemoteManifestEntry TypeScript type in galleryAssetShared.ts.
type Entry struct {
	// RelativePath is the stable namespaced logical path, e.g. "gallery-explicit/Baka_inuta.jpg".
	RelativePath string `json:"relativePath"`
	// SourceHash is the SHA-256 hex of the stored original file.
	SourceHash string `json:"sourceHash"`
	// ViewerFile is the hashed viewer filename, e.g. "Baka_inuta.abcd1234.jpg".
	ViewerFile string `json:"viewerFile"`
	// ThumbFile is the hashed thumb filename, e.g. "Baka_inuta.abcd1234.jpg".
	ThumbFile string `json:"thumbFile"`
	// UpdatedAt is the ISO date of last update.
	UpdatedAt string `json:"updatedAt"`
}

// Manifest is the shape of gallery-explicit.json.
type Manifest struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Store provides safe concurrent access to the on-disk manifest.
type Store struct {
	mu   sync.RWMutex
	path string
	m    Manifest
}

// NewStore loads or initialises the manifest at dataDir/gallery-explicit.json.
func NewStore(dataDir string) (*Store, error) {
	s := &Store{
		path: filepath.Join(dataDir, "gallery-explicit.json"),
	}
	if err := s.load(); err != nil {
		if os.IsNotExist(err) {
			s.m = Manifest{Version: 1, Entries: []Entry{}}
		} else {
			return nil, fmt.Errorf("loading manifest: %w", err)
		}
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.m)
}

// Get returns a copy of the current manifest.
func (s *Store) Get() Manifest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := Manifest{Version: s.m.Version, Entries: make([]Entry, len(s.m.Entries))}
	copy(cp.Entries, s.m.Entries)
	return cp
}

// HasEntry reports whether an entry with the given relativePath exists.
func (s *Store) HasEntry(relativePath string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.m.Entries {
		if e.RelativePath == relativePath {
			return true
		}
	}
	return false
}

// Upsert inserts or updates an entry identified by RelativePath.
// Fields not present in patch (empty strings) are preserved from the existing entry.
// The disk file is written before in-memory state is updated; if the write fails,
// s.m is left unchanged.
func (s *Store) Upsert(patch Entry) error {
	if patch.UpdatedAt == "" {
		patch.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Build a candidate entries slice without mutating s.m.Entries yet.
	candidate := make([]Entry, len(s.m.Entries))
	copy(candidate, s.m.Entries)

	found := false
	for i, e := range candidate {
		if e.RelativePath == patch.RelativePath {
			// Preserve fields that the caller left empty.
			if patch.SourceHash == "" && e.SourceHash != "" {
				log.Printf("[manifest] upsert %s: patch has no SourceHash, existing hash will be cleared", patch.RelativePath)
			}
			if patch.ViewerFile == "" {
				patch.ViewerFile = e.ViewerFile
			}
			if patch.ThumbFile == "" {
				patch.ThumbFile = e.ThumbFile
			}
			candidate[i] = patch
			found = true
			break
		}
	}
	if !found {
		candidate = append(candidate, patch)
	}

	next := Manifest{Version: s.m.Version, Entries: candidate}
	if err := s.writeManifest(next); err != nil {
		return err
	}
	s.m = next
	return nil
}

// Delete removes the entry with the given relativePath. Returns nil if not found.
// The disk file is written before in-memory state is updated.
func (s *Store) Delete(relativePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	candidate := s.m.Entries[:0:0] // new backing array, don't mutate s.m.Entries
	for _, e := range s.m.Entries {
		if e.RelativePath != relativePath {
			candidate = append(candidate, e)
		}
	}

	next := Manifest{Version: s.m.Version, Entries: candidate}
	if err := s.writeManifest(next); err != nil {
		return err
	}
	s.m = next
	return nil
}

// writeManifest atomically persists m to disk. Must be called with mu held.
func (s *Store) writeManifest(m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp manifest: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename manifest: %w", err)
	}
	return nil
}
