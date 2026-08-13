package race

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
)

// recordObject digests v, stores its canonical bytes in CAS, and appends
// them to the ledger under typ. Returns the object digest. Under
// parallelism callers serialize through raceRun.recordObjectLocked.
func recordObject(cfg Config, typ string, v any) (string, error) {
	dig, canon, err := object.Digest(v)
	if err != nil {
		return "", fmt.Errorf("race: digest %s: %w", typ, err)
	}
	if _, err := cfg.CAS.Put(canon); err != nil {
		return "", fmt.Errorf("race: store %s: %w", typ, err)
	}
	if _, err := cfg.Ledger.Append(typ, canon); err != nil {
		return "", fmt.Errorf("race: record %s: %w", typ, err)
	}
	return dig, nil
}

func appendEvent(led *ledger.Ledger, typ string, body map[string]any) error {
	payload, err := object.Canonical(body)
	if err != nil {
		return fmt.Errorf("race: encode %s: %w", typ, err)
	}
	if _, err := led.Append(typ, payload); err != nil {
		return fmt.Errorf("race: record %s: %w", typ, err)
	}
	return nil
}

// LoadPolicy fetches a Policy object from CAS by its digest and validates
// its schema. Race runs and audit replay share it: both must read the same
// policy the same way (NFR-1).
func LoadPolicy(store *cas.Store, dig string) (object.Policy, error) {
	var pol object.Policy
	if err := loadObject(store, dig, &pol); err != nil {
		return pol, err
	}
	if pol.Schema != object.SchemaPolicy {
		return pol, fmt.Errorf("race: policy %s has schema %q, want %q", dig, pol.Schema, object.SchemaPolicy)
	}
	return pol, nil
}

// loadObject fetches an object's canonical bytes from CAS by its "mv0:"
// digest and decodes them into v.
func loadObject(store *cas.Store, dig string, v any) error {
	key, err := object.CASKey(dig)
	if err != nil {
		return fmt.Errorf("race: %w", err)
	}
	b, err := store.Get(key)
	if err != nil {
		return fmt.Errorf("race: load %s: %w", dig, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("race: decode %s: %w", dig, err)
	}
	return nil
}

// EnvDigest builds the T0 env manifest for a directory and returns its
// digest. Kept with its M1b signature — admit's call sites compile and
// behave unchanged — and delegating to the backend package's T0 manifest
// builder, whose bytes are the M1b golden (M1c decision 11).
func EnvDigest(store *cas.Store, dir string) (string, error) {
	return backend.T0EnvDigest(store, dir)
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
