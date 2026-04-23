package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"
)

type whoRow struct {
	AgentID     string
	DisplayName string
	Worktree    string
	LastTick    int64
	ClaimItem   string
	Intent      string
	TouchCount  int
	Status      string
}

func (d db) whoList(ctx context.Context) ([]whoRow, error) {
	rows, err := d.sqldb.QueryContext(ctx, `
		SELECT a.id, a.display_name, COALESCE(a.worktree, ''), a.last_tick_at, a.status,
		       COALESCE(c.item_id, ''), COALESCE(c.intent, ''),
		       (SELECT COUNT(*) FROM touches t WHERE t.agent_id = a.id AND t.released_at IS NULL) AS tc
		FROM agents a
		LEFT JOIN claims c ON c.agent_id = a.id
		ORDER BY a.last_tick_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []whoRow
	for rows.Next() {
		var r whoRow
		var item, intent sql.NullString
		if err := rows.Scan(&r.AgentID, &r.DisplayName, &r.Worktree, &r.LastTick, &r.Status,
			&item, &intent, &r.TouchCount); err != nil {
			return nil, err
		}
		r.ClaimItem = item.String
		r.Intent = intent.String
		out = append(out, r)
	}
	return out, nil
}

func runWho(args []string) int {
	fs := flag.NewFlagSet("who", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	fs.Parse(args)

	ctx, sqldb, _, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	rows, err := d.whoList(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rows)
		return 0
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()
	fmt.Fprintln(w, "AGENT\tNAME\tCLAIM\tINTENT\tFILES\tTICK\tSTATUS")
	for _, r := range rows {
		tick := time.Unix(r.LastTick, 0).Format("15:04")
		intent := r.Intent
		if len(intent) > 40 {
			intent = intent[:40]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			r.AgentID, r.DisplayName, r.ClaimItem, intent, r.TouchCount, tick, r.Status)
	}
	return 0
}
