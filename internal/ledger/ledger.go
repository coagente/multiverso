// Package ledger implements a hash-chained append-only event log on
// SQLite (DP-1, NFR-3). Append-only is enforced by the API surface: no
// update/delete statements exist anywhere in this package.
package ledger

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/coagente/multiverso/internal/object"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS events (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  ts          TEXT NOT NULL,
  type        TEXT NOT NULL,
  payload     BLOB NOT NULL,
  payload_dig TEXT NOT NULL,
  prev_hash   TEXT NOT NULL,
  hash        TEXT NOT NULL
);`

// Event is one ledger row.
type Event struct {
	Seq        int64
	TS         string // RFC3339 UTC
	Type       string
	Payload    []byte
	PayloadDig string
	PrevHash   string // "" for seq 1
	Hash       string
}

// Ledger is an append-only, hash-chained event log.
type Ledger struct {
	db *sql.DB
}

// Open opens (creating if needed) the ledger database at path, in WAL mode.
// TODO(M1): refuse network filesystems (NFR-3).
func Open(path string) (*Ledger, error) {
	// busy_timeout makes concurrent mvo invocations wait for the lock
	// instead of failing immediately with SQLITE_BUSY; _txlock=immediate
	// makes Append's read-tip-then-insert transaction take the write lock
	// at Begin, so waiting writers queue on the timeout rather than hit an
	// un-retryable SQLITE_BUSY upgrading a read snapshot to a write.
	db, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("ledger: open %s: %w", path, err)
	}
	// Single connection serializes writers and keeps last-hash reads and
	// inserts on one snapshot.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("ledger: enable WAL on %s: %w", path, err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("ledger: create schema in %s: %w", path, err)
	}
	return &Ledger{db: db}, nil
}

// Close closes the underlying database.
func (l *Ledger) Close() error {
	if err := l.db.Close(); err != nil {
		return fmt.Errorf("ledger: close: %w", err)
	}
	return nil
}

// Append records one event and returns its sequence number. The row hash
// chains to the previous row: hex(sha256(prev_hash + "\n" + ts + "\n" +
// type + "\n" + payload_dig)) — ts is folded in so when-it-happened
// evidence is tamper-evident too.
func (l *Ledger) Append(typ string, payload []byte) (int64, error) {
	tx, err := l.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("ledger: append %s: begin: %w", typ, err)
	}
	defer tx.Rollback()

	prev := ""
	err = tx.QueryRow(`SELECT hash FROM events ORDER BY seq DESC LIMIT 1`).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("ledger: append %s: read tip: %w", typ, err)
	}

	dig := object.DigestBytes(payload)
	ts := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(
		`INSERT INTO events (ts, type, payload, payload_dig, prev_hash, hash) VALUES (?, ?, ?, ?, ?, ?)`,
		ts, typ, payload, dig, prev, rowHash(prev, ts, typ, dig),
	)
	if err != nil {
		return 0, fmt.Errorf("ledger: append %s: insert: %w", typ, err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("ledger: append %s: last insert id: %w", typ, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("ledger: append %s: commit: %w", typ, err)
	}
	return seq, nil
}

// Scan calls fn for every event in ascending seq order.
func (l *Ledger) Scan(fn func(Event) error) error {
	rows, err := l.db.Query(
		`SELECT seq, ts, type, payload, payload_dig, prev_hash, hash FROM events ORDER BY seq ASC`)
	if err != nil {
		return fmt.Errorf("ledger: scan: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.TS, &e.Type, &e.Payload, &e.PayloadDig, &e.PrevHash, &e.Hash); err != nil {
			return fmt.Errorf("ledger: scan row: %w", err)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("ledger: scan: %w", err)
	}
	return nil
}

// VerifyChain recomputes every payload digest and chain hash and returns an
// error naming the first row where the recorded values do not match. Seqs
// must run 1..N without gaps and N must equal the AUTOINCREMENT high-water
// mark: AUTOINCREMENT never reuses seqs, so a gap or a short tail means
// rows were deleted (the truncate-then-append history rewrite).
func (l *Ledger) VerifyChain() error {
	prev := ""
	var last int64
	err := l.Scan(func(e Event) error {
		if e.Seq != last+1 {
			return fmt.Errorf("ledger: verify seq %d: gap after seq %d (rows deleted?)", e.Seq, last)
		}
		if got := object.DigestBytes(e.Payload); got != e.PayloadDig {
			return fmt.Errorf("ledger: verify seq %d: payload digest mismatch: recorded %s, recomputed %s", e.Seq, e.PayloadDig, got)
		}
		if e.PrevHash != prev {
			return fmt.Errorf("ledger: verify seq %d: prev_hash mismatch: recorded %q, expected %q", e.Seq, e.PrevHash, prev)
		}
		if want := rowHash(prev, e.TS, e.Type, e.PayloadDig); e.Hash != want {
			return fmt.Errorf("ledger: verify seq %d: hash mismatch: recorded %q, recomputed %q", e.Seq, e.Hash, want)
		}
		prev = e.Hash
		last = e.Seq
		return nil
	})
	if err != nil {
		return err
	}
	var mark int64
	err = l.db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'events'`).Scan(&mark)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("ledger: verify: read autoincrement mark: %w", err)
	}
	if mark != last {
		return fmt.Errorf("ledger: verify: last seq %d, autoincrement mark %d (tail rows deleted?)", last, mark)
	}
	return nil
}

func rowHash(prevHash, ts, typ, payloadDig string) string {
	sum := sha256.Sum256([]byte(prevHash + "\n" + ts + "\n" + typ + "\n" + payloadDig))
	return hex.EncodeToString(sum[:])
}
