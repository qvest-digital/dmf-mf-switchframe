package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func (d db) writeItemStatus(ctx context.Context, w io.Writer, itemID string) error {
	var holder, intent string
	var claimedAt int64
	err := d.sqldb.QueryRowContext(ctx,
		`SELECT agent_id, COALESCE(intent, ''), claimed_at FROM claims WHERE item_id=?`, itemID).
		Scan(&holder, &intent, &claimedAt)
	if err != nil {
		_, _ = fmt.Fprintf(w, "item %s: not currently claimed\n", itemID)
	} else {
		_, _ = fmt.Fprintf(w, "item %s: claimed by %s since %s\n  intent: %s\n",
			itemID, holder, time.Unix(claimedAt, 0).Format("15:04"), intent)
	}

	_, _ = fmt.Fprintf(w, "\nrecent chat in #%s:\n", itemID)
	rows, err := d.sqldb.QueryContext(ctx, `
		SELECT ts, agent_id, kind, COALESCE(body, '') FROM messages
		WHERE thread = ? ORDER BY id DESC LIMIT 20
	`, itemID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ts int64
		var agent, kind, body string
		_ = rows.Scan(&ts, &agent, &kind, &body)
		_, _ = fmt.Fprintf(w, "  [%s] %s (%s): %s\n", time.Unix(ts, 0).Format("15:04"), agent, kind, body)
	}
	return nil
}

func runStatus(args []string) int {
	args = reorderFlags(args, nil)
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	item := fs.String("item", "", "scope to a single item")
	_ = fs.Parse(args)

	ctx, sqldb, _, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	if *item != "" {
		if err := d.writeItemStatus(ctx, os.Stdout, *item); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 4
		}
		return 0
	}
	rows, _ := d.whoList(ctx)
	fmt.Println("active agents:")
	for _, r := range rows {
		fmt.Printf("  %s → %s (%q)\n", r.AgentID, r.ClaimItem, r.Intent)
	}
	fmt.Println()
	fmt.Println("recent global chat:")
	mrows, err := d.sqldb.QueryContext(ctx,
		`SELECT ts, agent_id, kind, COALESCE(body, '') FROM messages WHERE thread='global' ORDER BY id DESC LIMIT 20`)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	defer func() { _ = mrows.Close() }()
	for mrows.Next() {
		var ts int64
		var agent, kind, body string
		_ = mrows.Scan(&ts, &agent, &kind, &body)
		fmt.Printf("  [%s] %s (%s): %s\n", time.Unix(ts, 0).Format("15:04"), agent, kind, body)
	}
	return 0
}
