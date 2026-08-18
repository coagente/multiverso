package eval

// M2d.1 §2 — WARMING: pricing the cost table before the arms race against it.
//
// On a fresh workspace nothing is priced. Every rung reads `declared-rank`,
// M2b.2 decision 1's unpriced fallback fires on every step, `voc2` IS `voc`,
// and — the failure nobody had named — an unpriced purchase is affordable
// while any pool remains, so THE BUDGET DOES NOT BIND AT ALL. Measured on
// this repository's own fixture: 2 164 ms spent against a 1 500 ms bound,
// stopping `S-empty` rather than `S-budget`. A cell that cannot bind its own
// budget is not an oracle-budget-matched cell in the sense its caption
// claims, whichever rule allocates it.
//
// `scripts/schedule-compare.sh` has warmed since M2b.1 and `mvo-eval` has
// not, which is why every M2d cell was byte-identical between the two
// allocation rules. This file is that mechanism, lifted into the code.
//
// THREE PROPERTIES, AND EACH IS A DECISION RATHER THAN AN IMPLEMENTATION
// DETAIL.
//
//  1. WARMING IS A PREDICATE ON THE COST TABLE, NOT A COUNT OF RACES
//     (decision 1). A count is wrong in both directions: one race on a
//     2-candidate fixture yields 2 samples per kind, below MinSamples = 3,
//     and the table stays unpriced; one race on an 8-candidate fixture yields
//     8 and a second would be waste. And a world that dies at rung O-1
//     contributes NOTHING to the kinds behind it, so "two races is enough"
//     prices the guard and leaves the suite unpriced — and the fallback fires
//     anyway, on the kind that matters.
//
//  2. WARMING HAPPENS ONCE PER (INSTANCE, POLICY DIGEST, BINARY) INTO A
//     TEMPLATE, and every arm and every replicate inherits it BY COPY
//     (decision 2). The cost table is a property of the host and the
//     repository, not of the arm. Copying is what makes the table
//     byte-identical across every arm and every replicate of one protocol
//     run — the property the comparison needs — and it is asserted rather
//     than assumed.
//
//  3. THE ARM'S POOL NEVER SEES A WARM-UP MILLISECOND (decision 4). A
//     warm-up is a DIFFERENT INTENT and a DIFFERENT RACE, and the budget is
//     an intent field with a per-race pool, so its spend is structurally
//     outside every arm's pool: there is no accounting to get right, only a
//     report to get right, and LedgerView's race window is what gets the
//     report right. The warm intent carries `--budget-oracle-ms 0` and the
//     warm-up's own spend, race count and resulting table are RECORDED,
//     because an uncharged cost that is also unreported is a cost nobody can
//     audit.
//
// THE WARM SET IS THE INSTANCE'S PUBLIC CANDIDATE SET — the same patch bytes
// the arms race, already in the workspace, already handed over by
// HandoffPatches. No new bytes cross the hidden/public boundary, so D1–D5 and
// the non-consultation witness are unchanged, and on a family-B instance gold
// is not in the public candidate set at all, so warming can never plant gold
// in a workspace where gold was withheld.
//
// ZERO AGENT SPEND: a warm-up is `--agent script` over patch bytes we already
// have, under the same poisoned-stub PATH every other race in this package
// runs under.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// WarmupAuto is `--warmup auto`: race until decision 1's predicate holds or
// the cap is reached, then say which it was.
const WarmupAuto = "auto"

// WarmupCapDefault is where `auto` stops. It is a CAP and not a target: the
// predicate is what decides, and the cap only decides what happens when the
// predicate cannot be reached — which is `warm_incomplete`, named kinds, and
// a coverage of 0 BY CONSTRUCTION AND REPORTED AS SUCH, never a workspace
// silently raced as though it were warm.
const WarmupCapDefault = 3

