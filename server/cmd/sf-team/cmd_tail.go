package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func (d db) tailOneShot(ctx context.Context, w io.Writer, thread string, sinceUnix int64, kinds []string) error {
	q := `SELECT id, ts, agent_id, thread, kind, COALESCE(body, '') FROM messages WHERE ts >= ?`
	args := []interface{}{sinceUnix}
	if thread != "" && thread != "all" {
		q += ` AND thread = ?`
		args = append(args, thread)
	}
	if len(kinds) > 0 {
		q += ` AND kind IN (?` + strings.Repeat(",?", len(kinds)-1) + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	q += ` ORDER BY id`
	rows, err := d.sqldb.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, ts int64
		var agent, th, kind, body string
		_ = rows.Scan(&id, &ts, &agent, &th, &kind, &body)
		_, _ = fmt.Fprintf(w, "%s  %-10s  %-10s  %s  %s\n",
			time.Unix(ts, 0).Format("15:04:05"), agent, kind, threadLabel(th), body)
	}
	return nil
}

func threadLabel(t string) string {
	if t == "global" {
		return "→ #global"
	}
	return "→ #" + t
}

func parseSince(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil {
			return time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix(), nil
		}
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	return time.Now().Add(-dur).Unix(), nil
}

func runTail(args []string) int {
	args = reorderFlags(args, map[string]bool{"follow": true})
	fs := flag.NewFlagSet("tail", flag.ExitOnError)
	thread := fs.String("thread", "all", "'global', '<ITEM>', or 'all'")
	follow := fs.Bool("follow", false, "stream new messages")
	since := fs.String("since", "30m", "only messages newer than duration")
	kindsFlag := fs.String("kind", "", "comma-separated kinds to filter")
	_ = fs.Parse(args)

	sinceUnix, err := parseSince(*since)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	var kinds []string
	if *kindsFlag != "" {
		kinds = strings.Split(*kindsFlag, ",")
	}

	ctx, sqldb, _, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)

	if err := d.tailOneShot(ctx, os.Stdout, *thread, sinceUnix, kinds); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}

	if !*follow {
		return 0
	}

	var lastID int64
	_ = d.sqldb.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM messages`).Scan(&lastID)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		q := `SELECT id, ts, agent_id, thread, kind, COALESCE(body, '') FROM messages WHERE id > ?`
		qArgs := []interface{}{lastID}
		if *thread != "" && *thread != "all" {
			q += ` AND thread = ?`
			qArgs = append(qArgs, *thread)
		}
		if len(kinds) > 0 {
			q += ` AND kind IN (?` + strings.Repeat(",?", len(kinds)-1) + `)`
			for _, k := range kinds {
				qArgs = append(qArgs, k)
			}
		}
		q += ` ORDER BY id`
		rows, err := d.sqldb.QueryContext(ctx, q, qArgs...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id, ts int64
			var agent, th, kind, body string
			_ = rows.Scan(&id, &ts, &agent, &th, &kind, &body)
			fmt.Printf("%s  %-10s  %-10s  %s  %s\n",
				time.Unix(ts, 0).Format("15:04:05"), agent, kind, threadLabel(th), body)
			if id > lastID {
				lastID = id
			}
		}
		_ = rows.Close()
	}
	return 0
}
