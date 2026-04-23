package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"
)

func (d db) touch(ctx context.Context, agentID, path, itemID string) (conflicts []string, err error) {
	tx, err := d.sqldb.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT agent_id FROM touches
		WHERE path = ? AND released_at IS NULL AND agent_id != ?
	`, path, agentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return nil, err
		}
		conflicts = append(conflicts, a)
	}
	rows.Close()

	var itemSQL sql.NullString
	if itemID != "" {
		itemSQL = sql.NullString{String: itemID, Valid: true}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO touches (agent_id, item_id, path, started_at) VALUES (?, ?, ?, ?)
	`, agentID, itemSQL, path, d.nowUnix()); err != nil {
		return nil, err
	}

	if len(conflicts) > 0 {
		body := fmt.Sprintf("also touching %s (%s already has it)", path, conflicts[0])
		if err := d.postMessage(ctx, tx, d.nowUnix(), agentID, "global", "heads_up", body, conflicts, "normal"); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return conflicts, nil
}

func (d db) untouch(ctx context.Context, agentID, path string) error {
	_, err := d.sqldb.ExecContext(ctx, `
		UPDATE touches SET released_at = ?
		WHERE agent_id = ? AND path = ? AND released_at IS NULL
	`, d.nowUnix(), agentID, path)
	return err
}

func runTouch(args []string) int {
	fs := flag.NewFlagSet("touch", flag.ExitOnError)
	item := fs.String("item", "", "item id to associate")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: sf-team touch <path> [--item ITEM]")
		return 4
	}
	path := fs.Arg(0)

	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)

	conflicts, err := d.touch(ctx, id, path, *item)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	if len(conflicts) > 0 {
		fmt.Printf("WARNING: also touched by: %s\n", strings.Join(conflicts, ", "))
	}
	fmt.Printf("touched %s\n", path)
	return 0
}

func runUntouch(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: sf-team untouch <path>")
		return 4
	}
	path := args[0]
	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	if err := d.untouch(ctx, id, path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	return 0
}
