package eval

// MECHANICAL CANDIDATE DERIVATION (§3, decision 7).
//
// THE GENERATOR PROPOSES, THE HIDDEN ORACLE LABELS. No source carries an
// assumed label — not even the ones built to be wrong. A "revert one hunk"
// mutant can be semantically neutral (the equivalent-mutant problem, the
// oldest one in mutation testing); a "weaken the condition" mutant can weaken
// a condition the tests never reach. Stamping such a patch `incorrect`
// because we MEANT it to be wrong would put this file's intentions into the
// numerator of FAR. So every operator records an `Expected` field that
// nothing in the metric path reads, and the report prints an
// `expectation-violated` census over it — information about the oracle's
// strength, not an error.
//
// WHAT THIS FILE CANNOT DO, and it bounds the whole block. These are
// perturbations of a KNOWN-CORRECT PATCH: they touch the right file, in the
// right function, with the right shape — the part real agents most often get
// wrong. They are therefore systematically easier for a test suite to catch,
// so any FAR measured over them is a LOWER BOUND that flatters every arm and
// every oracle; they share a common ancestor, so M2a's cross-candidate
// differential sees artificially high agreement; and their diversity is a
// function of the operator list below rather than of a model's failure
// distribution. Any TCAR/FAR figure from S1+S2 is a property of this file as
// much as of the scheduler, which is why decision 8 puts SYNTHETIC-CANDIDATES
// on every table.
//
// PURITY. Every function here is a pure function of its arguments: patch
// bytes in, patch bytes out, no filesystem, no clock, no randomness that is
// not the caller's seed. That is what makes a derived population
// reproducible from (generator id, seed, params) alone, which AG-3 requires
// of anything random.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Derivation operator ids. The list IS the diversity of the population, and
// it is short: that is the limitation, stated as data rather than as prose.
const (
	OpRevertHunk        = "revert-hunk"
	OpTruncateLines     = "truncate-lines"
	OpFlipComparison    = "flip-comparison"
	OpOffByOne          = "off-by-one"
	OpSiblingTarget     = "sibling-target"
	OpWeakenCondition   = "weaken-condition"
	OpTransplantForeign = "transplant-foreign"
)

// Operators lists every operator in a fixed order.
func Operators() []string {
	return []string{
		OpRevertHunk,
		OpTruncateLines,
		OpFlipComparison,
		OpOffByOne,
		OpSiblingTarget,
		OpWeakenCondition,
		OpTransplantForeign,
	}
}

// Expectation values. `unknown` is a real answer: transplanting a foreign
// patch may not even apply, and an operator that cannot say is not allowed
// to guess.
const (
	ExpectIncorrect = "incorrect"
	ExpectCorrect   = "correct"
	ExpectUnknown   = "unknown"
)

// DeriveInput is everything an operator may read. It is explicit so that
// "pure" is checkable by inspection: nothing here comes from the ambient
// process.
type DeriveInput struct {
	// Gold is the instance's own patch with test hunks already stripped.
	Gold []byte
	// Base maps repository-relative path -> base file contents, for the
	// operators that must know what the tree looked like before the fix
	// (sibling-target needs to find a sibling function).
	Base map[string]string
	// Foreign is another instance's gold patch, for the transplant
	// operator. Empty disables it, with a reason.
	Foreign []byte
	// ForeignID names where Foreign came from, recorded in Params.
	ForeignID string
	// Seed makes every choice reproducible.
	Seed int64
}

// Derived is one proposal. Patch is empty exactly when Applied is false, and
// Reason is nonempty exactly then: an operator that declines says why.
type Derived struct {
	Operator string `json:"operator"`
	Seed     int64  `json:"seed"`
	Params   string `json:"params"`
	Expected string `json:"expected"`
	Applied  bool   `json:"applied"`
	Reason   string `json:"reason"`
	Patch    []byte `json:"-"`
}