// WarmReport is what warming cost and what it produced. It goes into the run
// manifest verbatim.
type WarmReport struct {
	// Requested is the flag as it was passed: "auto", "0", or a count.
	Requested string `json:"requested"`
	Races     int    `json:"races"`
	SpendMS   int64  `json:"spend_ms"`
	WallMS    int64  `json:"wall_ms"`
	// TableDigest is the fitted cost model the template ended up holding. It
	// is the value the harness asserts EQUAL ACROSS ARMS: if it is not, every
	// per-arm difference is confounded with pricing (falsifier V-6).
	TableDigest string `json:"table_digest"`
	// KindsFitted and KindsUnfitted are decision 1's predicate, itemized. A
	// nonempty KindsUnfitted with Incomplete set is the honest failure: the
	// cap was reached and these kinds are still priced `declared-rank`.
	KindsFitted   []string `json:"kinds_fitted"`
	KindsUnfitted []string `json:"kinds_unfitted"`
	// Incomplete is `warm_incomplete`: the cap was reached and the predicate
	// still fails. The instance's coverage is then 0 BY CONSTRUCTION and is
	// reported as such rather than raced as though it were warm.
	Incomplete bool `json:"warm_incomplete"`
	// Refused is set when warming could not run at all — no python, a
	// pre-flight abort, a missing binary. It is a NAMED degradation: the cell
	// falls back to the cold instrument and says so, rather than dying.
	Refused string `json:"refused"`
	// Template is the seeded workspace every arm copies. Empty means no
	// template: the arms build their own workspaces, cold, as before.
	Template string `json:"template"`
	// Key is the cache key this template was built under.
	Key string `json:"key"`
}

// Warm reports whether decision 1's predicate holds.
func (w WarmReport) Warm() bool {
	return w.Template != "" && !w.Incomplete && w.Refused == "" && len(w.KindsUnfitted) == 0
}

// Regime renders decision 11's caption for a cell warmed this way.
func (w WarmReport) Regime() string {
	if w.Warm() {
		return fmt.Sprintf("WARM-COST-TABLE(n=%d)", w.Races)
	}
	return "COLD-COST-TABLE"
}

// Lines renders the warm report for a human, always — including when nothing
// was warmed, because "warming did not run" is exactly the fact a reader
// needs in order to know what the coverage number below it means.
func (w WarmReport) Lines() []string {
	if w.Refused != "" {
		return []string{fmt.Sprintf("warm-up: REFUSED (%s) — the instrument is COLD and every "+
			"allocation figure below is a figure about the exhaustive ladder", w.Refused)}
	}
	if w.Requested == "0" || (w.Races == 0 && w.Template == "") {
		return []string{"warm-up: none (--warmup 0) — COLD-COST-TABLE, every kind priced declared-rank"}
	}
	out := []string{fmt.Sprintf(
		"warm-up: %d race(s), %d ms, charged to NO ARM (a separate intent at --budget-oracle-ms 0); table %s",
		w.Races, w.SpendMS, short(w.TableDigest))}
	if len(w.KindsUnfitted) > 0 {
		out = append(out, fmt.Sprintf(
			"  warm_incomplete: %s still carry no local measurement after the cap of %d race(s); "+
				"this instance's coverage is 0 BY CONSTRUCTION",
			strings.Join(w.KindsUnfitted, ", "), w.Races))
	} else {
		out = append(out, "  every kind the pinned policy can buy carries a local fit")
	}
	return out
}

func short(dig string) string {
	if len(dig) <= 12 {
		return dig
	}
	return dig[:12] + "…"
}

// ParseWarmup reads the `--warmup {auto|N|0}` flag. It refuses anything else
// by name rather than defaulting, because a mistyped warm-up that silently
// meant "cold" is exactly the invisible vacuum this block exists to remove.
func ParseWarmup(s string) (auto bool, n int, err error) {
	s = strings.TrimSpace(s)
	if s == "" || s == WarmupAuto {
		return true, WarmupCapDefault, nil
	}
	v, cerr := strconv.Atoi(s)
	if cerr != nil || v < 0 {
		return false, 0, fmt.Errorf("eval: --warmup %q: want `auto`, `0` or a non-negative count", s)
	}
	return false, v, nil
}

