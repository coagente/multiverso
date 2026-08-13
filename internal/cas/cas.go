// Package cas implements a file-based content-addressed store (DP-2):
// raw bytes stored at <root>/sha256/<first2>/<rest>, keyed "sha256:<hex>".
package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const keyPrefix = "sha256:"

// Store is a content-addressed store rooted at a directory
// (M0: .multiverso/cas).
type Store struct {
	root string
}

// Open creates (if needed) and returns a store rooted at dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "sha256"), 0o755); err != nil {
		return nil, fmt.Errorf("cas: open %s: %w", dir, err)
	}
	return &Store{root: dir}, nil
}

// Put stores b and returns its key "sha256:<hex>". Idempotent: re-putting
// existing content succeeds without rewriting. Writes go to a temp file in
// the destination directory followed by an atomic rename.
func (s *Store) Put(b []byte) (string, error) {
	sum := sha256.Sum256(b)
	key := keyPrefix + hex.EncodeToString(sum[:])
	path, err := s.keyPath(key)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return key, nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cas: put %s: %w", key, err)
	}
	tmp, err := os.CreateTemp(dir, ".put-*")
	if err != nil {
		return "", fmt.Errorf("cas: put %s: %w", key, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("cas: put %s: write temp: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("cas: put %s: close temp: %w", key, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("cas: put %s: rename: %w", key, err)
	}
	return key, nil
}

// Get returns the bytes stored under key, re-hashed against the key so a
// corrupted or tampered blob is never served as authentic content —
// consumers (race, audit) act on what Get returns.
func (s *Store) Get(key string) ([]byte, error) {
	path, err := s.keyPath(key)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cas: get %s: %w", key, err)
	}
	sum := sha256.Sum256(b)
	if got := keyPrefix + hex.EncodeToString(sum[:]); got != key {
		return nil, fmt.Errorf("cas: get %s: content corrupted: bytes hash to %s", key, got)
	}
	return b, nil
}

// Has reports whether key is present.
func (s *Store) Has(key string) bool {
	path, err := s.keyPath(key)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func (s *Store) keyPath(key string) (string, error) {
	hexDigest, ok := strings.CutPrefix(key, keyPrefix)
	if !ok || len(hexDigest) != sha256.Size*2 {
		return "", fmt.Errorf("cas: invalid key %q", key)
	}
	if _, err := hex.DecodeString(hexDigest); err != nil {
		return "", fmt.Errorf("cas: invalid key %q: %w", key, err)
	}
	return filepath.Join(s.root, "sha256", hexDigest[:2], hexDigest[2:]), nil
}
