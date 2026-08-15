package publish

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

func TestFetchRaceHappyNonAdmitted(t *testing.T) {
	f := newFixture(t)
	f.newIntent("fetch happy")
	f.race(true, false)
	f.mustPublish(true)
	rep := f.fetchRace(f.clone())

	if !rep.OK {
		t.Fatalf("verification failed: %+v", rep.Items)
	}
	for _, it := range rep.Items {
		if !it.OK {
			t.Errorf("item %s failed: %s", it.Path, it.Err)
		}
	}
	if rep.IntentDig != f.intentDig || rep.Title != "fetch happy" ||
		rep.SelectDig != f.selDig || rep.DecisionType != "SELECT" {
		t.Errorf("report identity = %+v", rep)
	}
	if rep.Winner != f.sel.Subject[0] || rep.Admitted {
		t.Errorf("winner/admitted = %q/%v", rep.Winner, rep.Admitted)
	}
	if len(rep.Refs) != 3 {
		t.Errorf("refs = %+v, want cand/1 cand/2 evidence", rep.Refs)
	}
	// Table rows: both worlds, ordinal-ordered, gates derived through the
	// closure's own policy — a failing row names the GATE that stopped the
	// ladder, exactly as the local `mvo worlds` does, never a bare "fail"
	// guessed from one receipt's family.
	if len(rep.Worlds) != 2 {
		t.Fatalf("world rows = %+v", rep.Worlds)
	}
	if rep.Worlds[0].Ordinal != 1 || rep.Worlds[0].Gate != "pass" || rep.Worlds[0].Ref != "cand/1" ||
		rep.Worlds[0].Dig != f.worldDigs[0] || rep.Worlds[0].Signed != 1 {
		t.Errorf("row 0 = %+v", rep.Worlds[0])
	}
	if rep.Worlds[1].Ordinal != 2 || rep.Worlds[1].Gate != policy.GateSuitePass ||
		rep.Worlds[1].Outcome != object.OutcomeCompleted {
		t.Errorf("row 1 = %+v", rep.Worlds[1])
	}
}

func TestFetchRaceHappyAdmitted(t *testing.T) {
	f := newFixture(t)
	f.newIntent("fetch admitted")
	f.race(true, false)
	f.admitIntent()
	f.mustPublish(false) // default: winner + evidence only
	rep := f.fetchRace(f.clone())

	if !rep.OK {
		t.Fatalf("verification failed: %+v", rep.Items)
	}
	if !rep.Admitted {
		t.Errorf("admitted closure not reported as admitted")
	}
	if len(rep.Refs) != 2 {
		t.Errorf("refs = %+v, want winner cand + evidence", rep.Refs)
	}
	// The loser world has no candidate ref — losers' code is
	// retention-managed, their evidence always ships.
	var loserRow *WorldRow
	for i := range rep.Worlds {
		if rep.Worlds[i].Dig == f.worldDigs[1] {
			loserRow = &rep.Worlds[i]
		}
	}
	if loserRow == nil || loserRow.Ref != "-" || loserRow.Ordinal != 0 {
		t.Errorf("loser row = %+v", loserRow)
	}
	// The winner carries its suite receipt plus both landing receipts.
	if rep.Worlds[0].Dig != f.sel.Subject[0] || rep.Worlds[0].Signed != 3 {
		t.Errorf("winner row = %+v", rep.Worlds[0])
	}
}

func TestFetchRaceFreshnessStale(t *testing.T) {
	f := newFixture(t)
	f.newIntent("fetch freshness")
	f.race(true)
	f.mustPublish(false)
	// Give origin a main branch past the base, so the consumer clone
	// checks out a head that advanced beyond the intent base.
	gitT(t, f.repo, "commit", "-q", "--allow-empty", "-m", "advance trunk")
	gitT(t, f.repo, "push", "-q", "origin", "main")
	rep := f.fetchRace(f.clone())
	if !rep.OK {
		t.Fatalf("verification failed: %+v", rep.Items)
	}
	if rep.Freshness != DriftStale || !strings.Contains(rep.FreshnessDetail, "advanced past base") {
		t.Errorf("freshness = %s (%s), want STALE advanced", rep.Freshness, rep.FreshnessDetail)
	}
}

