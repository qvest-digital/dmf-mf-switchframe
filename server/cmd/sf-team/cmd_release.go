package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
)

var (
	errNotYours   = errors.New("claim held by another agent")
	errNotClaimed = errors.New("no active claim")
)

func (d db) release(ctx context.Context, itemID, agentID, outcome string) error {
	if outcome == "" {
		outcome = "released"
	}
	tx, err := d.sqldb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := d.releaseInTx(ctx, tx, itemID, agentID, outcome); err != nil {
		return err
	}
	return tx.Commit()
}

func (d db) releaseInTx(ctx context.Context, tx *sql.Tx, itemID, agentID, outcome string) error {
	now := d.nowUnix()

	var holder string
	var claimedAt int64
	row := tx.QueryRowContext(ctx, `SELECT agent_id, claimed_at FROM claims WHERE item_id=?`, itemID)
	if err := row.Scan(&holder, &claimedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errNotClaimed
		}
		return fmt.Errorf("lookup claim: %w", err)
	}
	if holder != agentID {
		return errNotYours
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO claim_history (item_id, agent_id, claimed_at, released_at, outcome)
		VALUES (?, ?, ?, ?, ?)
	`, itemID, agentID, claimedAt, now, outcome); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM claims WHERE item_id=?`, itemID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE touches SET released_at = ?
		WHERE item_id = ? AND agent_id = ? AND released_at IS NULL
	`, now, itemID, agentID); err != nil {
		return err
	}

	body := fmt.Sprintf("released %s (%s)", itemID, outcome)
	if err := d.postMessage(ctx, tx, now, agentID, "global", "release", body, nil, "normal"); err != nil {
		return err
	}
	if err := d.postMessage(ctx, tx, now, agentID, itemID, "release", body, nil, "normal"); err != nil {
		return err
	}
	return nil
}

func runRelease(args []string) int {
	args = reorderFlags(args, nil)
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	outcome := fs.String("outcome", "released", "done | blocked | released")
	fs.Parse(args)

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: sf-team release <ITEM-ID> [--outcome ...]")
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

	if err := d.release(ctx, itemID, id, *outcome); err != nil {
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
	fmt.Printf("released %s (%s)\n", itemID, *outcome)
	return 0
}
