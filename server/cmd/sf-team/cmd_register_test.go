package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func newTestDB(t *testing.T) (db, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "team.db")
	sqldb, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	return newDB(sqldb), path
}

func TestRegister_InsertsAgent(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()

	if err := d.register(ctx, "agent-a", "Agent A", "/tmp/sf-agent-a", 1234); err != nil {
		t.Fatalf("register: %v", err)
	}

	var name string
	err := d.sqldb.QueryRow("SELECT display_name FROM agents WHERE id=?", "agent-a").Scan(&name)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if name != "Agent A" {
		t.Fatalf("got %q want Agent A", name)
	}
}

func TestRegister_UpsertsOnRepeat(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()

	if err := d.register(ctx, "agent-a", "Agent A", "/tmp/a", 1); err != nil {
		t.Fatal(err)
	}
	if err := d.register(ctx, "agent-a", "Agent A v2", "/tmp/a", 2); err != nil {
		t.Fatal(err)
	}

	var name string
	var pid int64
	err := d.sqldb.QueryRow("SELECT display_name, pid FROM agents WHERE id=?", "agent-a").Scan(&name, &pid)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Agent A v2" || pid != 2 {
		t.Fatalf("got %q/%d", name, pid)
	}
}

func TestRegister_EmitsSystemMessage(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()

	if err := d.register(ctx, "agent-a", "A", "/tmp/a", 1); err != nil {
		t.Fatal(err)
	}

	var count int
	err := d.sqldb.QueryRow("SELECT COUNT(*) FROM messages WHERE kind='system' AND agent_id='agent-a'").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 system message, got %d", count)
	}

	var thread, body string
	err = d.sqldb.QueryRow(`SELECT thread, body FROM messages WHERE kind='system' AND agent_id='agent-a'`).Scan(&thread, &body)
	if err != nil {
		t.Fatal(err)
	}
	if thread != "global" {
		t.Fatalf("thread=%q want global", thread)
	}
	if !strings.Contains(body, "agent-a") || !strings.Contains(body, "/tmp/a") {
		t.Fatalf("body=%q missing id or worktree", body)
	}
}

func TestRegister_RollbackLeavesNoTraces(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()

	d.sqldb.Close()

	if err := d.register(ctx, "agent-a", "A", "/tmp/a", 1); err == nil {
		t.Fatal("expected register to fail against a closed DB")
	}
}