// WarmSpec is one template build.
type WarmSpec struct {
	// MVO is the racing binary. Warming uses the SAME binary the arms use,
	// because the racing binary is what took the measurements (M2b.1 F15).
	MVO string
	// Dir is where the template workspace is built.
	Dir string
	// RepoSrc, Patches and PolicyFile are exactly the arms' inputs: the warm
	// set is the instance's PUBLIC CANDIDATE SET and nothing else.
	RepoSrc    string
	Patches    map[string][]byte
	PolicyFile string
	Parallel   int
	// Auto and Races are ParseWarmup's answer.
	Auto  bool
	Races int
	// Env is the child environment; nil means the ambient one, scrubbed.
	Env []string
	// EvalHome is checked exactly as Race checks it: a template is a
	// workspace, and a workspace must never be built inside the eval home.
	EvalHome string
	// Key is the cache key (instance, policy digest, binary digest). It is
	// recorded so that a reader can see WHY two instances did not share a
	// template.
	Key string
}

// Warm builds the template and races into it until decision 1's predicate
// holds, or refuses by name.
//
// It never returns an error for a race that failed: warming is an
// AMORTIZATION, not a precondition, and a host that cannot warm must still be
// able to run the cold instrument and say that is what it ran. Every
// degradation lands in WarmReport.Refused / .Incomplete, which the harness
// prints and the coverage block reads.
func Warm(spec WarmSpec) WarmReport {
	rep := WarmReport{Key: spec.Key, Requested: WarmupAuto}
	if !spec.Auto {
		rep.Requested = strconv.Itoa(spec.Races)
	}
	if !spec.Auto && spec.Races == 0 {
		// The COLD instrument, kept on purpose: accept step m2d1-9a has to be
		// able to reproduce the vacuum deliberately.
		return rep
	}
	start := time.Now()
	if spec.MVO == "" {
		rep.Refused = "no mvo binary"
		return rep
	}
	if err := outsideEvalHome(spec.Dir, spec.EvalHome); err != nil {
		rep.Refused = err.Error()
		return rep
	}
	ws := filepath.Join(spec.Dir, "ws")
	if err := copyTree(spec.RepoSrc, ws); err != nil {
		rep.Refused = err.Error()
		return rep
	}
	if err := gitSeed(ws); err != nil {
		rep.Refused = err.Error()
		return rep
	}
	shimDir := filepath.Join(spec.Dir, "no-agents")
	firedDir := filepath.Join(spec.Dir, "stubs-fired")
	if err := writeAgentStubs(shimDir, firedDir); err != nil {
		rep.Refused = err.Error()
		return rep
	}
	env := ScrubEnv(spec.Env, shimDir)

	run := func(args ...string) (string, error) {
		cmd := exec.Command(spec.MVO, args...)
		cmd.Dir = ws
		cmd.Env = env
		b, err := cmd.CombinedOutput()
		if err != nil {
			return string(b), fmt.Errorf("%s %s: %w: %s", filepath.Base(spec.MVO),
				strings.Join(args, " "), err, strings.TrimSpace(string(b)))
		}
		return string(b), nil
	}
	if _, err := run("init", "--dir", ws); err != nil {
		rep.Refused = err.Error()
		return rep
	}
	policyName := "default"
	if spec.PolicyFile != "" {
		name := strings.TrimSuffix(filepath.Base(spec.PolicyFile), ".json")
		b, err := os.ReadFile(spec.PolicyFile)
		if err != nil {
			rep.Refused = err.Error()
			return rep
		}
		polDir := filepath.Join(ws, ".multiverso", "policies")
		if err := os.MkdirAll(polDir, 0o755); err != nil {
			rep.Refused = err.Error()
			return rep
		}
		if err := os.WriteFile(filepath.Join(polDir, name+".json"), b, 0o644); err != nil {
			rep.Refused = err.Error()
			return rep
		}
		if _, err := run("policy", "use", name, "--dir", ws); err != nil {
			rep.Refused = err.Error()
			return rep
		}
		policyName = name
	}
	// The warm set is the PUBLIC PROJECTION's candidate set, written exactly
	// where the arms' handoff goes.
	handoffDir := filepath.Join(ws, ".mvo-eval-handoff")
	if err := os.MkdirAll(handoffDir, 0o755); err != nil {
		rep.Refused = err.Error()
		return rep
	}
	names := make([]string, 0, len(spec.Patches))
	for n := range spec.Patches {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(handoffDir, n), spec.Patches[n], 0o644); err != nil {
			rep.Refused = err.Error()
			return rep
		}
	}
	if len(names) == 0 {
		rep.Refused = "the instance has no public candidate set to warm on"
		return rep
	}

	parallel := spec.Parallel
	if parallel <= 0 {
		parallel = 1
	}
	cap := spec.Races
	if spec.Auto {
		cap = WarmupCapDefault
		if spec.Races > 0 {
			cap = spec.Races
		}
	}
	for rep.Races < cap {
		// THE LOOP IS `race → re-read → stop`, because the predicate is read
		// from a READ-ONLY VERB over the workspace's own ledger and costs no
		// race. Under `--warmup N` the count is pinned and the predicate is
		// still read, so the report says what the count actually bought.
		if spec.Auto {
			fitted, unfitted, err := warmPredicate(spec.MVO, ws, policyName, env)
			if err == nil && len(unfitted) == 0 && len(fitted) > 0 {
				rep.KindsFitted, rep.KindsUnfitted = fitted, nil
				break
			}
		}
		out, err := run("intent", "new", "--dir", ws,
			"--title", "warm-up "+strconv.Itoa(rep.Races+1),
			"--desc", "cost-table warm-up (charged to no arm)",
			// DECISION 4: the warm intent is UNBUDGETED. It is a different
			// intent and a different race, so its spend is structurally
			// outside every arm's pool.
			"--budget-oracle-ms", "0",
			"--budget-candidates", strconv.Itoa(len(names)))
		if err != nil {
			rep.Refused = err.Error()
			break
		}
		intent := lastLine(out)
		if intent == "" {
			rep.Refused = "mvo intent new printed no digest"
			break
		}
		// `--schedule=fixed` buys the EXHAUSTIVE ladder, which is what
		// maximizes samples per race and what makes the warm-up's own
		// allocation irrelevant to what it is measuring.
		if _, err := run("race", intent, "--dir", ws, "--agent", "script",
			"--patches", handoffDir, "--parallel", strconv.Itoa(parallel),
			"--schedule=fixed"); err != nil {
			rep.Refused = err.Error()
			break
		}
		rep.Races++
		if fired := stubsFired(firedDir); len(fired) > 0 {
			rep.Refused = "a poisoned agent stub fired during warm-up: " + strings.Join(fired, " ")
			break
		}
	}
	fitted, unfitted, err := warmPredicate(spec.MVO, ws, policyName, env)
	if err == nil {
		rep.KindsFitted, rep.KindsUnfitted = fitted, unfitted
		rep.TableDigest = warmTableDigest(spec.MVO, ws, policyName, env)
	} else if rep.Refused == "" {
		rep.Refused = err.Error()
	}
	if rep.Refused == "" && len(rep.KindsUnfitted) > 0 {
		rep.Incomplete = true
	}
	// The warm-up's own spend, taken from the receipts it wrote, so the
	// uncharged cost is at least an audited one.
	if v, verr := ReadLedger(ws); verr == nil {
		rep.SpendMS = v.SpendMS + v.OutsideSpendMS
	}
	rep.WallMS = time.Since(start).Milliseconds()
	if rep.Refused == "" {
		rep.Template = ws
	}
	return rep
}

