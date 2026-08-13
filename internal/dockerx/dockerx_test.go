package dockerx

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestKeeperArgvGolden(t *testing.T) {
	img := Image{
		Ref:    "python:3.12-alpine",
		Digest: "sha256:" + strings.Repeat("d", 64),
		RunRef: "python@sha256:" + strings.Repeat("d", 64),
		OS:     "linux",
	}
	tests := []struct {
		name string
		opts RunOpts
		want []string
	}{
		{
			name: "full caps",
			opts: RunOpts{
				Image: img, HostDir: "/hosts/w1",
				CPUMilli: 1500, MemoryMB: 512, PidsLimit: 256,
				User: "501:20", TTLSeconds: 1200,
				IntentDigest: "mv0:abc", Name: "mvo-w-0123456789ab",
			},
			want: []string{
				"run", "-d", "--rm", "--name", "mvo-w-0123456789ab",
				"--label", "dev.multiverso=1",
				"--label", "dev.multiverso.intent=mv0:abc",
				"--network", "none",
				"--cap-drop", "ALL",
				"--security-opt", "no-new-privileges",
				"--read-only", "--tmpfs", "/tmp",
				"-v", "/hosts/w1:/work", "-w", "/work",
				"--memory", "512m", "--memory-swap", "512m",
				"--cpus", "1.5",
				"--pids-limit", "256",
				"--user", "501:20",
				"--entrypoint", "sleep", "python@sha256:" + strings.Repeat("d", 64), "1200",
			},
		},
		{
			name: "no caps, no user (image's own user stands)",
			opts: RunOpts{
				Image: img, HostDir: "/hosts/w2", TTLSeconds: 86400,
				IntentDigest: "mv0:abc", Name: "mvo-w-ba9876543210",
			},
			want: []string{
				"run", "-d", "--rm", "--name", "mvo-w-ba9876543210",
				"--label", "dev.multiverso=1",
				"--label", "dev.multiverso.intent=mv0:abc",
				"--network", "none",
				"--cap-drop", "ALL",
				"--security-opt", "no-new-privileges",
				"--read-only", "--tmpfs", "/tmp",
				"-v", "/hosts/w2:/work", "-w", "/work",
				"--entrypoint", "sleep", "python@sha256:" + strings.Repeat("d", 64), "86400",
			},
		},
		{
			name: "allow-network omits the network flag (NFR-4 opt-out)",
			opts: RunOpts{
				Image: img, HostDir: "/hosts/w3", AllowNetwork: true,
				TTLSeconds: 60, IntentDigest: "mv0:abc", Name: "mvo-w-net",
			},
			want: []string{
				"run", "-d", "--rm", "--name", "mvo-w-net",
				"--label", "dev.multiverso=1",
				"--label", "dev.multiverso.intent=mv0:abc",
				"--cap-drop", "ALL",
				"--security-opt", "no-new-privileges",
				"--read-only", "--tmpfs", "/tmp",
				"-v", "/hosts/w3:/work", "-w", "/work",
				"--entrypoint", "sleep", "python@sha256:" + strings.Repeat("d", 64), "60",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := KeeperArgv(tt.opts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("KeeperArgv = %q\nwant %q", got, tt.want)
			}
		})
	}
}

// The hardening riders are constant tier properties and the memory cap
// must never page out: --memory always pairs with an equal --memory-swap,
// and --entrypoint sleep sits immediately before the image reference so an
// image ENTRYPOINT can never reinterpret the keeper.
func TestKeeperArgvInvariants(t *testing.T) {
	argv := KeeperArgv(RunOpts{
		Image:    Image{RunRef: "img@sha256:x"},
		MemoryMB: 64, TTLSeconds: 9, Name: "n", HostDir: "/h",
	})
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--memory 64m --memory-swap 64m") {
		t.Errorf("memory flags unpaired: %s", joined)
	}
	if !strings.Contains(joined, "--entrypoint sleep img@sha256:x 9") {
		t.Errorf("entrypoint/image/ttl tail wrong: %s", joined)
	}
}

