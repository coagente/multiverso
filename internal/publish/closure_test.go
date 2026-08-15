package publish

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/race"
)

func TestBuildClosureItemPathsNonAdmitted(t *testing.T) {
	f := newFixture(t)
	f.newIntent("closure paths")
	f.race(true, false)
	cl := f.buildClosure()

	if cl.IntentDig != f.intentDig || cl.Short != f.short() || cl.SelectDig != f.selDig {
		t.Fatalf("closure identity = %+v", cl)
	}
	if cl.Admitted || cl.AdmitDig != "" {
		t.Fatalf("non-admitted closure claims admission: %+v", cl)
	}

	want := []string{
		"decisions/" + FileName(f.selDig) + ".dsse.json",
		"intent/" + FileName(f.intentDig) + ".json",
		"policy/" + FileName(f.polDig) + ".json",
	}
	for _, dig := range f.receiptDigs {
		want = append(want, "receipts/"+FileName(dig)+".dsse.json")
	}
	for _, dig := range f.worldDigs {
		want = append(want, "worlds/"+FileName(dig)+".json")
	}
	sortStrings(want)
	got := make([]string, 0, len(cl.Items))
	for _, it := range cl.Items {
		got = append(got, it.Path)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("item paths:\n got %v\nwant %v", got, want)
	}

	// Candidates carry ordinal order and flag exactly one winner — the
	// SELECT subject head.
	if len(cl.Candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(cl.Candidates))
	}
	winners := 0
	for i, cand := range cl.Candidates {
		if cand.Ordinal != i+1 || cand.Dig != f.worldDigs[i] {
			t.Errorf("candidate %d = %+v", i, cand)
		}
		if cand.Winner {
			winners++
			if cand.Dig != f.sel.Subject[0] {
				t.Errorf("winner %s is not the SELECT subject head %s", cand.Dig, f.sel.Subject[0])
			}
		}
	}
	if winners != 1 {
		t.Errorf("winners = %d, want 1", winners)
	}
}

func TestBuildClosureAdmittedItems(t *testing.T) {
	f := newFixture(t)
	f.newIntent("admitted closure")
	f.race(true, false)
	f.admitIntent()
	cl := f.buildClosure()

	if !cl.Admitted || cl.AdmitDig != f.admitDig {
		t.Fatalf("admitted closure = %+v", cl)
	}
	wantExtra := []string{
		"attestation/" + FileName(f.attKey) + ".dsse.json",
		"decisions/" + FileName(f.admitDig) + ".dsse.json",
		"receipts/" + FileName(f.applyDig) + ".dsse.json",
		"receipts/" + FileName(f.gateDig) + ".dsse.json",
	}
	paths := map[string]bool{}
	for _, it := range cl.Items {
		paths[it.Path] = true
	}
	for _, p := range wantExtra {
		if !paths[p] {
			t.Errorf("admitted closure misses %s; has %v", p, paths)
		}
	}
	// The attestation ships verbatim CAS bytes.
	for _, it := range cl.Items {
		if it.Path == wantExtra[0] {
			bundle, err := f.store.Get(f.attKey)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(it.Bytes, bundle) {
				t.Errorf("attestation item bytes differ from CAS bundle")
			}
		}
	}
}

func TestBuildClosureReplayComplete(t *testing.T) {
	f := newFixture(t)
	f.newIntent("replay complete")
	f.race(true, true, false)
	cl := f.buildClosure()

	// Decode worlds and receipts back out of the published items alone and
	// re-run the decision function: PRD principle 3 — any third party
	// re-derives why W won.
	var worlds []object.World
	var receipts []object.Receipt
	inEvidence := map[string]bool{}
	for _, dig := range cl.Select.Evidence {
		inEvidence[dig] = true
	}
	for _, it := range cl.Items {
		switch {
		case strings.HasPrefix(it.Path, "worlds/"):
			var w object.World
			if err := json.Unmarshal(it.Bytes, &w); err != nil {
				t.Fatal(err)
			}
			worlds = append(worlds, w)
		case strings.HasPrefix(it.Path, "receipts/"):
			payload, err := OpenItem(it.Bytes, PayloadTypeReceipt, f.signer.Public)
			if err != nil {
				t.Fatalf("open %s: %v", it.Path, err)
			}
			if !inEvidence[object.DigestBytes(payload)] {
				continue
			}
			var r object.Receipt
			if err := json.Unmarshal(payload, &r); err != nil {
				t.Fatal(err)
			}
			receipts = append(receipts, r)
		}
	}
	got := race.Decide(f.policy, worlds, receipts)
	if detail := diffDecision(cl.Select, got); detail != "" {
		t.Errorf("closure is not replay-complete: %s", detail)
	}
}