// warmPredicate is decision 1's predicate, read from `mvo oracles --json
// --policy <name>`: a read-only verb over the workspace's own ledger that
// costs no race.
//
//	warm(workspace, policy) ⟺ ∀ kind k with declared_by_policy(k) ≠ ∅ :
//	                             measurement(k) ≠ null
//
// It asks the PRODUCT what it can price rather than re-deriving a fit here. A
// second copy of the fit is a second cost model, and the one the arms
// allocate against is the one that matters.
func warmPredicate(mvo, ws, policyName string, env []string) (fitted, unfitted []string, err error) {
	rows, err := oracleMenu(mvo, ws, policyName, env)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range rows {
		if len(r.Declared) == 0 {
			continue
		}
		if r.Measurement == nil {
			unfitted = append(unfitted, r.Kind)
			continue
		}
		fitted = append(fitted, r.Kind)
	}
	sort.Strings(fitted)
	sort.Strings(unfitted)
	return fitted, unfitted, nil
}

// menuKind is the subset of `mvo oracles --json` this package reads. It is
// deliberately partial: a later binary may report more, and this must keep
// working rather than refusing the whole document (M1f decision 3).
type menuKind struct {
	Kind        string          `json:"kind"`
	Declared    []string        `json:"declared_by_policy"`
	Measurement json.RawMessage `json:"measurement"`
	SampleN     int             `json:"measurement_n"`
	Note        string          `json:"measurement_note"`
}

