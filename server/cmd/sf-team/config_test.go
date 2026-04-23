package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_BootstrapsOnFirstRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SF_TEAM_HOME", home)
	_ = ensureHome()

	cfg, err := loadOrInitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token == "" {
		t.Fatal("token not generated")
	}

	data, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("config.json empty")
	}
}

func TestLoadConfig_RespectsExistingToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SF_TEAM_HOME", home)
	_ = ensureHome()
	_ = os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"token":"existing-token"}`), 0o600)

	cfg, err := loadOrInitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "existing-token" {
		t.Fatalf("token=%q", cfg.Token)
	}
}
