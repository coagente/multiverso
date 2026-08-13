// Package dockerx shells out to the docker CLI for T1 container worlds
// (XP-1; the gitx pattern — see docs/design/M1c-containers-parallel.md).
// It knows docker and nothing about worlds: pure argv builders unit-tested
// by golden, stderr folded into errors, no SDK, no socket protocol, no
// JSON API client — `--format` templates only. Every command runs with a
// docker-client env allowlist (decision 2) so the operator's daemon
// selection works and nothing else leaks into docker's own process.
package dockerx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// availableTimeout bounds the `docker version` daemon probe.
const availableTimeout = 10 * time.Second

// teardownTimeout bounds `docker kill` and `docker rm -f`. A wedged daemon
// is the failure mode that co-occurs with runaway containers, and both
// calls sit on enforcement paths: the runner's watchdog runs World.Kill
// BEFORE the host-group escalation, and the oracle's cmd.Cancel must
// return before os/exec starts its WaitDelay timer — an unbounded docker
// call there would disable exactly the enforcement XP-2 exists for. On
// expiry the host-side kill proceeds; the keeper TTL + --rm remain the
// backstop for the container itself.
const teardownTimeout = 10 * time.Second

// clientEnvNames is the docker-client env allowlist (decision 2): the
// operator's daemon selection (DOCKER_HOST, contexts, TLS) is control-plane
// configuration, not world input; nothing else reaches docker's process.
var clientEnvNames = []string{
	"PATH", "HOME", "USER", "TMPDIR",
	"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG",
	"DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "DOCKER_API_VERSION",
}

