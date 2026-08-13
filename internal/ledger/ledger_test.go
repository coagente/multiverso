package ledger

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

func openWith10(t *testing.T) *Ledger {
	t.Helper()
	l, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	for i := 1; i <= 10; i++ {
		payload, err := object.Canonical(map[string]any{"i": i, "note": "event"})
		if err != nil {
			t.Fatalf("Canonical: %v", err)
		}
		seq, err := l.Append("world.created", payload)
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		if seq != int64(i) {
			t.Fatalf("Append %d returned seq %d", i, seq)
		}
	}
	return l
}

func TestOpenSetsWALMode(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	var mode string
	if err := l.db.QueryRow(`PRAGMA journal_mode;`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want \"wal\"", mode)
	}
}

func TestVerifyChainPasses(t *testing.T) {
	l := openWith10(t)
	if err := l.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain on clean ledger: %v", err)
	}

	// The chain links rows: each prev_hash equals the previous row's hash,
	// and the first row's prev_hash is "".
	var prev string
	if err := l.Scan(func(e Event) error {
		if e.PrevHash != prev {
			return fmt.Errorf("seq %d: prev_hash %q, want %q", e.Seq, e.PrevHash, prev)
		}
		prev = e.Hash
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyChainDetectsTampering(t *testing.T) {
	tests := []struct {
		name   string
		tamper string // direct SQL UPDATE, bypassing the append-only API
		args   func(l *Ledger, t *testing.T) []any
		want   string
	}{
		{
			name:   "payload byte flipped",
			tamper: `UPDATE events SET payload = ? WHERE seq = 5`,
			args: func(l *Ledger, t *testing.T) []any {
				var payload []byte
				if err := l.db.QueryRow(`SELECT payload FROM events WHERE seq = 5`).Scan(&payload); err != nil {
					t.Fatalf("read payload: %v", err)
				}
				payload[0] ^= 0x01 // flip one bit of one byte
				return []any{payload}
			},
			want: "payload digest mismatch",
		},
		{
			name:   "type rewritten",
			tamper: `UPDATE events SET type = 'decision.recorded' WHERE seq = 3`,
			args:   func(*Ledger, *testing.T) []any { return nil },
			want:   "hash mismatch",
		},
		{
			name:   "hash rewritten breaks link to next row",
			tamper: `UPDATE events SET hash = lower(hex(randomblob(32))) WHERE seq = 7`,
			args:   func(*Ledger, *testing.T) []any { return nil },
			want:   "mismatch",
		},
		{
			name:   "ts rewritten",
			tamper: `UPDATE events SET ts = '1999-01-01T00:00:00Z' WHERE seq = 4`,
			args:   func(*Ledger, *testing.T) []any { return nil },
			want:   "hash mismatch",
		},
		{
			name:   "middle row deleted",
			tamper: `DELETE FROM events WHERE seq = 5`,
			args:   func(*Ledger, *testing.T) []any { return nil },
			want:   "gap after seq",
		},
		{
			name:   "tail rows deleted",
			tamper: `DELETE FROM events WHERE seq >= 6`,
			args:   func(*Ledger, *testing.T) []any { return nil },
			want:   "tail rows deleted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := openWith10(t)
			if err := l.VerifyChain(); err != nil {
				t.Fatalf("VerifyChain before tampering: %v", err)
			}
			if _, err := l.db.Exec(tt.tamper, tt.args(l, t)...); err != nil {
				t.Fatalf("tamper: %v", err)
			}
			err := l.VerifyChain()
			if err == nil {
				t.Fatal("VerifyChain passed on tampered ledger")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("VerifyChain error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

// Truncating the tail and appending fresh events is the cheap history
// rewrite: the new row chains onto the new tip, but AUTOINCREMENT never
// reuses seqs, so the gap gives it away.
func TestVerifyChainDetectsTruncateThenAppend(t *testing.T) {
	l := openWith10(t)
	if _, err := l.db.Exec(`DELETE FROM events WHERE seq >= 6`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	payload, err := object.Canonical(map[string]any{"note": "rewritten history"})
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	seq, err := l.Append("world.created", payload)
	if err != nil {
		t.Fatalf("Append after truncation: %v", err)
	}
	if seq <= 10 {
		t.Fatalf("Append after truncation reused seq %d", seq)
	}
	err = l.VerifyChain()
	if err == nil {
		t.Fatal("VerifyChain passed on truncated-then-appended ledger")
	}
	if !strings.Contains(err.Error(), "gap after seq") {
		t.Fatalf("VerifyChain error = %q, want a seq gap", err)
	}
}

func TestScanOrderAndContent(t *testing.T) {
	l := openWith10(t)
	var seqs []int64
	if err := l.Scan(func(e Event) error {
		seqs = append(seqs, e.Seq)
		if e.Type != "world.created" {
			return fmt.Errorf("seq %d: type %q", e.Seq, e.Type)
		}
		if got := object.DigestBytes(e.Payload); got != e.PayloadDig {
			return fmt.Errorf("seq %d: stored digest %q != recomputed %q", e.Seq, e.PayloadDig, got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seqs) != 10 {
		t.Fatalf("scanned %d events, want 10", len(seqs))
	}
	for i, s := range seqs {
		if s != int64(i+1) {
			t.Fatalf("seqs[%d] = %d, want ascending from 1", i, s)
		}
	}
}
