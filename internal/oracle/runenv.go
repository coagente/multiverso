package oracle

import (
	"os"
	"strings"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// The in-world environment every code-executing rung builds, in ONE place.
//
// It was two identical copies for about an hour, which is exactly how the
// PYTHONPATH bug below gets reintroduced: the rule that matters is a rule
// about a shipped incident, and a second copy of it is a second chance to
// get it wrong.

// evidenceEnv is the in-world environment for one run: the entry-point
// autoload seal, and — when a channel exists — the observer's PYTHONPATH,
// stream path and nonce.
//
// The seal is unconditional on the POLICY, not on the channel: a run with
// no stream is still a run whose exit code reaches a gate, and an
// entry-point plugin can author that. pytest imports pytest11 entry points
// from any *.egg-info / *.dist-info on sys.path, the candidate tree root IS
// on sys.path, and the module they name may be called anything — so no
// harness glob closes the surface and PYTEST_DISABLE_PLUGIN_AUTOLOAD must.
//
// An empty nonce means "no channel": the observer is inert without both
// variables, which is also every local `pytest -p mvo_evidence`.
func evidenceEnv(ev evidencePlan, streamPath, nonce string) []string {
	env := []string{}
	if ev.autoload != policy.AutoloadOn {
		env = append(env, envNoAutoload+"=1")
	}
	if nonce == "" || streamPath == "" {
		return env
	}
	return append(env,
		envPyPath+"="+ev.inWorldPlugin,
		envStream+"="+streamPath,
		envNonce+"="+nonce,
	)
}

// mergeWorldEnv builds the process environment for one in-world run. On T0
// the process inherits the host environment plus our additions (nil extras
// keeps M0/M1b's exact inherit-everything behaviour); on T1 only the
// additions travel, because the image owns PATH and the backend maps names
// to values client-side.
//
// On T0 our PYTHONPATH addition is MERGED with the operator's rather than
// assigned over it. os/exec deduplicates env keys keeping the LAST, so a
// bare assignment silently discarded the ambient value — and a src-layout
// repo, or any repo whose tests import through PYTHONPATH, then collected
// zero tests on the first race a new user ever ran.
func mergeWorldEnv(w backend.World, extra []string) []string {
	if len(extra) == 0 {
		return nil
	}
	if w.Tier() != object.TierT0Worktree {
		return extra
	}
	merged := make([]string, 0, len(extra))
	for _, kv := range extra {
		if name, val, ok := strings.Cut(kv, "="); ok && name == envPyPath {
			kv = envPyPath + "=" + prependPath(val, os.Getenv(envPyPath))
		}
		merged = append(merged, kv)
	}
	return append(os.Environ(), merged...)
}
