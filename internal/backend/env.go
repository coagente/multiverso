package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/dockerx"
	"github.com/coagente/multiverso/internal/object"
)

// lockfileNames are hashed into the env manifest when present in a world.
var lockfileNames = []string{
	"Cargo.lock", "go.sum", "package-lock.json",
	"poetry.lock", "requirements.txt", "uv.lock",
}

// maxLockfileBytes caps how much of a lockfile the manifest builders read.
// The dir may be a just-raced world whose files are agent-written: hashing
// must never turn into an unbounded read.
const maxLockfileBytes = 64 << 20

// T0EnvDigest builds the M0/M1b env manifest for a directory —
// {"go":"none","os":runtime.GOOS} plus sha256 hashes of any recognized
// lockfiles — stores its canonical bytes in CAS, and returns its digest.
// Byte-identical to M1b's race.EnvDigest (which now delegates here): T0
// world digests must not move (M1c decision 11).
func T0EnvDigest(store *cas.Store, dir string) (string, error) {
	manifest := map[string]any{"go": "none", "os": runtime.GOOS}
	if locks := lockfileHashes(dir); len(locks) > 0 {
		manifest["lockfiles"] = locks
	}
	return putManifest(store, manifest)
}

// t1EnvDigest builds the T1 XP-3 manifest: {image_digest, image_ref,
// lockfiles?, os}. Lockfiles are read from the HOST worktree — the bind
// mount makes the bytes identical and never requires an in-container read
// (decision 11). os is the image's inspected .Os: the world executes
// there; runtime.GOOS would be a lie.
func t1EnvDigest(store *cas.Store, dir string, img dockerx.Image) (string, error) {
	manifest := map[string]any{
		"image_digest": img.Digest,
		"image_ref":    img.Ref,
		"os":           img.OS,
	}
	if locks := lockfileHashes(dir); len(locks) > 0 {
		manifest["lockfiles"] = locks
	}
	return putManifest(store, manifest)
}

// lockfileHashes hashes recognized lockfiles under dir. Only plain,
// readable files under maxLockfileBytes are hashed: a hostile agent's FIFO
// at a lockfile name would block ReadFile forever (the agent is dead, no
// writer ever arrives) and a symlink to a device would read unboundedly.
// Skipped entries are simply absent from the manifest — the world's tree
// digest still records the file itself.
func lockfileHashes(dir string) map[string]any {
	locks := map[string]any{}
	for _, name := range lockfileNames {
		path := filepath.Join(dir, name)
		fi, err := os.Lstat(path)
		if err != nil {
			continue // absent (or unstatable: nothing honest to hash)
		}
		if !fi.Mode().IsRegular() || fi.Size() > maxLockfileBytes {
			continue // FIFOs, symlinks, devices, oversized: never opened
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue // unreadable (e.g. chmod 000): nothing honest to hash
		}
		sum := sha256.Sum256(b)
		locks[name] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return locks
}

// putManifest digests a manifest, stores its canonical bytes in CAS, and
// returns the digest. A returned error is always a control-plane
// (digest/CAS) failure.
func putManifest(store *cas.Store, manifest map[string]any) (string, error) {
	dig, canon, err := object.Digest(manifest)
	if err != nil {
		return "", fmt.Errorf("backend: digest env manifest: %w", err)
	}
	if _, err := store.Put(canon); err != nil {
		return "", fmt.Errorf("backend: store env manifest: %w", err)
	}
	return dig, nil
}