func TestBuildClosureLatestSelectWins(t *testing.T) {
	f := newFixture(t)
	f.newIntent("two races")
	f.race(true, false)
	firstWorlds := append([]string(nil), f.worldDigs...)
	firstSel := f.selDig
	f.race(false, true, true) // superseding race, three candidates
	cl := f.buildClosure()

	if cl.SelectDig == firstSel {
		t.Fatalf("closure cites the superseded SELECT %s", firstSel)
	}
	if len(cl.Candidates) != 3 {
		t.Fatalf("candidates = %d, want 3 (latest race)", len(cl.Candidates))
	}
	for i, cand := range cl.Candidates {
		if cand.Ordinal != i+1 || cand.Dig != f.worldDigs[i] {
			t.Errorf("candidate %d = %+v, want ordinal %d dig %s", i, cand, i+1, f.worldDigs[i])
		}
	}
	// Superseded worlds are absent from the items.
	for _, it := range cl.Items {
		for _, old := range firstWorlds {
			if strings.Contains(it.Path, FileName(old)) {
				t.Errorf("superseded world %s leaked into %s", old, it.Path)
			}
		}
	}
	// Winner of race 2 is world 2 (first passer by wall-time ranking).
	if cl.Select.Subject[0] != f.worldDigs[1] {
		t.Errorf("winner = %s, want %s", cl.Select.Subject[0], f.worldDigs[1])
	}
}

func TestBuildClosureDeterministic(t *testing.T) {
	f := newFixture(t)
	f.newIntent("deterministic")
	f.race(true, false)
	f.admitIntent()
	one := f.buildClosure()
	two := f.buildClosure()
	if len(one.Items) != len(two.Items) {
		t.Fatalf("item counts differ: %d vs %d", len(one.Items), len(two.Items))
	}
	for i := range one.Items {
		if one.Items[i].Path != two.Items[i].Path {
			t.Errorf("item %d path %q vs %q", i, one.Items[i].Path, two.Items[i].Path)
		}
		if !bytes.Equal(one.Items[i].Bytes, two.Items[i].Bytes) {
			t.Errorf("item %s bytes differ across builds", one.Items[i].Path)
		}
	}
}

func TestBuildClosureDP3(t *testing.T) {
	f := newFixture(t)
	f.newIntent("dp3")
	f.race(true, false)
	cl := f.buildClosure()
	// Prompt and transcript payloads live only in the private CAS; the
	// closure carries their CAS keys inside world objects, never the bytes.
	for _, it := range cl.Items {
		if bytes.Contains(it.Bytes, []byte("secret prompt")) ||
			bytes.Contains(it.Bytes, []byte("secret transcript")) {
			t.Errorf("item %s leaks a private payload", it.Path)
		}
	}
	// The keys themselves do appear (inside the world objects) — that is
	// the DP-3 split, not a leak.
	var worldItem *Item
	for i := range cl.Items {
		if strings.HasPrefix(cl.Items[i].Path, "worlds/") {
			worldItem = &cl.Items[i]
			break
		}
	}
	if worldItem == nil || !bytes.Contains(worldItem.Bytes, []byte(`"context":"sha256:`)) {
		t.Errorf("world item does not carry the context CAS key")
	}
}

func TestBuildClosureNoSelect(t *testing.T) {
	f := newFixture(t)
	f.newIntent("never raced")
	if _, err := BuildClosure(f.led, f.store, f.signer, f.intentDig); err == nil ||
		!strings.Contains(err.Error(), "no SELECT decision") {
		t.Errorf("BuildClosure without a SELECT = %v", err)
	}
	// Unknown intent digest.
	bogus := "mv0:" + strings.Repeat("9", 64)
	if _, err := BuildClosure(f.led, f.store, f.signer, bogus); err == nil ||
		!strings.Contains(err.Error(), "no intent") {
		t.Errorf("BuildClosure(unknown intent) = %v", err)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