// ID is the candidate id a derivation gets. It is a function of the operator,
// the seed and the params, so two runs of the same derivation collide on
// purpose.
func (d Derived) ID() string {
	if d.Params == "" {
		return fmt.Sprintf("%s@%d", d.Operator, d.Seed)
	}
	return fmt.Sprintf("%s@%d[%s]", d.Operator, d.Seed, d.Params)
}

// DeriveAll runs every operator in fixed order and returns one Derived per
// operator, including the ones that declined. The caller decides what to
// race; nothing is silently dropped, because "the operator list produced 3
// of 7" is itself a fact about the population.
func DeriveAll(in DeriveInput) []Derived {
	out := make([]Derived, 0, len(Operators()))
	for _, op := range Operators() {
		out = append(out, Derive(op, in))
	}
	return out
}

// Derive runs one operator. It is total: an operator that cannot apply
// returns Applied=false with a reason, never an error and never a patch that
// will not apply for a reason it knew about.
func Derive(op string, in DeriveInput) Derived {
	d := Derived{Operator: op, Seed: in.Seed, Expected: ExpectIncorrect}
	p, err := ParsePatch(in.Gold)
	if err != nil {
		d.Reason = fmt.Sprintf("gold patch does not parse as a unified diff: %v", err)
		d.Expected = ExpectUnknown
		return d
	}
	switch op {
	case OpRevertHunk:
		return deriveRevertHunk(d, p, in)
	case OpTruncateLines:
		return deriveTruncateLines(d, p, in)
	case OpFlipComparison:
		return deriveSubstitute(d, p, flipComparison, "no comparison operator in any changed line")
	case OpOffByOne:
		return deriveSubstitute(d, p, offByOne, "no boundary expression (len(...), an integer literal or a slice bound) in any changed line")
	case OpSiblingTarget:
		return deriveSiblingTarget(d, p, in)
	case OpWeakenCondition:
		return deriveWeaken(d, p)
	case OpTransplantForeign:
		return deriveTransplant(d, in)
	default:
		d.Reason = fmt.Sprintf("unknown operator %q", op)
		d.Expected = ExpectUnknown
		return d
	}
}

// ---------------------------------------------------------------------------
// The operators
// ---------------------------------------------------------------------------

// deriveRevertHunk drops one hunk: the classic incomplete fix. It needs a
// patch with at least two hunks, and on a one-hunk patch it declines rather
// than producing the empty patch (which is S5, a different source with a
// different meaning).
func deriveRevertHunk(d Derived, p Patch, in DeriveInput) Derived {
	total := 0
	for _, f := range p.Files {
		total += len(f.Hunks)
	}
	if total < 2 {
		d.Reason = fmt.Sprintf("gold has %d hunk(s): reverting one would leave the empty patch, which is S5-null and not S2", total)
		return d
	}
	idx := int(mix(in.Seed, "revert") % uint64(total))
	d.Params = fmt.Sprintf("hunk=%d/%d", idx, total)
	seen := 0
	out := Patch{}
	for _, f := range p.Files {
		nf := f
		nf.Hunks = nil
		for _, h := range f.Hunks {
			if seen != idx {
				nf.Hunks = append(nf.Hunks, h)
			}
			seen++
		}
		if len(nf.Hunks) > 0 {
			out.Files = append(out.Files, nf)
		}
	}
	if len(out.Files) == 0 {
		d.Reason = "reverting the chosen hunk empties the patch"
		return d
	}
	d.Applied = true
	d.Patch = out.Render()
	return d
}

