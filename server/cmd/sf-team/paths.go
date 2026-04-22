package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func teamHome() (string, error) {
	if env := os.Getenv("SF_TEAM_HOME"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".switchframe-team"), nil
}

func ensureHome() error {
	home, err := teamHome()
	if err != nil {
		return err
	}
	for _, sub := range []string{"", "archive", "backups"} {
		if err := os.MkdirAll(filepath.Join(home, sub), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", sub, err)
		}
	}
	return nil
}

func dbPath() (string, error) {
	home, err := teamHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "team.db"), nil
}
