package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"
)

var errNoUnclaimed = errors.New("no unclaimed items")

func (d db) nextUnclaimed(ctx context.Context, itemsDir string) (item, error) {
	items, err := walkItems(itemsDir)
	if err != nil {
		return item{}, err
	}

	claimed := map[string]bool{}
	rows, err := d.sqldb.QueryContext(ctx, `SELECT item_id FROM claims`)
	if err != nil {
		return item{}, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return item{}, err
		}
		claimed[id] = true
	}
	_ = rows.Close()

	priorityScore := map[string]int{"P0": 0, "P1": 1, "P2": 2, "P3": 3}
	bestScore := 999
	var best item
	found := false
	now := time.Now().UTC()
	for _, it := range items {
		if claimed[it.ID] || it.Status == "done" || it.Status == "blocked" {
			continue
		}
		if gatedUntil(it, now) {
			continue
		}
		s, ok := priorityScore[it.Priority]
		if !ok {
			s = 3
		}
		if s < bestScore || (s == bestScore && !found) {
			bestScore = s
			best = it
			found = true
		}
	}
	if !found {
		return item{}, errNoUnclaimed
	}
	return best, nil
}

// gatedUntil reports whether the item has a `not-before:` frontmatter value
// that is still in the future. An unset or unparseable value is not a gate —
// silent no-op rather than hiding items due to a bad date string.
func gatedUntil(it item, now time.Time) bool {
	if it.NotBefore == "" {
		return false
	}
	t, err := time.Parse("2006-01-02", it.NotBefore)
	if err != nil {
		return false
	}
	return now.Before(t)
}

func runNext(args []string) int {
	args = reorderFlags(args, map[string]bool{"id-only": true, "json": true})
	fs := flag.NewFlagSet("next", flag.ExitOnError)
	itemsDir := fs.String("items", "docs/backlog/items", "items directory")
	idOnly := fs.Bool("id-only", false, "print only the ID (for scripts)")
	jsonOut := fs.Bool("json", false, "emit JSON")
	_ = fs.Parse(args)

	ctx, sqldb, _, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)

	it, err := d.nextUnclaimed(ctx, *itemsDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *idOnly {
		fmt.Println(it.ID)
		return 0
	}
	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"id": it.ID, "priority": it.Priority, "title": it.Title, "estimate": it.Estimate,
		})
		return 0
	}
	fmt.Printf("%s  %s  (%s)  %s\n", it.ID, it.Priority, it.Estimate, it.Title)
	fmt.Printf("claim with:  sf-team claim %s --intent \"...\"\n", it.ID)
	return 0
}