// deriveTruncateLines keeps the first k added lines and drops the rest,
// leaving the removals in place: a fix that stopped halfway, which is the
// commonest partial-application shape.
func deriveTruncateLines(d Derived, p Patch, in DeriveInput) Derived {
	added := 0
	for _, f := range p.Files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if strings.HasPrefix(l, "+") {
					added++
				}
			}
		}
	}
	if added == 0 {
		d.Reason = "gold adds no lines: there is nothing to truncate"
		return d
	}
	keep := int(mix(in.Seed, "truncate") % uint64(added))
	d.Params = fmt.Sprintf("keep=%d/%d", keep, added)
	kept := 0
	out := Patch{}
	for _, f := range p.Files {
		nf := f
		nf.Hunks = nil
		for _, h := range f.Hunks {
			nh := h
			nh.Lines = nil
			for _, l := range h.Lines {
				if strings.HasPrefix(l, "+") {
					if kept >= keep {
						kept++
						continue
					}
					kept++
				}
				nh.Lines = append(nh.Lines, l)
			}
			if hunkChanges(nh) {
				nf.Hunks = append(nf.Hunks, nh)
			}
		}
		if len(nf.Hunks) > 0 {
			out.Files = append(out.Files, nf)
		}
	}
	if len(out.Files) == 0 {
		d.Reason = "truncation empties the patch"
		return d
	}
	d.Applied = true
	d.Patch = out.Render()
	return d
}

var comparisonRe = regexp.MustCompile(`(<=|>=|==|!=|<|>)`)

// flipComparison swaps the sense of the first comparison in a line.
func flipComparison(line string) (string, bool) {
	loc := comparisonRe.FindStringIndex(line)
	if loc == nil {
		return line, false
	}
	swap := map[string]string{"<": ">", ">": "<", "<=": ">=", ">=": "<=", "==": "!=", "!=": "=="}
	op := line[loc[0]:loc[1]]
	return line[:loc[0]] + swap[op] + line[loc[1]:], true
}

var lenRe = regexp.MustCompile(`len\(([A-Za-z_][A-Za-z0-9_]*)\)`)
var intRe = regexp.MustCompile(`(^|[^A-Za-z0-9_.])([0-9]+)([^0-9]|$)`)

// offByOne perturbs a boundary: len(x) becomes len(x) + 1, otherwise the
// first integer literal is incremented. This is the operator that reproduces
// the toyrepo bug's own shape, which is the point — the failure modes S2 can
// represent are the LOCAL ones.
func offByOne(line string) (string, bool) {
	if m := lenRe.FindStringIndex(line); m != nil {
		return line[:m[0]] + "(" + line[m[0]:m[1]] + " + 1)" + line[m[1]:], true
	}
	if m := intRe.FindStringSubmatchIndex(line); m != nil {
		n, err := strconv.Atoi(line[m[4]:m[5]])
		if err == nil {
			return line[:m[4]] + strconv.Itoa(n+1) + line[m[5]:], true
		}
	}
	return line, false
}

// deriveSubstitute rewrites the FIRST added line the substitution applies to.
// Only added lines: rewriting a context line would produce a patch that does
// not apply, and an operator that knowingly emits a non-applying patch is a
// bug dressed as a mutant.
func deriveSubstitute(d Derived, p Patch, sub func(string) (string, bool), noneReason string) Derived {
	out := Patch{}
	done := false
	for _, f := range p.Files {
		nf := f
		nf.Hunks = nil
		for _, h := range f.Hunks {
			nh := h
			nh.Lines = append([]string(nil), h.Lines...)
			for i, l := range nh.Lines {
				if done || !strings.HasPrefix(l, "+") {
					continue
				}
				if rewritten, ok := sub(l[1:]); ok {
					nh.Lines[i] = "+" + rewritten
					d.Params = fmt.Sprintf("line=%q", strings.TrimSpace(rewritten))
					done = true
				}
			}
			nf.Hunks = append(nf.Hunks, nh)
		}
		out.Files = append(out.Files, nf)
	}
	if !done {
		d.Reason = noneReason
		return d
	}
	d.Applied = true
	d.Patch = out.Render()
	return d
}

var defRe = regexp.MustCompile(`^def ([A-Za-z_][A-Za-z0-9_]*)\(`)

