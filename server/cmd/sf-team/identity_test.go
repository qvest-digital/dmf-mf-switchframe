package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentID_PrefersEnvVar(t *testing.T) {
	t.Setenv("SF_TEAM_HOME", t.TempDir())
	t.Setenv("SF_AGENT", "agent-zeta")
	got, err := agentID("/tmp/whatever")
	if err != nil {
		t.Fatalf("agentID: %v", err)
	}
	if got != "agent-zeta" {
		t.Fatalf("got %q want agent-zeta", got)
	}
}

func TestAgentID_FallsBackToWorktreeBasename(t *testing.T) {
	t.Setenv("SF_TEAM_HOME", t.TempDir())
	t.Setenv("SF_AGENT", "")
	dir := filepath.Join(t.TempDir(), "sf-agent-b")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := agentID(dir)
	if err != nil {
		t.Fatalf("agentID: %v", err)
	}
	if got != "sf-agent-b" {
		t.Fatalf("got %q want sf-agent-b", got)
	}
}

func TestAgentID_RejectsEmptyWorktree(t *testing.T) {
	t.Setenv("SF_TEAM_HOME", t.TempDir())
	t.Setenv("SF_AGENT", "")
	if _, err := agentID(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestAgentID_UsesPersistedOverride(t *testing.T) {
	t.Setenv("SF_AGENT", "")
	tmp := t.TempDir()
	t.Setenv("SF_TEAM_HOME", tmp)
	clearSessionEnvs(t)
	if err := ensureHome(); err != nil {
		t.Fatal(err)
	}
	if err := writePersistedAgentID("tester"); err != nil {
		t.Fatal(err)
	}
	got, err := agentID("/tmp/some-worktree")
	if err != nil {
		t.Fatal(err)
	}
	if got != "tester" {
		t.Fatalf("got %q want tester (persisted should beat worktree basename)", got)
	}
}

// TestPersistedAgentID_IsolatedPerSession pins the fix for the
// parallel-Claude-Code collision bug: two sessions sharing the same home
// but with different session keys must not stomp on each other's id file.
func TestPersistedAgentID_IsolatedPerSession(t *testing.T) {
	t.Setenv("SF_TEAM_HOME", t.TempDir())
	t.Setenv("SF_AGENT", "")
	clearSessionEnvs(t)
	if err := ensureHome(); err != nil {
		t.Fatal(err)
	}

	// Session A registers agent "alpha".
	t.Setenv("SF_TEAM_SESSION_ID", "session-A")
	if err := writePersistedAgentID("alpha"); err != nil {
		t.Fatal(err)
	}
	if got := readPersistedAgentID(); got != "alpha" {
		t.Fatalf("session-A: got %q want alpha", got)
	}

	// Session B registers agent "bravo" in the same home.
	t.Setenv("SF_TEAM_SESSION_ID", "session-B")
	if err := writePersistedAgentID("bravo"); err != nil {
		t.Fatal(err)
	}
	if got := readPersistedAgentID(); got != "bravo" {
		t.Fatalf("session-B: got %q want bravo", got)
	}

	// Session A still sees "alpha" — the prior fix put B's id in a
	// different file, so A is not clobbered.
	t.Setenv("SF_TEAM_SESSION_ID", "session-A")
	if got := readPersistedAgentID(); got != "alpha" {
		t.Fatalf("session-A after session-B write: got %q want alpha (session isolation broken)", got)
	}
}

// TestPersistedAgentID_FallsBackToLegacyFile pins that users who already
// have a persisted id in the single legacy location before upgrading do
// not lose their identity after this change.
func TestPersistedAgentID_FallsBackToLegacyFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SF_TEAM_HOME", tmp)
	t.Setenv("SF_AGENT", "")
	clearSessionEnvs(t)
	if err := ensureHome(); err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-upgrade install: id file at the legacy single path.
	if err := os.WriteFile(filepath.Join(tmp, "agent-id.txt"), []byte("legacy-id\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SF_TEAM_SESSION_ID", "whatever-session")
	if got := readPersistedAgentID(); got != "legacy-id" {
		t.Fatalf("legacy fallback: got %q want legacy-id", got)
	}
}

// clearSessionEnvs unsets every env var that sessionKey consults, so tests
// can set only what they need without leakage from the host shell
// (iTerm/Terminal.app export TERM_SESSION_ID by default).
func clearSessionEnvs(t *testing.T) {
	t.Helper()
	for _, env := range []string{
		"SF_TEAM_SESSION_ID",
		"TERM_SESSION_ID",
		"ITERM_SESSION_ID",
		"TMUX_PANE",
		"STY",
		"WT_SESSION",
	} {
		t.Setenv(env, "")
	}
}
