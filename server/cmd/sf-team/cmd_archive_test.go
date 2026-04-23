package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestArchive_MovesOldMessages(t *testing.T) {
	t.Setenv("SF_TEAM_HOME", t.TempDir())
	_ = ensureHome()
	path, _ := dbPath()
	sqldb, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	d := newDB(sqldb)
	ctx := context.Background()

	t0 := time.Unix(0, 0).Add(60 * 24 * time.Hour) // far future base
	d.now = func() time.Time { return t0 }
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	// old message
	d.now = func() time.Time { return t0.Add(-40 * 24 * time.Hour) }
	_ = d.say(ctx, "agent-a", "global", "say", "old msg", nil, "normal")

	// recent message
	d.now = func() time.Time { return t0 }
	_ = d.say(ctx, "agent-a", "global", "say", "recent", nil, "normal")

	moved, archivePath, err := d.archive(ctx, t0.Add(-30*24*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if moved < 1 {
		t.Fatalf("moved=%d want >=1", moved)
	}
	if _, err := filepath.Abs(archivePath); err != nil {
		t.Fatal(err)
	}

	var c int
	sqldb.QueryRow(`SELECT COUNT(*) FROM messages WHERE body='old msg'`).Scan(&c)
	if c != 0 {
		t.Fatalf("old messages still present")
	}
}