func oracleMenu(mvo, ws, policyName string, env []string) ([]menuKind, error) {
	cmd := exec.Command(mvo, "oracles", "--json", "--dir", ws, "--policy", policyName)
	cmd.Dir = ws
	cmd.Env = env
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("eval: mvo oracles --json --policy %s: %w", policyName, err)
	}
	var body struct {
		Kinds []menuKind `json:"kinds"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("eval: decode the oracle menu: %w", err)
	}
	// A `measurement` of JSON null is ABSENT, not present-and-empty.
	for i := range body.Kinds {
		if strings.TrimSpace(string(body.Kinds[i].Measurement)) == "null" {
			body.Kinds[i].Measurement = nil
		}
	}
	return body.Kinds, nil
}

// warmTableDigest digests the fitted cost model the template holds. It is the
// number the harness asserts EQUAL ACROSS ARMS: decision 2's template exists
// to make it identical by construction, and a comparison whose arms priced
// differently has every difference confounded with pricing (V-6).
func warmTableDigest(mvo, ws, policyName string, env []string) string {
	rows, err := oracleMenu(mvo, ws, policyName, env)
	if err != nil {
		return ""
	}
	var lines []string
	for _, r := range rows {
		if len(r.Declared) == 0 || r.Measurement == nil {
			continue
		}
		lines = append(lines, r.Kind+"="+string(r.Measurement))
	}
	sort.Strings(lines)
	return CASKeyBytes([]byte(strings.Join(lines, "\n")))
}

// ---------------------------------------------------------------------------
// The template cache (decision 2)
// ---------------------------------------------------------------------------

// TemplateCache holds ONE template per (instance, policy digest, binary
// digest) for the life of a run. It is what turns warming from a 9× cost into
// a < 4 % one — a full protocol cell races 3 reference replicates + 2 arms ×
// R = 3 replicates = 9 races per instance, and one warm-up serves all of them.
//
// REUSE ACROSS POLICIES IS REFUSED RATHER THAN REASONED ABOUT. `costSamples()`
// admits a receipt only when the pinned policy declares its Oracle.Config, and
// the fit is keyed on the SEAL (`plugin_autoload`), which is a policy field
// that M2a amendment 27 measured as a 4.4× lever on fixed cost. The price of
// the refusal is one extra warm-up per policy.
type TemplateCache struct {
	byKey map[string]WarmReport
}

// NewTemplateCache builds an empty cache.
func NewTemplateCache() *TemplateCache { return &TemplateCache{byKey: map[string]WarmReport{}} }

// TemplateKey is the cache key, and every member of it is load-bearing.
func TemplateKey(instance, policyDigest, binaryDigest string) string {
	return instance + "\x00" + policyDigest + "\x00" + binaryDigest
}

// Get returns a cached template for this key, if one was built.
func (c *TemplateCache) Get(key string) (WarmReport, bool) {
	if c == nil || c.byKey == nil {
		return WarmReport{}, false
	}
	r, ok := c.byKey[key]
	return r, ok
}

// Put records a built template.
func (c *TemplateCache) Put(key string, r WarmReport) {
	if c == nil {
		return
	}
	if c.byKey == nil {
		c.byKey = map[string]WarmReport{}
	}
	c.byKey[key] = r
}

// Warm builds or reuses the template for one key.
func (c *TemplateCache) Warm(key string, spec WarmSpec) WarmReport {
	if r, ok := c.Get(key); ok {
		return r
	}
	spec.Key = key
	r := Warm(spec)
	c.Put(key, r)
	return r
}
