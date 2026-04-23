package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var errClaimTaken = errors.New("item already claimed")

func (d db) claim(ctx context.Context, itemID, agentID, intent string, touches []string, long bool) error {
	now := d.nowUnix()
	longVal := 0
	if long {
		longVal = 1
	}

	tx, err := d.sqldb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO claims (item_id, agent_id, claimed_at, last_touch, intent, long)
		VALUES (?, ?, ?, ?, ?, ?)
	`, itemID, agentID, now, now, intent, longVal)
	if err != nil {
		if isPKConflict(err) {
			return errClaimTaken
		}
		return fmt.Errorf("insert claim: %w", err)
	}

	for _, path := range touches {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO touches (agent_id, item_id, path, started_at)
			VALUES (?, ?, ?, ?)
		`, agentID, itemID, path, now); err != nil {
			return fmt.Errorf("insert touch: %w", err)
		}
	}

	body := fmt.Sprintf("claimed %s", itemID)
	if intent != "" {
		body += ": " + intent
	}
	if err := d.postMessage(ctx, tx, now, agentID, "global", "claim", body, nil, "normal"); err != nil {
		return err
	}
	if err := d.postMessage(ctx, tx, now, agentID, itemID, "claim", body, nil, "normal"); err != nil {
		return err
	}

	return tx.Commit()
}

func isPKConflict(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY ||
			sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
	}
	return false
}

func runClaim(args []string) int {
	args = reorderFlags(args, map[string]bool{"long": true})
	fs := flag.NewFlagSet("claim", flag.ExitOnError)
	intent := fs.String("intent", "", "short sentence describing intent")
	touches := fs.String("touches", "", "comma-separated file paths you'll touch")
	long := fs.Bool("long", false, "extended stale threshold (2h)")
	fs.Parse(args)

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: sf-team claim <ITEM-ID> [--intent ...] [--touches a,b] [--long]")
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

	var touchList []string
	if *touches != "" {
		for _, p := range strings.Split(*touches, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				touchList = append(touchList, p)
			}
		}
	}

	if err := d.claim(ctx, itemID, id, *intent, touchList, *long); err != nil {
		if errors.Is(err, errClaimTaken) {
			holder, since := d.claimHolder(ctx, itemID)
			fmt.Fprintf(os.Stderr, "%s is claimed by %s since %s\n", itemID, holder, since)
			return 1
		}
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	fmt.Printf("claimed %s\n", itemID)
	return 0
}

func (d db) claimHolder(ctx context.Context, itemID string) (agent, since string) {
	var ts int64
	_ = d.sqldb.QueryRowContext(ctx, `SELECT agent_id, claimed_at FROM claims WHERE item_id=?`, itemID).Scan(&agent, &ts)
	if ts > 0 {
		since = time.Unix(ts, 0).Format("15:04")
	}
	return
}