// tamperFixture publishes an admitted two-candidate race (rejected
// included) and returns the fixture plus a consumer clone.
func tamperFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	f.newIntent("tamper matrix")
	f.race(true, false)
	f.admitIntent()
	f.mustPublish(true)
	return f, f.clone()
}

func TestFetchRaceTamperMatrix(t *testing.T) {
	t.Run("flipped receipt bundle byte", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		var path string
		f.tamperEvidence(func(files map[string][]byte) {
			path = pathsWithPrefix(files, "receipts/")[0]
			files[path] = flipPayloadChar(t, files[path])
		})
		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("tampered receipt bundle verified")
		}
		it := failedItem(t, rep, path)
		if !strings.Contains(it.Err, "no signature verified") {
			t.Errorf("failure %q does not name the signature", it.Err)
		}
	})

	t.Run("two receipt files swapped", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		var a, b string
		f.tamperEvidence(func(files map[string][]byte) {
			paths := pathsWithPrefix(files, "receipts/")
			a, b = paths[0], paths[1]
			files[a], files[b] = files[b], files[a]
		})
		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("swapped receipt files verified")
		}
		for _, p := range []string{a, b} {
			it := failedItem(t, rep, p)
			if !strings.Contains(it.Err, "filename claims") {
				t.Errorf("failure %q does not name the digest mismatch", it.Err)
			}
		}
	})

	t.Run("doctored re-digested decision", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		selPath := "decisions/" + FileName(f.selDig) + ".dsse.json"
		var newPath string
		f.tamperEvidence(func(files map[string][]byte) {
			env := files[selPath]
			var e struct {
				Payload     string          `json:"payload"`
				PayloadType string          `json:"payloadType"`
				Signatures  json.RawMessage `json:"signatures"`
			}
			if err := json.Unmarshal(env, &e); err != nil {
				t.Fatal(err)
			}
			dec := f.sel
			dec.Rationale = "doctored: trust me"
			_, canon, err := object.Digest(dec)
			if err != nil {
				t.Fatal(err)
			}
			// Re-digest the doctored payload and rename the file to match —
			// content-address consistent, but the old signature cannot cover
			// the new bytes.
			doctored, err := object.Canonical(map[string]any{
				"payload":     encodeB64(canon),
				"payloadType": e.PayloadType,
				"signatures":  json.RawMessage(e.Signatures),
			})
			if err != nil {
				t.Fatal(err)
			}
			newPath = "decisions/" + FileName(object.DigestBytes(canon)) + ".dsse.json"
			delete(files, selPath)
			files[newPath] = doctored
		})
		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("doctored decision verified")
		}
		it := failedItem(t, rep, newPath)
		if !strings.Contains(it.Err, "no signature verified") {
			t.Errorf("failure %q does not name the signature", it.Err)
		}
	})

	t.Run("doctored unsigned world caught by decision pin", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		loserPath := "worlds/" + FileName(f.worldDigs[1]) + ".json"
		f.tamperEvidence(func(files map[string][]byte) {
			var w object.World
			if err := json.Unmarshal(files[loserPath], &w); err != nil {
				t.Fatal(err)
			}
			w.Outcome = object.OutcomeCrash // rewrite history: "the loser crashed"
			dig, canon, err := object.Digest(w)
			if err != nil {
				t.Fatal(err)
			}
			delete(files, loserPath)
			files["worlds/"+FileName(dig)+".json"] = canon
		})
		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("consistently doctored world verified")
		}
		// The world file itself is self-consistent; the SIGNED decision's
		// subject pin is what catches it.
		selPath := "decisions/" + FileName(f.selDig) + ".dsse.json"
		it := failedItem(t, rep, selPath)
		if !strings.Contains(it.Err, "subject world "+f.worldDigs[1]) {
			t.Errorf("failure %q does not pin the missing subject world", it.Err)
		}
	})

	t.Run("extra unplanned ref", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		stray := Namespace(f.short()) + "/stray"
		tip := gitT(t, f.origin, "rev-parse", EvidenceRef(f.short()))
		gitT(t, f.origin, "update-ref", stray, tip)
		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("namespace with a stray ref verified")
		}
		it := failedItem(t, rep, stray)
		if !strings.Contains(it.Err, "unplanned ref") {
			t.Errorf("failure %q does not name the unplanned ref", it.Err)
		}
	})

	// A candidate ref whose ordinal is not its canonical decimal rendering
	// ("01", "+1") is unplanned by construction — no plan can emit it — even
	// though it aliases a legitimate candidate commit. The "namespace ⊆
	// plan" invariant (decision 10) is only sound if these fail too.
	t.Run("alias ordinal candidate ref", func(t *testing.T) {
		for _, alias := range []string{"01", "+1"} {
			t.Run(alias, func(t *testing.T) {
				f, consumer := tamperFixture(t)
				stray := Namespace(f.short()) + "/cand/" + alias
				tip := gitT(t, f.origin, "rev-parse", CandRef(f.short(), 1))
				gitT(t, f.origin, "update-ref", stray, tip)
				rep := f.fetchRace(consumer)
				if rep.OK {
					t.Fatal("namespace with an alias ordinal ref verified")
				}
				it := failedItem(t, rep, stray)
				if !strings.Contains(it.Err, "unplanned ref") {
					t.Errorf("failure %q does not name the unplanned ref", it.Err)
				}
			})
		}
	})

	t.Run("missing winner candidate ref", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		gitT(t, f.origin, "update-ref", "-d", CandRef(f.short(), 1))
		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("namespace without the winner candidate verified")
		}
		it := failedItem(t, rep, Namespace(f.short()))
		if !strings.Contains(it.Err, "no candidate ref publishes the SELECT winner") {
			t.Errorf("failure %q does not name the missing winner", it.Err)
		}
	})

	t.Run("candidate tree differs from world tree", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		ref := CandRef(f.short(), 1)
		tip := gitT(t, f.origin, "rev-parse", ref)
		msg := gitT(t, f.origin, "log", "-1", "--format=%B", tip)
		baseTree := gitT(t, f.origin, "rev-parse", tip+"^^{tree}")
		parent := gitT(t, f.origin, "rev-parse", tip+"^")
		doctored, err := gitx.CommitTreeEpoch(f.origin, baseTree, parent, msg+"\n")
		if err != nil {
			t.Fatal(err)
		}
		gitT(t, f.origin, "update-ref", ref, doctored)
		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("candidate with a swapped tree verified")
		}
		it := failedItem(t, rep, ref)
		if !strings.Contains(it.Err, "world tree") {
			t.Errorf("failure %q does not name the tree mismatch", it.Err)
		}
	})

	t.Run("wrong key fails every signed item", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		other := testSigner(t)
		rep, err := FetchRace(FetchConfig{
			Repo: consumer, Remote: "origin", Short: f.short(), Pub: other.Public,
		})
		if err != nil {
			t.Fatal(err)
		}
		if rep.OK {
			t.Fatal("closure verified against the wrong key")
		}
		signed, failed := 0, 0
		for _, it := range rep.Items {
			if strings.HasPrefix(it.Path, "decisions/") || strings.HasPrefix(it.Path, "receipts/") ||
				strings.HasPrefix(it.Path, "attestation/") {
				signed++
				if !it.OK {
					failed++
				}
			}
		}
		if signed == 0 || failed != signed {
			t.Errorf("%d of %d signed items failed, want all", failed, signed)
		}
	})

	t.Run("tampered attestation bundle", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		attPath := "attestation/" + FileName(f.attKey) + ".dsse.json"
		f.tamperEvidence(func(files map[string][]byte) {
			b := bytes.Clone(files[attPath])
			b[0] ^= 0xFF
			files[attPath] = b
		})
		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("tampered attestation verified")
		}
		it := failedItem(t, rep, attPath)
		if !strings.Contains(it.Err, "hashes to") {
			t.Errorf("failure %q does not name the content address", it.Err)
		}
	})

	t.Run("budget sum mismatch", func(t *testing.T) {
		f := newFixture(t)
		f.newIntent("budget mismatch")
		f.race(true, false)
		f.budgetDoctor = 999 // the signer attests a wrong budget
		f.admitIntent()
		f.mustPublish(true)
		rep := f.fetchRace(f.clone())
		if rep.OK {
			t.Fatal("wrong attested budget verified")
		}
		it := failedItem(t, rep, "attestation/"+FileName(f.attKey)+".dsse.json")
		if !strings.Contains(it.Err, "wall_ms") {
			t.Errorf("failure %q does not name the budget", it.Err)
		}
	})

	t.Run("replay is load-bearing", func(t *testing.T) {
		f := newFixture(t)
		f.newIntent("wrong winner")
		// A hand-built wrong-winner decision signed with the RIGHT key:
		// subjects swapped so the loser leads. Every signature and digest
		// checks out — only replay through race.Decide catches the lie.
		f.doctorSelect = func(d object.Decision) object.Decision {
			d.Subject = []string{d.Subject[1], d.Subject[0]}
			return d
		}
		f.race(true, false)
		f.mustPublish(true)
		rep := f.fetchRace(f.clone())
		if rep.OK {
			t.Fatal("wrong-winner decision verified")
		}
		selPath := "decisions/" + FileName(f.selDig) + ".dsse.json"
		it := failedItem(t, rep, selPath)
		if !strings.Contains(it.Err, "replay mismatch") {
			t.Errorf("failure %q does not name the replay", it.Err)
		}
	})

	// World objects are the one closure kind authenticated by digest alone
	// (the filename is recomputed from the bytes), so a self-consistent
	// forgery is free to mint. Only reverse containment — every published
	// world is a subject of a signed decision — keeps anyone with push
	// access from splicing an extra World naming attacker-authored code
	// into the evidence commit and shipping it as a candidate ref.
	t.Run("spliced world and candidate ref", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		forgedTree, forgedDig := f.spliceWorld("rm -rf / # attacker code\n")
		ref := Namespace(f.short()) + "/cand/9"
		tip, err := gitx.CommitTreeEpoch(f.origin, forgedTree, f.intent.Base.Commit,
			candidateMessage(9, f.intentDig, forgedDig))
		if err != nil {
			t.Fatal(err)
		}
		gitT(t, f.origin, "update-ref", ref, tip)

		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("spliced world + candidate ref verified")
		}
		it := failedItem(t, rep, "worlds/"+FileName(forgedDig)+".json")
		if !strings.Contains(it.Err, "not a subject of any signed decision") {
			t.Errorf("failure %q does not name the unbound world", it.Err)
		}
	})

	// The same reverse containment for the other plain kind: only the
	// intent's own policy may ride in the closure.
	t.Run("spliced policy", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		forged := object.Policy{
			Schema:    object.SchemaPolicy,
			HardGates: []string{},
			Ranking:   []string{"wall_ms_asc"},
		}
		dig, canon, err := object.Digest(forged)
		if err != nil {
			t.Fatal(err)
		}
		path := "policy/" + FileName(dig) + ".json"
		f.tamperEvidence(func(files map[string][]byte) { files[path] = canon })

		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("closure with a spliced policy verified")
		}
		it := failedItem(t, rep, path)
		if !strings.Contains(it.Err, "is not the intent's policy") {
			t.Errorf("failure %q does not name the uncited policy", it.Err)
		}
	})

	// One world, one candidate ref: a plan never publishes a world twice,
	// so a second ref over the same world is unplanned by construction.
	t.Run("duplicate candidate ref for one world", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		winner := f.sel.Subject[0]
		var winnerTree string
		for i, dig := range f.worldDigs {
			if dig == winner {
				winnerTree = f.worlds[i].Tree
			}
		}
		ref := Namespace(f.short()) + "/cand/9"
		tip, err := gitx.CommitTreeEpoch(f.origin, strings.TrimPrefix(winnerTree, gitx.TreePrefix),
			f.intent.Base.Commit, candidateMessage(9, f.intentDig, winner))
		if err != nil {
			t.Fatal(err)
		}
		gitT(t, f.origin, "update-ref", ref, tip)

		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("namespace with two candidate refs for one world verified")
		}
		it := failedItem(t, rep, ref)
		if !strings.Contains(it.Err, "already published by") {
			t.Errorf("failure %q does not name the duplicate candidate ref", it.Err)
		}
	})

	t.Run("evidence ref missing", func(t *testing.T) {
		f, consumer := tamperFixture(t)
		evRef := EvidenceRef(f.short())
		gitT(t, f.origin, "update-ref", "-d", evRef)
		rep := f.fetchRace(consumer)
		if rep.OK {
			t.Fatal("namespace without an evidence ref verified")
		}
		it := failedItem(t, rep, evRef)
		if !strings.Contains(it.Err, "missing") {
			t.Errorf("failure %q does not name the missing evidence ref", it.Err)
		}
	})
}