// ClientEnv returns the allowlisted docker-client environment: each name
// copied from mvo's own environment when set. It is both the env of every
// dockerx command and the hostEnv of a T1 world's Command mapping.
func ClientEnv() []string {
	env := make([]string, 0, len(clientEnvNames))
	for _, name := range clientEnvNames {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// run executes docker with the client env allowlist and returns trimmed
// stdout. On failure the captured stderr is folded into the error.
func run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = ClientEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("dockerx: docker %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Available checks the docker CLI is on PATH and the daemon answers
// `docker version` (10 s timeout). The error text is operator-facing.
func Available() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("dockerx: docker CLI not found in PATH: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), availableTimeout)
	defer cancel()
	if _, err := run(ctx, "version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("dockerx: daemon probe failed: %w", err)
	}
	return nil
}

// Image is a resolved, digest-pinned image reference (decision 10).
type Image struct {
	Ref    string // operator's flag value, verbatim ("python:3.12-alpine")
	Digest string // "sha256:…" — RepoDigests[0] digest, or the image ID for local-only images
	RunRef string // what containers start FROM: "repo@sha256:…", or the image ID
	OS     string // inspected .Os ("linux")
	User   string // inspected .Config.User ("" when unset)
}

// inspectFormat pulls the four fields ResolveImage needs in one call.
// RepoDigests entries never contain spaces, so a space-separated range is
// unambiguous (the template `join` builtin rejects the []interface{} the
// daemon hands newer CLIs).
const inspectFormat = `{{.Os}}|{{.Config.User}}|{{.Id}}|{{range .RepoDigests}}{{.}} {{end}}`

// ResolveImage inspects ref, pulling once if absent (stderr visible — the
// operator sees the one-time cost), and pins it: RepoDigests[0] supplies
// the digest and the run reference ("repo@sha256:…") — TOCTOU-free, what
// was recorded is what runs. Local-only images (built, never pushed: no
// RepoDigests) fall back to the image ID (config digest) for both — an ID
// pins content exactly but is not a registry manifest digest.
func ResolveImage(ref string) (Image, error) {
	if ref == "" {
		return Image{}, errors.New("dockerx: empty image reference")
	}
	out, err := run(context.Background(), "image", "inspect", "--format", inspectFormat, ref)
	if err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "no such image") {
			return Image{}, err // a real inspect failure, not an absent image
		}
		// Absent locally: pull once, stderr passed through to the operator.
		pull := exec.Command("docker", "pull", ref)
		pull.Env = ClientEnv()
		pull.Stdout = os.Stderr
		pull.Stderr = os.Stderr
		if pullErr := pull.Run(); pullErr != nil {
			return Image{}, fmt.Errorf("dockerx: image %q not present and pull failed: %w (inspect: %v)", ref, pullErr, err)
		}
		if out, err = run(context.Background(), "image", "inspect", "--format", inspectFormat, ref); err != nil {
			return Image{}, err
		}
	}
	return parseInspect(ref, out)
}

// parseInspect decodes one inspectFormat line into a pinned Image.
func parseInspect(ref, out string) (Image, error) {
	parts := strings.SplitN(out, "|", 4)
	if len(parts) != 4 {
		return Image{}, fmt.Errorf("dockerx: unexpected inspect output for %q: %q", ref, out)
	}
	img := Image{Ref: ref, OS: parts[0], User: parts[1]}
	id := parts[2]
	repoDigests := strings.Fields(parts[3])
	if len(repoDigests) > 0 {
		// "repo@sha256:…" — the digest is the part after "@".
		rd := repoDigests[0]
		at := strings.LastIndex(rd, "@")
		if at < 0 {
			return Image{}, fmt.Errorf("dockerx: malformed RepoDigest %q for %q", rd, ref)
		}
		img.Digest = rd[at+1:]
		img.RunRef = rd
		return img, nil
	}
	// Local-only image: the ID pins content exactly (honestly labeled by
	// the M1c contract as a config digest, not a registry manifest digest).
	if id == "" {
		return Image{}, fmt.Errorf("dockerx: image %q has neither RepoDigests nor an ID", ref)
	}
	img.Digest = id
	img.RunRef = id
	return img, nil
}

// RunOpts parameterizes one keeper container (KeeperArgv is the pure,
// golden-tested builder; RunKeeper executes it and returns the cid).
type RunOpts struct {
	Image        Image
	HostDir      string // bind-mounted at /work
	CPUMilli     int64
	MemoryMB     int64
	PidsLimit    int64
	AllowNetwork bool
	User         string // "uid:gid" or "" (image's own user stands)
	TTLSeconds   int64
	IntentDigest string
	Name         string // "mvo-w-<12 hex>"
}

// KeeperArgv builds the keeper `docker run` argv (normative shape, M1c):
// flags appear iff their knob is set, order fixed. `--entrypoint sleep`
// (not a bare command) so image ENTRYPOINTs can never reinterpret the
// keeper; `--memory-swap` equals `--memory` so the cap cannot page out;
// `sleep <integer seconds>` because `sleep infinity` is not portable to
// busybox.
func KeeperArgv(o RunOpts) []string {
	argv := []string{
		"run", "-d", "--rm", "--name", o.Name,
		"--label", "dev.multiverso=1",
		"--label", "dev.multiverso.intent=" + o.IntentDigest,
	}
	if !o.AllowNetwork {
		argv = append(argv, "--network", "none")
	}
	argv = append(argv,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--read-only", "--tmpfs", "/tmp",
		"-v", o.HostDir+":/work", "-w", "/work",
	)
	if o.MemoryMB > 0 {
		m := strconv.FormatInt(o.MemoryMB, 10) + "m"
		argv = append(argv, "--memory", m, "--memory-swap", m)
	}
	if o.CPUMilli > 0 {
		argv = append(argv, "--cpus", FormatCPUMilli(o.CPUMilli))
	}
	if o.PidsLimit > 0 {
		argv = append(argv, "--pids-limit", strconv.FormatInt(o.PidsLimit, 10))
	}
	if o.User != "" {
		argv = append(argv, "--user", o.User)
	}
	return append(argv, "--entrypoint", "sleep", o.Image.RunRef, strconv.FormatInt(o.TTLSeconds, 10))
}

// RunKeeper starts the keeper container and returns its container ID. It
// honors ctx (the race deadline / first-failure cancellation): a hung
// `docker run` must not keep a whole race waiting in Open. If ctx expires
// after the daemon created the container but before the cid was read, the
// keeper TTL + --rm reap the orphan.
func RunKeeper(ctx context.Context, o RunOpts) (string, error) {
	cid, err := run(ctx, KeeperArgv(o)...)
	if err != nil {
		return "", err
	}
	if cid == "" {
		return "", fmt.Errorf("dockerx: docker run of %s printed no container ID", o.Image.RunRef)
	}
	return cid, nil
}

// ExecArgv builds the in-world invocation: docker exec -w workdir
// [-e NAME | -e NAME=value …] cid argv… — env entries pass through
// verbatim and pre-sorted by the caller; no -i, no -t (stdin closed:
// agents must never block on reads, M1b rule). A name-only entry makes
// docker resolve the value from the docker CLIENT's process environment —
// the decision-7 transport for every caller-supplied value, so allowlisted
// secrets never sit on the host command line (NFR-4); only the constant
// HOME=/tmp pin rides inline.
func ExecArgv(cid, workdir string, env []string, argv []string) []string {
	full := []string{"docker", "exec", "-w", workdir}
	for _, kv := range env {
		full = append(full, "-e", kv)
	}
	full = append(full, cid)
	return append(full, argv...)
}

// noSuchContainerRe spots the daemon's already-gone answer for kill/rm —
// idempotence: a dead world is not an error.
var noSuchContainerRe = regexp.MustCompile(`(?i)no such container|is not running|already in progress`)

// isGone reports whether err means the container is already gone (or on
// its way out via --rm), which Kill and Remove treat as success.
func isGone(err error) bool {
	return err != nil && noSuchContainerRe.MatchString(err.Error())
}

// Kill SIGKILLs the container's PID 1, tearing down the pid namespace and
// with it every in-container process. "No such container" → nil (already
// dead — idempotent). Bounded by teardownTimeout so a wedged daemon can
// only delay, never disable, the caller's host-side escalation.
func Kill(cid string) error {
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	if _, err := run(ctx, "kill", cid); err != nil && !isGone(err) {
		return err
	}
	return nil
}

// Remove force-removes the container. Idempotent: "No such container" →
// nil. The ultimate cleanup on every path (NFR-3). Bounded by
// teardownTimeout: on expiry the keeper TTL + --rm reap the container.
func Remove(cid string) error {
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	if _, err := run(ctx, "rm", "-f", cid); err != nil && !isGone(err) {
		return err
	}
	return nil
}

// cpuMilliRe is the strict --cpus flag grammar: a decimal with at most 3
// fractional digits (milli-CPU resolution), no signs, no exponents.
var cpuMilliRe = regexp.MustCompile(`^\d+(\.\d{1,3})?$`)

// ParseCPUMilli parses a CLI decimal ("1.5") into milli-CPUs. Strict:
// ^\d+(\.\d{1,3})?$ and > 0. Integer arithmetic only (usd.go pattern);
// round-trip law: ParseCPUMilli(FormatCPUMilli(m)) == m for all m ≥ 1.
func ParseCPUMilli(s string) (int64, error) {
	if !cpuMilliRe.MatchString(s) {
		return 0, fmt.Errorf("dockerx: invalid cpu value %q (want a decimal with at most 3 fractional digits)", s)
	}
	whole, frac, _ := strings.Cut(s, ".")
	for len(frac) < 3 {
		frac += "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("dockerx: invalid cpu value %q: %w", s, err)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("dockerx: invalid cpu value %q: %w", s, err)
	}
	m := w*1000 + f
	if m <= 0 {
		return 0, fmt.Errorf("dockerx: cpu value %q must be positive", s)
	}
	return m, nil
}

// FormatCPUMilli renders the shortest exact decimal for the --cpus flag:
// 1500 → "1.5", 1000 → "1", 250 → "0.25".
func FormatCPUMilli(m int64) string {
	whole, frac := m/1000, m%1000
	if frac == 0 {
		return strconv.FormatInt(whole, 10)
	}
	s := fmt.Sprintf("%d.%03d", whole, frac)
	return strings.TrimRight(s, "0")
}
