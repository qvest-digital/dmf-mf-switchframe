package main

import (
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

func readPersistedAgentID() string {
	home, err := teamHome()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, "agent-id.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writePersistedAgentID(id string) error {
	home, err := teamHome()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, "agent-id.txt"), []byte(id+"\n"), 0o644)
}

func detectWorktree() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
