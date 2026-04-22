package main

import (
	"path/filepath"
	"testing"
)

func TestOpenDB_CreatesSchemaOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "team.db")

	db, err := openDB(path)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	tables := []string{"agents", "claims", "claim_history", "messages", "touches", "reads", "progress"}
	for _, table := range tables {
		var name string
		row := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table)
		if err := row.Scan(&name); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestOpenDB_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "team.db")

	for i := 0; i < 3; i++ {
		db, err := openDB(path)
		if err != nil {
			t.Fatalf("openDB iteration %d: %v", i, err)
		}
		db.Close()
	}
}

func TestOpenDB_EnablesWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "team.db")

	db, err := openDB(path)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("expected WAL mode, got %q", mode)
	}
}
