package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/dockerx"
	"github.com/coagente/multiverso/internal/object"
)

// t1Backend provisions one long-lived keeper container per world (M1c
// decision 4); agent and oracle run inside it via docker exec, joining the
// container's cgroups so the world's caps cover them (decision 6).
type t1Backend struct {
	cfg Config
}

// Tier implements Backend.
func (*t1Backend) Tier() string { return object.TierT1Container }

// ImageDigest reports the pinned image digest — observational, consumed by
// race.started's exec_image_digest key via type assertion (the Backend
// interface itself stays two methods).
func (b *t1Backend) ImageDigest() string { return b.cfg.Image.Digest }

// ImageRef reports the image reference AS THE OPERATOR NAMED IT — the
// human-facing half of the same pin, used by the race's pytest pre-flight to
// say which environment it probed (M1e decision 15). Same assertion
// discipline as ImageDigest: the Backend interface stays two methods.
func (b *t1Backend) ImageRef() string { return b.cfg.Image.Ref }

// Open implements Backend: docker run of the keeper container, bind-
// mounting the worktree at /work (never docker cp — decision 3: one
// mutable state, host-side evidence capture runs unchanged).
func (b *t1Backend) Open(ctx context.Context, dir string) (World, error) {
	// Non-root when the image supports it (decision 8): only a named
	// NON-ROOT image user stands; an unset user — or an explicit root in
	// any spelling ("root", "0", "0:0") — runs the keeper as the invoking
	// mvo process instead, so /work writes are correctly owned on Linux
	// hosts. The EFFECTIVE user is what isolation_caps.user records.
	runUser := ""
	effectiveUser := b.cfg.Image.User
	if !imageUserIsNonRoot(effectiveUser) {
		runUser = fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
		effectiveUser = runUser
	}
	// TTL (decision 4): a crashed mvo leaves no immortal container — the
	// keeper expires and --rm removes it. 86400 s when the intent carries
	// no wall budget.
	ttl := int64(86400)
	if b.cfg.KeeperTTL > 0 {
		ttl = int64((b.cfg.KeeperTTL + 999999999) / 1000000000) // seconds, rounded up
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return nil, fmt.Errorf("backend: world name entropy: %w", err)
	}
	cid, err := dockerx.RunKeeper(ctx, dockerx.RunOpts{
		Image:        b.cfg.Image,
		HostDir:      dir,
		CPUMilli:     b.cfg.CPUMilli,
		MemoryMB:     b.cfg.MemoryMB,
		PidsLimit:    b.cfg.PidsLimit,
		AllowNetwork: b.cfg.AllowNetwork,
		User:         runUser,
		TTLSeconds:   ttl,
		IntentDigest: b.cfg.IntentDigest,
		Name:         "mvo-w-" + hex.EncodeToString(suffix),
	})
	if err != nil {
		return nil, fmt.Errorf("backend: open T1 world: %w", err)
	}
	network := object.NetworkNone
	if b.cfg.AllowNetwork {
		network = object.NetworkDefault
	}
	return &t1World{
		cid:   cid,
		dir:   dir,
		image: b.cfg.Image,
		caps: object.IsolationCaps{
			CapDrop:      "ALL",
			CPUMilli:     b.cfg.CPUMilli,
			MemoryBytes:  b.cfg.MemoryMB << 20,
			Network:      network,
			PidsLimit:    b.cfg.PidsLimit,
			ReadOnlyRoot: true,
			User:         effectiveUser,
		},
	}, nil
}

// t1World is one keeper container. Containers are transport, not
// evidence: the cid appears nowhere in the ledger — the image digest is
// the evidence and lives in the env manifest and receipts.
type t1World struct {
	cid   string
	dir   string
	image dockerx.Image
	caps  object.IsolationCaps
}

// Tier implements World.
func (*t1World) Tier() string { return object.TierT1Container }

// Dir implements World: the HOST worktree path — the bind mount makes it
// the same bytes the container sees at /work (AG-4 capture surface).
func (w *t1World) Dir() string { return w.dir }

// Command implements World: docker exec argv with the decision-7 env
// mapping. The host process runs the docker client, so hostEnv is the
// docker-client allowlist (decision 2) plus the in-world NAME=value pairs
// the argv's name-only -e flags resolve from — values travel in the
// client's process environment, never on the host command line, so an
// allowlisted secret (ANTHROPIC_API_KEY, CODEX_API_KEY) is not readable
// out of the world-visible process table (NFR-4; M1b's cmd.Env posture).
func (w *t1World) Command(argv, env []string) ([]string, []string) {
	flags, pairs := t1Env(env)
	return dockerx.ExecArgv(w.cid, "/work", flags, argv),
		append(dockerx.ClientEnv(), pairs...)
}

// Kill implements World: docker kill — SIGKILL to PID 1 tears down the pid
// namespace, killing every in-container process (decision 5). Idempotent.
func (w *t1World) Kill() error { return dockerx.Kill(w.cid) }

// Caps implements World.
func (w *t1World) Caps() object.IsolationCaps { return w.caps }

// EnvDigest implements World: the T1 XP-3 manifest, image-pinned.
func (w *t1World) EnvDigest(store *cas.Store) (string, error) {
	return t1EnvDigest(store, w.dir, w.image)
}

// Close implements World: docker rm -f, the ultimate cleanup on every
// path. Idempotent, safe after Kill.
func (w *t1World) Close() error { return dockerx.Remove(w.cid) }

// t1FilteredNames are dropped from caller-supplied env pairs before an
// exec (decision 7): container images own their PATH, /tmp owns temp, and
// host HOME/USER are meaningless inside T1.
var t1FilteredNames = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "USER": true,
}

// t1Env is the normative T1 in-world env mapping (decision 7): filter
// {PATH, HOME, TMPDIR, USER} out of the caller-supplied pairs, inject
// HOME=/tmp, keep the rest. The kept pairs split into two channels: flags
// carries name-only -e entries (sorted — deterministic argv) plus the one
// inline HOME=/tmp pin, and pairs carries the kept NAME=value pairs for
// the docker client's environment, where docker resolves the name-only
// flags from. HOME=/tmp alone rides inline because resolving it
// client-side would mean overriding the client's own HOME and breaking
// the operator's docker config lookup (decision 2); it is a non-secret
// constant.
func t1Env(env []string) (flags, pairs []string) {
	flags = make([]string, 0, len(env)+1)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if t1FilteredNames[name] {
			continue
		}
		flags = append(flags, name)
		pairs = append(pairs, kv)
	}
	flags = append(flags, "HOME=/tmp")
	sort.Strings(flags)
	sort.Strings(pairs)
	return flags, pairs
}

// imageUserIsNonRoot reports whether an image's .Config.User names a
// non-root user — only then does the image's choice stand (decision 8).
// Docker's USER grammar is user[:group]; root is "root" or uid 0 in any
// spelling ("0", "0:0", "root:root").
func imageUserIsNonRoot(u string) bool {
	name, _, _ := strings.Cut(u, ":")
	if name == "" || name == "root" {
		return false
	}
	if n, err := strconv.ParseUint(name, 10, 32); err == nil && n == 0 {
		return false
	}
	return true
}