func TestFetchRaceHealsAfterRepublish(t *testing.T) {
	f, consumer := tamperFixture(t)
	f.tamperEvidence(func(files map[string][]byte) {
		path := pathsWithPrefix(files, "receipts/")[0]
		files[path] = flipPayloadChar(t, files[path])
	})
	if rep := f.fetchRace(consumer); rep.OK {
		t.Fatal("tampered closure verified")
	}
	// Republish heals: the plan-diff sees the tampered tip and pushes the
	// true evidence commit under a lease on the observed (tampered) value.
	res := f.mustPublish(true)
	if len(res.Pushed) != 1 || res.Pushed[0].Ref != EvidenceRef(f.short()) {
		t.Fatalf("healing publish = %+v", res)
	}
	if rep := f.fetchRace(consumer); !rep.OK {
		t.Fatalf("healed closure still fails: %+v", rep.Items)
	}
}

func TestFetchRaceMachineryErrors(t *testing.T) {
	f := newFixture(t)
	f.newIntent("machinery")
	f.race(true)
	f.mustPublish(false)
	consumer := f.clone()

	// Unknown namespace: nothing published under that short.
	if _, err := FetchRace(FetchConfig{
		Repo: consumer, Remote: "origin", Short: strings.Repeat("0", 12), Pub: f.signer.Public,
	}); err == nil || !strings.Contains(err.Error(), "no refs under") {
		t.Errorf("empty namespace = %v, want machinery error", err)
	}
	// Unreachable remote.
	if _, err := FetchRace(FetchConfig{
		Repo: consumer, Remote: "nosuch", Short: f.short(), Pub: f.signer.Public,
	}); err == nil {
		t.Errorf("unreachable remote did not error")
	}
	// Config validation.
	if _, err := FetchRace(FetchConfig{Repo: consumer, Remote: "origin", Short: "xyz", Pub: f.signer.Public}); err == nil {
		t.Errorf("malformed short accepted")
	}
}

// encodeB64 mirrors signing's payload encoding for the doctored-envelope
// tamper case.
func encodeB64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// flipPayloadChar swaps one base64 character of the envelope's payload in
// place: the JSON and the encoding stay valid, only the signed bytes move.
func flipPayloadChar(t *testing.T, env []byte) []byte {
	t.Helper()
	marker := []byte(`"payload":"`)
	i := bytes.Index(env, marker)
	if i < 0 {
		t.Fatal("envelope has no payload field")
	}
	out := bytes.Clone(env)
	pos := i + len(marker)
	if out[pos] == 'A' {
		out[pos] = 'B'
	} else {
		out[pos] = 'A'
	}
	return out
}