// deriveSiblingTarget applies gold's edit to the WRONG function: the
// wrong-target failure mode. It needs the base file, because "which functions
// are siblings" is not knowable from a diff.
func deriveSiblingTarget(d Derived, p Patch, in DeriveInput) Derived {
	if len(in.Base) == 0 {
		d.Reason = "no base tree supplied: sibling-target cannot know which functions are siblings"
		return d
	}
	// The fix line is the last added line of the first hunk that adds
	// anything, and the target function is the one that hunk sits in.
	var fixLine, path string
	for _, f := range p.Files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if strings.HasPrefix(l, "+") {
					fixLine = l[1:]
					path = f.NewPath
				}
			}
			if fixLine != "" {
				break
			}
		}
		if fixLine != "" {
			break
		}
	}
	if fixLine == "" || path == "" {
		d.Reason = "gold adds no line inside a known file"
		return d
	}
	base, ok := in.Base[path]
	if !ok {
		d.Reason = fmt.Sprintf("base tree has no %s", path)
		return d
	}
	lines := strings.Split(base, "\n")
	// Which function does gold touch? The nearest preceding `def` to the
	// first removed line's text.
	touched := ""
	removed := ""
	for _, f := range p.Files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if strings.HasPrefix(l, "-") && removed == "" {
					removed = strings.TrimSpace(l[1:])
				}
			}
		}
	}
	cur := ""
	for _, l := range lines {
		if m := defRe.FindStringSubmatch(l); m != nil {
			cur = m[1]
		}
		if removed != "" && strings.TrimSpace(l) == removed {
			touched = cur
			break
		}
	}
	// Candidate siblings: every other top-level def with a return line.
	type sibling struct {
		name string
		idx  int
		line string
	}
	var sibs []sibling
	cur = ""
	curIdx := -1
	for i, l := range lines {
		if m := defRe.FindStringSubmatch(l); m != nil {
			cur, curIdx = m[1], i
			_ = curIdx
			continue
		}
		if cur == "" || cur == touched {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(l), "return ") {
			sibs = append(sibs, sibling{name: cur, idx: i, line: l})
			cur = "" // one return per sibling: the first is enough
		}
	}
	if len(sibs) == 0 {
		d.Reason = fmt.Sprintf("no sibling function with a return statement in %s (touched %q)", path, touched)
		return d
	}
	sort.Slice(sibs, func(i, j int) bool { return sibs[i].name < sibs[j].name })
	pick := sibs[int(mix(in.Seed, "sibling")%uint64(len(sibs)))]
	indent := leadingSpace(pick.line)
	newLine := indent + strings.TrimSpace(fixLine)
	if newLine == pick.line {
		d.Reason = fmt.Sprintf("gold's line is already what %s returns", pick.name)
		return d
	}
	d.Params = fmt.Sprintf("path=%s,sibling=%s", path, pick.name)
	h := Hunk{
		OldStart: pick.idx + 1,
		NewStart: pick.idx + 1,
		Section:  " def " + pick.name + "()",
		Lines:    []string{"-" + pick.line, "+" + newLine},
	}
	// One context line either side, when the file has them: git apply is
	// happier with context and a zero-context hunk is a needless risk.
	if pick.idx > 0 {
		h.OldStart--
		h.NewStart--
		h.Lines = append([]string{" " + lines[pick.idx-1]}, h.Lines...)
	}
	if pick.idx+1 < len(lines) && lines[pick.idx+1] != "" {
		h.Lines = append(h.Lines, " "+lines[pick.idx+1])
	}
	out := Patch{Files: []FilePatch{{
		OldPath: path,
		NewPath: path,
		Header:  []string{fmt.Sprintf("diff --git a/%s b/%s", path, path)},
		Hunks:   []Hunk{h},
	}}}
	d.Applied = true
	d.Patch = out.Render()
	return d
}

var boolRe = regexp.MustCompile(`\b(and|or|not)\b`)

