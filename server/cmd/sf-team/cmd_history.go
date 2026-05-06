package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

type historyEntry struct {
	TS    int64
	Agent string
	Kind  string
	Body  string
}

func (d db) historyFor(ctx context.Context, itemID string) ([]historyEntry, error) {
	rows, err := d.sqldb.QueryContext(ctx, `
		SELECT ts, agent_id, kind, COALESCE(body, '') FROM messages
		WHERE thread = ? ORDER BY ts
	`, itemID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []historyEntry
	for rows.Next() {
		var e historyEntry
		if err := rows.Scan(&e.TS, &e.Agent, &e.Kind, &e.Body); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func runHistory(args []string) int {
	args = reorderFlags(args, nil)
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: sf-team history <ITEM-ID>")
		return 4
	}
	itemID := args[0]
	ctx, sqldb, _, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	entries, err := d.historyFor(ctx, itemID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	fmt.Printf("history for %s:\n", itemID)
	for _, e := range entries {
		fmt.Printf("  [%s] %s (%s): %s\n",
			time.Unix(e.TS, 0).Format("2006-01-02 15:04"), e.Agent, e.Kind, e.Body)
	}
	return 0
}
