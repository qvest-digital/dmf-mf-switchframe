package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

func (d db) reassign(ctx context.Context, itemID, byAgent, toAgent string) error {
	now := d.nowUnix()
	tx, err := d.sqldb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := d.releaseInTx(ctx, tx, itemID, byAgent, "released"); err != nil {
		return err
	}
	body := fmt.Sprintf("@%s reassigning %s to you", toAgent, itemID)
	if err := d.postMessage(ctx, tx, now, byAgent, "global", "say", body, []string{toAgent}, "normal"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	d.publish("reassign", map[string]any{"item_id": itemID, "from": byAgent, "to": toAgent})
	return nil
}

func runReassign(args []string) int {
	args = reorderFlags(args, nil)
	fs := flag.NewFlagSet("reassign", flag.ExitOnError)
	to := fs.String("to", "", "target agent id (optionally prefixed with @)")
	fs.Parse(args)
	if fs.NArg() != 1 || *to == "" {
		fmt.Fprintln(os.Stderr, "usage: sf-team reassign <ITEM-ID> --to <agent-id>")
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
	target := strings.TrimPrefix(*to, "@")
	if err := d.reassign(ctx, itemID, id, target); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	fmt.Printf("reassigned %s to %s\n", itemID, target)
	return 0
}
