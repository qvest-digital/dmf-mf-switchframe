package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func (d db) register(ctx context.Context, id, name, worktree string, pid int) error {
	now := d.nowUnix()
	tx, err := d.sqldb.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agents (id, display_name, worktree, pid, started_at, last_tick_at, status)
		VALUES (?, ?, ?, ?, ?, ?, 'active')
		ON CONFLICT(id) DO UPDATE SET
			display_name = excluded.display_name,
			worktree     = excluded.worktree,
			pid          = excluded.pid,
			last_tick_at = excluded.last_tick_at,
			status       = 'active'
	`, id, name, worktree, pid, now, now)
	if err != nil {
		return fmt.Errorf("upsert agent: %w", err)
	}

	body := fmt.Sprintf("%s joined (worktree: %s)", id, worktree)
	if err := d.postMessage(ctx, tx, now, id, "global", "system", body, nil, "normal"); err != nil {
		return fmt.Errorf("emit join msg: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	d.publish("agent", map[string]any{"agent_id": id, "kind": "joined"})
	return nil
}

func runRegister(args []string) int {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	asFlag := fs.String("as", "", "agent id override")
	nameFlag := fs.String("name", "", "display name")
	_ = fs.Parse(args)

	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}

	if *asFlag != "" {
		id = *asFlag
		if err := writePersistedAgentID(id); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 4
		}
	}
	name := *nameFlag
	if name == "" {
		name = id
	}

	d := newDB(sqldb)
	if err := d.register(ctx, id, name, detectWorktree(), os.Getpid()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	fmt.Printf("registered %s\n", id)
	return 0
}
