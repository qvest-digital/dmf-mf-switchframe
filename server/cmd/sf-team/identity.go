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
	if worktree == "" {
		return "", errors.New("cannot infer agent id: SF_AGENT unset and worktree empty")
	}
	return filepath.Base(worktree), nil
}

func detectWorktree() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
