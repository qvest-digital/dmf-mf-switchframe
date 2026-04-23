package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func (d db) reportProgress(ctx context.Context, agentID, itemID string, pct int, note string) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("progress must be 0..100, got %d", pct)
	}
	_, err := d.sqldb.ExecContext(ctx, `
		INSERT INTO progress (item_id, pct, reported_at, reported_by, note)
		VALUES (?, ?, ?, ?, ?)
	`, itemID, pct, d.nowUnix(), agentID, note)
	if err != nil {
		return err
	}
	d.publish("progress", map[string]any{"item_id": itemID, "agent_id": agentID, "pct": pct, "note": note})
	return nil
}

func (d db) latestProgress(ctx context.Context, itemID string) (int, string) {
	var pct int
	var note string
	_ = d.sqldb.QueryRowContext(ctx, `
		SELECT pct, COALESCE(note, '') FROM progress
		WHERE item_id = ? ORDER BY reported_at DESC LIMIT 1
	`, itemID).Scan(&pct, &note)
	return pct, note
}

func runProgress(args []string) int {
	args = reorderFlags(args, nil)
	fs := flag.NewFlagSet("progress", flag.ExitOnError)
	note := fs.String("note", "", "optional note")
	fs.Parse(args)
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: sf-team progress <ITEM-ID> <pct 0..100> [--note ...]")
		return 4
	}
	var pct int
	if _, err := fmt.Sscanf(fs.Arg(1), "%d", &pct); err != nil {
		fmt.Fprintf(os.Stderr, "invalid pct: %v\n", err)
		return 4
	}

	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	if err := d.reportProgress(ctx, id, fs.Arg(0), pct, *note); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	return 0
}
