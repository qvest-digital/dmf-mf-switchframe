package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (d db) archive(ctx context.Context, beforeUnix int64) (int, string, error) {
	home, err := teamHome()
	if err != nil {
		return 0, "", err
	}
	month := time.Unix(beforeUnix, 0).Format("2006-01")
	archivePath := filepath.Join(home, "archive", "team-"+month+".db")

	adb, err := openDB(archivePath)
	if err != nil {
		return 0, "", fmt.Errorf("open archive: %w", err)
	}
	defer adb.Close()

	rows, err := d.sqldb.QueryContext(ctx, `
		SELECT id, ts, agent_id, thread, kind, COALESCE(body, ''), COALESCE(mentions, '[]'), priority
		FROM messages WHERE ts < ?
	`, beforeUnix)
	if err != nil {
		return 0, "", err
	}
	type row struct {
		ID       int64
		TS       int64
		Agent    string
		Thread   string
		Kind     string
		Body     string
		Mentions string
		Priority string
	}
	var batch []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.TS, &r.Agent, &r.Thread, &r.Kind, &r.Body, &r.Mentions, &r.Priority); err != nil {
			rows.Close()
			return 0, "", err
		}
		batch = append(batch, r)
	}
	rows.Close()

	tx, err := adb.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", err
	}
	for _, r := range batch {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO messages (ts, agent_id, thread, kind, body, mentions, priority)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, r.TS, r.Agent, r.Thread, r.Kind, r.Body, r.Mentions, r.Priority); err != nil {
			tx.Rollback()
			return 0, "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, "", err
	}

	if _, err := d.sqldb.ExecContext(ctx, `DELETE FROM messages WHERE ts < ?`, beforeUnix); err != nil {
		return 0, "", err
	}
	return len(batch), archivePath, nil
}

func runArchive(args []string) int {
	fs := flag.NewFlagSet("archive", flag.ExitOnError)
	before := fs.String("before", "30d", "cut-off duration (e.g., 30d, 720h)")
	fs.Parse(args)

	var dur time.Duration
	var err error
	if dur, err = time.ParseDuration(*before); err != nil {
		var days int
		if _, err2 := fmt.Sscanf(*before, "%dd", &days); err2 == nil {
			dur = time.Duration(days) * 24 * time.Hour
		} else {
			fmt.Fprintln(os.Stderr, "invalid --before:", err)
			return 4
		}
	}
	cutoff := time.Now().Add(-dur).Unix()

	ctx, sqldb, _, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	n, path, err := d.archive(ctx, cutoff)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	fmt.Printf("archived %d messages to %s\n", n, path)
	return 0
}
