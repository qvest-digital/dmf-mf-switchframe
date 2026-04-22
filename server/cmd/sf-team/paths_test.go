package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTeamHome_UsesEnvOverride(t *testing.T) {
	t.Setenv("SF_TEAM_HOME", "/tmp/sf-team-test-override")
	got, err := teamHome()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/sf-team-test-override" {
		t.Fatalf("got %q", got)
	}
}

func TestTeamHome_DefaultsToHomeDotDir(t *testing.T) {
	t.Setenv("SF_TEAM_HOME", "")
	home, _ := os.UserHomeDir()
	got, err := teamHome()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".switchframe-team")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEnsureHome_CreatesDirAndSubdirs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SF_TEAM_HOME", filepath.Join(tmp, "home"))
	if err := ensureHome(); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"archive", "backups"} {
		path := filepath.Join(tmp, "home", sub)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", sub, err)
		}
	}
}

func TestDBPath_UnderTeamHome(t *testing.T) {
	t.Setenv("SF_TEAM_HOME", "/tmp/sf-home")
	got, err := dbPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/sf-home/team.db" {
		t.Fatalf("got %q", got)
	}
}
