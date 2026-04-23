package main

import (
	"context"
	"testing"
)

func TestTouch_WarnsOnExistingActive(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)
	_ = d.register(ctx, "agent-b", "B", "/tmp/b", 2)

	if _, err := d.touch(ctx, "agent-a", "server/x.go", ""); err != nil {
		t.Fatal(err)
	}

	conflicts, err := d.touch(ctx, "agent-b", "server/x.go", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0] != "agent-a" {
		t.Fatalf("conflicts=%v want [agent-a]", conflicts)
	}
}

func TestUntouch_MarksReleased(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()
	_ = d.register(ctx, "agent-a", "A", "/tmp/a", 1)

	if _, err := d.touch(ctx, "agent-a", "server/y.go", ""); err != nil {
		t.Fatal(err)
	}
	if err := d.untouch(ctx, "agent-a", "server/y.go"); err != nil {
		t.Fatal(err)
	}
	var active int
	d.sqldb.QueryRow(`SELECT COUNT(*) FROM touches WHERE path='server/y.go' AND released_at IS NULL`).Scan(&active)
	if active != 0 {
		t.Fatalf("still active=%d", active)
	}
}
