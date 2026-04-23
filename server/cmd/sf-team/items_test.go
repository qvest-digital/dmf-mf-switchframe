package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestParseItemFile_ACAndBodyAndDeps(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "FEAT-999.md")
	content := `---
id: FEAT-999
title: demo
priority: P1
status: open
blocked-by: [BUG-100]
relates-to: [DEBT-001, DEBT-002]
---

## Problem

Demo problem.

## Acceptance criteria

- [x] First done
- [ ] Second todo
  - sub-bullet one
  - sub-bullet two
- [X] Third capital-X

## Notes

Trailing notes.
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	it, err := parseItemFile(p)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if it.BlockedBy[0] != "BUG-100" || len(it.RelatesTo) != 2 || it.RelatesTo[1] != "DEBT-002" {
		t.Fatalf("deps: blocked_by=%v relates_to=%v", it.BlockedBy, it.RelatesTo)
	}
	if it.ACTotal != 3 || it.ACChecked != 2 {
		t.Fatalf("counts: %d/%d", it.ACChecked, it.ACTotal)
	}
	if len(it.ACItems) != 3 {
		t.Fatalf("ac items len=%d", len(it.ACItems))
	}
	if !it.ACItems[0].Checked || it.ACItems[0].Text != "First done" {
		t.Fatalf("ac[0]=%+v", it.ACItems[0])
	}
	if it.ACItems[1].Checked || !strings.Contains(it.ACItems[1].Text, "sub-bullet one") {
		t.Fatalf("ac[1]=%+v", it.ACItems[1])
	}
	if !it.ACItems[2].Checked || it.ACItems[2].Text != "Third capital-X" {
		t.Fatalf("ac[2]=%+v", it.ACItems[2])
	}
	if !strings.HasPrefix(it.Body, "\n## Problem") {
		t.Fatalf("body prefix=%q", it.Body[:min(40, len(it.Body))])
	}
	if !strings.Contains(it.Body, "## Notes") {
		t.Fatal("body missing Notes")
	}
}