func TestExecArgvGolden(t *testing.T) {
	// Entries pass through verbatim: name-only entries make docker resolve
	// the value from the docker client's environment (values — allowlisted
	// secrets included — never sit on the host command line, NFR-4); the
	// constant HOME=/tmp pin is the one inline NAME=value entry.
	got := ExecArgv("cid123", "/work",
		[]string{"FAKE_AGENT_MODE", "HOME=/tmp", "LANG"},
		[]string{"python3", "-m", "pytest", "-q"})
	want := []string{
		"docker", "exec", "-w", "/work",
		"-e", "FAKE_AGENT_MODE",
		"-e", "HOME=/tmp",
		"-e", "LANG",
		"cid123",
		"python3", "-m", "pytest", "-q",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExecArgv = %q\nwant %q", got, want)
	}
	// No -i, no -t: stdin closed — agents must never block on reads.
	for _, flag := range []string{"-i", "-t"} {
		for _, a := range got {
			if a == flag {
				t.Errorf("ExecArgv contains %s", flag)
			}
		}
	}
	// nil env: no -e flags at all.
	bare := ExecArgv("cid123", "/work", nil, []string{"true"})
	if want := []string{"docker", "exec", "-w", "/work", "cid123", "true"}; !reflect.DeepEqual(bare, want) {
		t.Errorf("ExecArgv(nil env) = %q, want %q", bare, want)
	}
}

func TestParseCPUMilli(t *testing.T) {
	accept := map[string]int64{
		"1.5":   1500,
		"1":     1000,
		"0.25":  250,
		"0.001": 1,
		"2.125": 2125,
		"16":    16000,
	}
	for s, want := range accept {
		got, err := ParseCPUMilli(s)
		if err != nil || got != want {
			t.Errorf("ParseCPUMilli(%q) = %d, %v; want %d", s, got, err, want)
		}
	}
	reject := []string{"0", "0.000", "1.2345", ".5", "1,5", "-1", "1.5e0", "", "1.", "abc"}
	for _, s := range reject {
		if got, err := ParseCPUMilli(s); err == nil {
			t.Errorf("ParseCPUMilli(%q) = %d, want error", s, got)
		}
	}
}

func TestFormatCPUMilli(t *testing.T) {
	golden := map[int64]string{
		1500:  "1.5",
		1000:  "1",
		250:   "0.25",
		1:     "0.001",
		2125:  "2.125",
		16000: "16",
	}
	for m, want := range golden {
		if got := FormatCPUMilli(m); got != want {
			t.Errorf("FormatCPUMilli(%d) = %q, want %q", m, got, want)
		}
	}
	// Round-trip law: ParseCPUMilli(FormatCPUMilli(m)) == m for all m ≥ 1.
	for m := int64(1); m <= 5000; m++ {
		back, err := ParseCPUMilli(FormatCPUMilli(m))
		if err != nil || back != m {
			t.Fatalf("round trip %d → %q → %d, %v", m, FormatCPUMilli(m), back, err)
		}
	}
}

// Kill/Remove idempotence mapping: the daemon's already-gone answers are
// success, anything else stays an error.
func TestIsGoneMapping(t *testing.T) {
	gone := []string{
		`dockerx: docker kill x: exit status 1: Error response from daemon: No such container: x`,
		`dockerx: docker kill x: exit status 1: Error response from daemon: cannot kill container: x: container is not running`,
		`dockerx: docker rm -f x: exit status 1: Error response from daemon: removal of container x is already in progress`,
	}
	for _, msg := range gone {
		if !isGone(errors.New(msg)) {
			t.Errorf("isGone(%q) = false, want true", msg)
		}
	}
	if isGone(errors.New("dockerx: docker kill x: permission denied")) {
		t.Error("isGone(permission denied) = true, want false")
	}
	if isGone(nil) {
		t.Error("isGone(nil) = true, want false")
	}
}

func TestParseInspect(t *testing.T) {
	// Registry image: RepoDigests[0] supplies digest and run reference.
	img, err := parseInspect("python:3.12-alpine",
		"linux||sha256:"+strings.Repeat("1", 64)+"|python@sha256:"+strings.Repeat("2", 64))
	if err != nil {
		t.Fatalf("parseInspect: %v", err)
	}
	want := Image{
		Ref:    "python:3.12-alpine",
		Digest: "sha256:" + strings.Repeat("2", 64),
		RunRef: "python@sha256:" + strings.Repeat("2", 64),
		OS:     "linux",
	}
	if img != want {
		t.Errorf("parseInspect = %+v, want %+v", img, want)
	}

	// Local-only image: the ID pins both digest and run reference.
	img, err = parseInspect("local:v1", "linux|worker|sha256:"+strings.Repeat("3", 64)+"|")
	if err != nil {
		t.Fatalf("parseInspect local-only: %v", err)
	}
	if img.Digest != "sha256:"+strings.Repeat("3", 64) || img.RunRef != img.Digest {
		t.Errorf("local-only fallback = %+v, want ID for both digest and run ref", img)
	}
	if img.User != "worker" {
		t.Errorf("user = %q, want worker", img.User)
	}

	if _, err := parseInspect("x", "not-enough-fields"); err == nil {
		t.Error("malformed inspect output accepted")
	}
}
