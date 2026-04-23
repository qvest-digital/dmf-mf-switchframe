package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func agentID(worktree string) (string, error) {
	if env := strings.TrimSpace(os.Getenv("SF_AGENT")); env != "" {
		return env, nil
	}
	if persisted := readPersistedAgentID(); persisted != "" {
		return persisted, nil
	}
	if worktree == "" {
		return "", errors.New("cannot infer agent id: SF_AGENT unset, no persisted id, and worktree empty")
	}
	return filepath.Base(worktree), nil
}

// readPersistedAgentID loads the persisted agent id from disk. First tries
// the per-session file (isolates parallel Claude Code sessions sharing a
// home); falls back to the legacy single file for users who persisted an id
// before per-session isolation landed.
func readPersistedAgentID() string {
	home, err := teamHome()
	if err != nil {
		return ""
	}
	if sessionPath := sessionAgentIDPath(home); sessionPath != "" {
		if data, err := os.ReadFile(sessionPath); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "agent-id.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writePersistedAgentID stores id at the per-session path when a session key
// is available, so parallel Claude Code sessions in the same home do not
// overwrite each other's identity. Falls back to the legacy single path.
func writePersistedAgentID(id string) error {
	home, err := teamHome()
	if err != nil {
		return err
	}
	path := sessionAgentIDPath(home)
	if path == "" {
		path = filepath.Join(home, "agent-id.txt")
	}
	return os.WriteFile(path, []byte(id+"\n"), 0o644)
}

// sessionAgentIDPath returns a per-session path under home when a stable
// session key is available, else empty (signal to use the legacy single
// path). The hash of the key guarantees a filesystem-safe filename
// regardless of what the terminal emulator puts in the env var.
func sessionAgentIDPath(home string) string {
	key := sessionKey()
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(home, "agent-id."+hex.EncodeToString(sum[:6])+".txt")
}

// sessionKey returns a discriminator unique to the current Claude Code /
// terminal session, stable across sf-team invocations within that session.
// Empty string means no reliable session identity is available — callers
// should fall back to the single shared id file.
//
// Priority ladder:
//  1. SF_TEAM_SESSION_ID — explicit override for tests and advanced setups.
//  2. TERM_SESSION_ID — set by iTerm2 and Apple Terminal.app per window/tab
//     (inherited by every sf-team invocation from Claude Code's Bash tool).
//  3. ITERM_SESSION_ID — iTerm2-specific duplicate of the above.
//  4. TMUX_PANE — stable for the lifetime of a tmux pane.
//  5. STY — GNU screen session name.
//  6. WT_SESSION — Windows Terminal session GUID.
//
// Every value is a stable string for the lifetime of the hosting terminal
// session, so the derived filename stays constant across every sf-team call
// from the same Claude Code session. If none of the above is set (plain
// shell with no multiplexer, or CI) the caller falls back to the legacy
// single file; users in that situation can set SF_TEAM_SESSION_ID manually
// to get isolation.
func sessionKey() string {
	for _, env := range []string{
		"SF_TEAM_SESSION_ID",
		"TERM_SESSION_ID",
		"ITERM_SESSION_ID",
		"TMUX_PANE",
		"STY",
		"WT_SESSION",
	} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	return ""
}

func detectWorktree() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
