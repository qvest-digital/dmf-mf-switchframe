package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
)

func (d db) done(ctx context.Context, itemID, agentID, summary string) error {
	now := d.nowUnix()
	tx, err := d.sqldb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	body := fmt.Sprintf("done %s", itemID)
	if summary != "" {
		body += ": " + summary
	}
	if err := d.postMessage(ctx, tx, now, agentID, "global", "done", body, nil, "normal"); err != nil {
		return err
	}
	if err := d.postMessage(ctx, tx, now, agentID, itemID, "done", body, nil, "normal"); err != nil {
		return err
	}
	if err := d.releaseInTx(ctx, tx, itemID, agentID, "done"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	d.publish("done", map[string]any{"item_id": itemID, "agent_id": agentID, "summary": summary})
	return nil
}

func (d db) blocked(ctx context.Context, itemID, agentID, reason string) error {
	now := d.nowUnix()
	tx, err := d.sqldb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	body := fmt.Sprintf("blocked on %s", itemID)
	if reason != "" {
		body += ": " + reason
	}
	if err := d.postMessage(ctx, tx, now, agentID, "global", "blocked", body, nil, "normal"); err != nil {
		return err
	}
	if err := d.postMessage(ctx, tx, now, agentID, itemID, "blocked", body, nil, "normal"); err != nil {
		return err
	}
	if err := d.releaseInTx(ctx, tx, itemID, agentID, "blocked"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	d.publish("blocked", map[string]any{"item_id": itemID, "agent_id": agentID, "reason": reason})
	return nil
}

func runDone(args []string) int {
	args = reorderFlags(args, nil)
	fs := flag.NewFlagSet("done", flag.ExitOnError)
	summary := fs.String("summary", "", "one-line summary")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: sf-team done <ITEM-ID> [--summary ...]")
		return 4
	}
	itemID := fs.Arg(0)
	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	if err := d.done(ctx, itemID, id, *summary); err != nil {
		if errors.Is(err, errNotYours) {
			fmt.Fprintln(os.Stderr, "not your claim")
			return 1
		}
		if errors.Is(err, errNotClaimed) {
			fmt.Fprintf(os.Stderr, "no active claim on %s\n", itemID)
			return 1
		}
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	fmt.Printf("done %s\n", itemID)
	return 0
}

func runBlocked(args []string) int {
	args = reorderFlags(args, nil)
	fs := flag.NewFlagSet("blocked", flag.ExitOnError)
	reason := fs.String("reason", "", "blocker description")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: sf-team blocked <ITEM-ID> [--reason ...]")
		return 4
	}
	itemID := fs.Arg(0)
	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	if err := d.blocked(ctx, itemID, id, *reason); err != nil {
		if errors.Is(err, errNotYours) {
			fmt.Fprintln(os.Stderr, "not your claim")
			return 1
		}
		if errors.Is(err, errNotClaimed) {
			fmt.Fprintf(os.Stderr, "no active claim on %s\n", itemID)
			return 1
		}
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	fmt.Printf("blocked %s\n", itemID)
	return 0
}
