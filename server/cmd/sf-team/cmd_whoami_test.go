package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout swapped for a pipe and returns
// everything fn wrote to stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close write: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	r.Close()
	return buf.String()
}

// TestWhoami_PrintsPersistedIDForSession pins that `sf-team whoami` emits
// the persisted agent id for the active session key — the entry point
// that lets callers get their identity without cat-ing the (wrong-under-
// per-session-isolation) legacy agent-id.txt file.
func TestWhoami_PrintsPersistedIDForSession(t *testing.T) {
	t.Setenv("SF_TEAM_HOME", t.TempDir())
	t.Setenv("SF_AGENT", "")
	clearSessionEnvs(t)
	t.Setenv("SF_TEAM_SESSION_ID", "session-whoami-test")
	if err := ensureHome(); err != nil {
		t.Fatal(err)
	}
	if err := writePersistedAgentID("agent-zeta"); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() {
		code = runWhoami(nil)
	})
	if code != 0 {
		t.Fatalf("runWhoami exit=%d want 0", code)
	}
	got := strings.TrimSpace(out)
	if got != "agent-zeta" {
		t.Fatalf("runWhoami stdout=%q want agent-zeta", got)
	}
}

// TestWhoami_PrefersSFAgentEnv pins that the SF_AGENT env override wins
// over a persisted id for this session, matching agentID() precedence.
func TestWhoami_PrefersSFAgentEnv(t *testing.T) {
	t.Setenv("SF_TEAM_HOME", t.TempDir())
	clearSessionEnvs(t)
	t.Setenv("SF_TEAM_SESSION_ID", "session-whoami-env")
	if err := ensureHome(); err != nil {
		t.Fatal(err)
	}
	if err := writePersistedAgentID("agent-persisted"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SF_AGENT", "agent-from-env")

	var code int
	out := captureStdout(t, func() {
		code = runWhoami(nil)
	})
	if code != 0 {
		t.Fatalf("runWhoami exit=%d want 0", code)
	}
	got := strings.TrimSpace(out)
	if got != "agent-from-env" {
		t.Fatalf("runWhoami stdout=%q want agent-from-env", got)
	}
}

// TestWhoami_IsolatedPerSession pins that whoami honours per-session
// isolation: two sessions with the same SF_TEAM_HOME but different
// session keys see their own persisted ids, not each other's.
func TestWhoami_IsolatedPerSession(t *testing.T) {
	t.Setenv("SF_TEAM_HOME", t.TempDir())
	t.Setenv("SF_AGENT", "")
	clearSessionEnvs(t)
	if err := ensureHome(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SF_TEAM_SESSION_ID", "session-A")
	if err := writePersistedAgentID("agent-alpha"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SF_TEAM_SESSION_ID", "session-B")
	if err := writePersistedAgentID("agent-bravo"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SF_TEAM_SESSION_ID", "session-A")
	out := captureStdout(t, func() { runWhoami(nil) })
	if got := strings.TrimSpace(out); got != "agent-alpha" {
		t.Fatalf("session-A whoami=%q want agent-alpha (session isolation leaks)", got)
	}

	t.Setenv("SF_TEAM_SESSION_ID", "session-B")
	out = captureStdout(t, func() { runWhoami(nil) })
	if got := strings.TrimSpace(out); got != "agent-bravo" {
		t.Fatalf("session-B whoami=%q want agent-bravo", got)
	}
}
