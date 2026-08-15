package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/workspace"
)

func cmdInit(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("init", stderr)
	dir := fs.String("dir", ".", "repository directory")
	keys := fs.Bool("keys", false, "generate signing keys in an existing workspace (error if present)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	// --keys retrofits a pre-M1a workspace with a keypair; plain init
	// still refuses to re-init and always generates keys in fresh
	// workspaces.
	if *keys {
		ws, err := workspace.Open(*dir)
		if err != nil {
			return fmt.Errorf("init: %w", err)
		}
		defer ws.Close()
		signer, err := ws.GenerateKeys()
		if err != nil {
			return fmt.Errorf("init: %w", err)
		}
		fmt.Fprintf(stdout, "generated signing key %s\n", signer.KeyID)
		return nil
	}

	// mvo is a git control plane: every later verb resolves HEAD, and a
	// workspace outside a worktree is a dead end that only surfaces one
	// verb later as a raw `git rev-parse` failure. Refusing here also
	// closes a quiet key-proliferation path — a typo'd --dir in CI used to
	// mint a fresh ed25519 keypair into a fresh directory and exit 0.
	if err := requireGitWorktree(*dir); err != nil {
		return err
	}

	ws, err := workspace.Init(*dir)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	defer ws.Close()

	fmt.Fprintf(stdout, "initialized %s (default policy %s)\n", ws.Dir, ws.Config.DefaultPolicy)
	// The private signing key is the trust anchor for every attestation
	// this workspace will ever produce, and it is written unencrypted.
	// Saying where it landed is the difference between a user who knows
	// what to protect and one who finds out from `git show`.
	fmt.Fprintf(stdout, "signing key: %s (PRIVATE, unencrypted — never commit or copy it)\n",
		filepath.Join(ws.KeysDir(), "local.key"))
	fmt.Fprintf(stdout, "git ignore:  %s%s\n", ws.Ignore.Path, ignoreNote(ws.Ignore))
	if ws.Ignore.Fallback {
		fmt.Fprintf(stderr, "mvo: init: warning: could not write .git/info/exclude (%s), so %s was edited instead.\n",
			ws.Ignore.Reason, ws.Ignore.Path)
		fmt.Fprintf(stderr, "mvo: init: warning: that file is TRACKED — `git reset --hard` or `git checkout -- .` will revert this line, after which `git add -A` commits %s, your unencrypted private signing key. Commit the .gitignore change now.\n",
			filepath.Join(ws.KeysDir(), "local.key"))
	}
	return nil
}

// ignoreNote annotates the ignore-rule line so "already there" is visibly
// different from "just written".
func ignoreNote(ign workspace.IgnoreResult) string {
	switch {
	case ign.Existed:
		return " (rule already present; nothing written)"
	case ign.Fallback:
		return " (FALLBACK: tracked file — see warning below)"
	}
	return " (untracked; survives `git reset --hard`)"
}

// requireGitWorktree refuses a directory git does not recognise as a
// worktree, naming the directory rather than leaking a `git rev-parse`
// failure from three layers down.
func requireGitWorktree(dir string) error {
	if _, err := gitx.CommonDir(dir); err != nil {
		return fmt.Errorf("init: %s is not a git repository (or does not exist); mvo requires a git worktree — run `git init` there first", dir)
	}
	return nil
}
