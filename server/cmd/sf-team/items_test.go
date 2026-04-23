package main

import (
	"path/filepath"
	"testing"
)

func TestParseItem_Frontmatter(t *testing.T) {
	item, err := parseItemFile(filepath.Join("testdata", "items", "BUG-TEST-001.md"))
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "BUG-TEST-001" {
		t.Fatalf("id=%q", item.ID)
	}
	if item.Priority != "P1" {
		t.Fatalf("priority=%q", item.Priority)
	}
}

func TestParseItem_ACProgress(t *testing.T) {
	item, _ := parseItemFile(filepath.Join("testdata", "items", "BUG-TEST-001.md"))
	if item.ACTotal != 4 || item.ACChecked != 2 {
		t.Fatalf("ac=%d/%d want 2/4", item.ACChecked, item.ACTotal)
	}
	if item.ProgressPct() != 50 {
		t.Fatalf("pct=%d", item.ProgressPct())
	}
}