// deriveWeaken weakens a guard. It may convert a CONTEXT line into a
// removal/addition pair, which is legitimate patch surgery and is the only
// way to reach the guard gold left alone — and it is exactly the mutant that
// keeps the fix and breaks a pass_to_pass test, so it exercises the gates
// rather than the suite.
func deriveWeaken(d Derived, p Patch) Derived {
	out := Patch{}
	done := false
	for _, f := range p.Files {
		nf := f
		nf.Hunks = nil
		for _, h := range f.Hunks {
			nh := h
			nh.Lines = nil
			for _, l := range h.Lines {
				if done || len(l) == 0 {
					nh.Lines = append(nh.Lines, l)
					continue
				}
				body := l[1:]
				trimmed := strings.TrimSpace(body)
				if !strings.HasPrefix(trimmed, "if ") || !strings.HasSuffix(trimmed, ":") {
					nh.Lines = append(nh.Lines, l)
					continue
				}
				weak := ""
				switch {
				case boolRe.MatchString(body):
					weak = boolRe.ReplaceAllStringFunc(body, func(s string) string {
						switch s {
						case "and":
							return "or"
						case "or":
							return "and"
						default:
							return "not not"
						}
					})
				default:
					if rewritten, ok := flipComparison(body); ok {
						weak = rewritten
					}
				}
				if weak == "" || weak == body {
					nh.Lines = append(nh.Lines, l)
					continue
				}
				switch l[0] {
				case '+':
					nh.Lines = append(nh.Lines, "+"+weak)
				case ' ':
					nh.Lines = append(nh.Lines, "-"+body, "+"+weak)
				default: // a removal: leave it alone
					nh.Lines = append(nh.Lines, l)
					continue
				}
				d.Params = fmt.Sprintf("guard=%q", trimmed)
				done = true
			}
			nf.Hunks = append(nf.Hunks, nh)
		}
		out.Files = append(out.Files, nf)
	}
	if !done {
		d.Reason = "no `if ...:` guard in any changed or context line"
		return d
	}
	d.Applied = true
	d.Patch = out.Render()
	return d
}

// deriveTransplant hands over another instance's gold patch verbatim. Its
// expectation is `unknown`, not `incorrect`: a foreign patch may not apply at
// all, and on a repository with a coincidentally similar file it might even
// be correct. Guessing here is exactly the assumed-label bug.
func deriveTransplant(d Derived, in DeriveInput) Derived {
	d.Expected = ExpectUnknown
	if len(in.Foreign) == 0 {
		d.Reason = "no foreign gold patch supplied (a one-instance corpus cannot transplant)"
		return d
	}
	if _, err := ParsePatch(in.Foreign); err != nil {
		d.Reason = fmt.Sprintf("foreign patch does not parse: %v", err)
		return d
	}
	d.Params = "from=" + in.ForeignID
	d.Applied = true
	d.Patch = append([]byte(nil), in.Foreign...)
	return d
}

// ---------------------------------------------------------------------------
// A unified-diff parser and renderer
// ---------------------------------------------------------------------------

// Hunk is one @@ block. Counts are NOT stored: they are recomputed at render
// time from the lines, so an operator that edits lines cannot forget to fix
// the header — the commonest way hand-built patch surgery produces a
// "corrupt patch" that looks like a fixture problem.
type Hunk struct {
	OldStart int
	NewStart int
	Section  string // the text after the closing @@, including its leading space
	Lines    []string
}

// FilePatch is one file's worth of diff.
type FilePatch struct {
	OldPath string
	NewPath string
	// Header holds every pre-@@ line EXCEPT `index` and the ---/+++ pair:
	// `index` carries blob hashes that a derived patch invalidates, and git
	// apply does not need it. Dropping it is what keeps derived patches
	// appliable with --index.
	Header []string
	Hunks  []Hunk
}

// Patch is a parsed unified diff.
type Patch struct {
	Files []FilePatch
}

