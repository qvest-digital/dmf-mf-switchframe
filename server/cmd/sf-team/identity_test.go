package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentID_PrefersEnvVar(t *testing.T) {
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
	t.Setenv("SF_AGENT", "")
	if _, err := agentID(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}