var hunkRe = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(.*)$`)

// ParsePatch parses a unified diff. It is strict about the things that
// silently corrupt a patch and permissive about the rest.
func ParsePatch(b []byte) (Patch, error) {
	var p Patch
	var cur *FilePatch
	var hunk *Hunk
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return Patch{}, fmt.Errorf("empty patch")
	}
	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			p.Files = append(p.Files, *cur)
		}
		cur = nil
	}
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			flushFile()
			cur = &FilePatch{Header: []string{l}}
		case strings.HasPrefix(l, "index "):
			// dropped on purpose (see FilePatch.Header)
		case strings.HasPrefix(l, "--- "):
			if cur == nil {
				cur = &FilePatch{}
			}
			flushHunk()
			cur.OldPath = stripPathPrefix(strings.TrimPrefix(l, "--- "))
		case strings.HasPrefix(l, "+++ "):
			if cur == nil {
				return Patch{}, fmt.Errorf("+++ line with no file header")
			}
			cur.NewPath = stripPathPrefix(strings.TrimPrefix(l, "+++ "))
		case strings.HasPrefix(l, "@@"):
			if cur == nil {
				return Patch{}, fmt.Errorf("hunk with no file header")
			}
			m := hunkRe.FindStringSubmatch(l)
			if m == nil {
				return Patch{}, fmt.Errorf("malformed hunk header: %q", l)
			}
			flushHunk()
			os_, _ := strconv.Atoi(m[1])
			ns, _ := strconv.Atoi(m[3])
			hunk = &Hunk{OldStart: os_, NewStart: ns, Section: m[5]}
		default:
			if hunk != nil && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "+") ||
				strings.HasPrefix(l, "-") || strings.HasPrefix(l, "\\") || l == "") {
				// An empty line inside a hunk is a context line whose
				// content is empty; git emits it without the leading
				// space in some pipelines, so it is normalized here.
				if l == "" {
					l = " "
				}
				hunk.Lines = append(hunk.Lines, l)
				continue
			}
			if cur != nil && hunk == nil {
				cur.Header = append(cur.Header, l)
				continue
			}
			return Patch{}, fmt.Errorf("unexpected line outside any file: %q", l)
		}
	}
	flushFile()
	if len(p.Files) == 0 {
		return Patch{}, fmt.Errorf("no file sections")
	}
	for _, f := range p.Files {
		if len(f.Hunks) == 0 && !isModeOnly(f) {
			return Patch{}, fmt.Errorf("file %s has no hunks", f.NewPath)
		}
	}
	return p, nil
}

func isModeOnly(f FilePatch) bool {
	for _, h := range f.Header {
		if strings.HasPrefix(h, "old mode ") || strings.HasPrefix(h, "similarity index ") {
			return true
		}
	}
	return false
}

func stripPathPrefix(s string) string {
	s = strings.TrimSpace(s)
	if s == "/dev/null" {
		return s
	}
	if i := strings.Index(s, "/"); i >= 0 && (strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/")) {
		return s[i+1:]
	}
	return s
}

// Render emits the patch with RECOMPUTED hunk headers. Old starts are kept
// (they address the file being patched, which nothing here changes); new
// starts are recomputed from the running delta, so a patch whose earlier
// hunks grew or shrank still addresses the right lines.
func (p Patch) Render() []byte {
	var sb strings.Builder
	for _, f := range p.Files {
		hdr := f.Header
		if len(hdr) == 0 {
			hdr = []string{fmt.Sprintf("diff --git a/%s b/%s", f.OldPath, f.NewPath)}
		}
		for _, h := range hdr {
			sb.WriteString(h)
			sb.WriteByte('\n')
		}
		if f.OldPath != "" {
			sb.WriteString(pathLine("---", f.OldPath, "a/"))
		}
		if f.NewPath != "" {
			sb.WriteString(pathLine("+++", f.NewPath, "b/"))
		}
		delta := 0
		for _, h := range f.Hunks {
			oldCount, newCount := 0, 0
			for _, l := range h.Lines {
				switch {
				case strings.HasPrefix(l, "\\"):
				case strings.HasPrefix(l, "+"):
					newCount++
				case strings.HasPrefix(l, "-"):
					oldCount++
				default:
					oldCount++
					newCount++
				}
			}
			newStart := h.OldStart + delta
			if oldCount == 0 {
				// A pure addition addresses the line AFTER which it is
				// inserted, which is how git spells an empty old range.
				newStart = h.OldStart + delta
			}
			fmt.Fprintf(&sb, "@@ -%s +%s @@%s\n",
				rangeSpec(h.OldStart, oldCount), rangeSpec(newStart, newCount), h.Section)
			for _, l := range h.Lines {
				sb.WriteString(l)
				sb.WriteByte('\n')
			}
			delta += newCount - oldCount
		}
	}
	return []byte(sb.String())
}

func pathLine(marker, path, prefix string) string {
	if path == "/dev/null" {
		return marker + " /dev/null\n"
	}
	return marker + " " + prefix + path + "\n"
}

func rangeSpec(start, count int) string {
	if count == 0 {
		// git spells an empty range as `<start-1>,0`; keeping start as-is
		// for a zero count would address a line that is not in the range.
		if start > 0 {
			start--
		}
		return fmt.Sprintf("%d,0", start)
	}
	if count == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

func hunkChanges(h Hunk) bool {
	for _, l := range h.Lines {
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			return true
		}
	}
	return false
}

func leadingSpace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

// StripTestHunks removes every hunk that touches a test file, per the
// SWE-bench convention, and RECORDS what it removed (§1.3: the strip is
// recorded). Gold with its test hunks still in would pass any suite by
// construction, which is the oldest way to fake a benchmark result.
func StripTestHunks(b []byte) (stripped []byte, removed []string, err error) {
	p, err := ParsePatch(b)
	if err != nil {
		return nil, nil, err
	}
	out := Patch{}
	for _, f := range p.Files {
		if IsTestPath(f.NewPath) || IsTestPath(f.OldPath) {
			removed = append(removed, f.NewPath)
			continue
		}
		out.Files = append(out.Files, f)
	}
	if len(out.Files) == 0 {
		return nil, removed, fmt.Errorf("eval: stripping test hunks empties the patch (every file it touches is a test)")
	}
	sort.Strings(removed)
	if removed == nil {
		removed = []string{}
	}
	return out.Render(), removed, nil
}

// IsTestPath is the test-file predicate the strip uses. It is deliberately
// broad: a false positive strips a hunk gold needed and shows up as
// gold-fails-control, which is loud; a false negative leaves a test edit in
// gold, which is silent and fatal.
func IsTestPath(path string) bool {
	if path == "" || path == "/dev/null" {
		return false
	}
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
	}
	if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") ||
		base == "conftest.py" {
		return true
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "tests" || seg == "test" || seg == "testing" {
			return true
		}
	}
	return false
}

// mix is a small, explicit, stable hash of (seed, tag). It is used only to
// make an operator's arbitrary choice reproducible; it is not a PRNG and
// nothing statistical depends on its quality. Written out rather than pulled
// from math/rand so the choice is stable across Go releases — math/rand's
// stream is not a compatibility promise, and a corpus that reshuffles when
// the toolchain moves is not reproducible.
func mix(seed int64, tag string) uint64 {
	h := uint64(0xcbf29ce484222325)
	b := make([]byte, 8)
	u := uint64(seed)
	for i := 0; i < 8; i++ {
		b[i] = byte(u >> (8 * i))
	}
	for _, c := range b {
		h ^= uint64(c)
		h *= 0x100000001b3
	}
	for i := 0; i < len(tag); i++ {
		h ^= uint64(tag[i])
		h *= 0x100000001b3
	}
	return h
}
