package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

func (d db) doctor(ctx context.Context) ([]string, error) {
	var findings []string
	now := d.nowUnix()

	rows, err := d.sqldb.QueryContext(ctx, `
		SELECT item_id, agent_id, last_touch, long FROM claims
		WHERE (long = 0 AND last_touch < ?) OR (long = 1 AND last_touch < ?)
	`, now-staleThresholdSec, now-staleThresholdLongSec)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item, agent string
		var lt, long int64
		_ = rows.Scan(&item, &agent, &lt, &long)
		findings = append(findings, fmt.Sprintf("stale claim: %s (agent=%s, last_touch=%s)",
			item, agent, time.Unix(lt, 0).Format("15:04")))
	}
	_ = rows.Close()

	orphRows, err := d.sqldb.QueryContext(ctx, `
		SELECT t.agent_id, t.path, t.item_id FROM touches t
		LEFT JOIN claims c ON c.item_id = t.item_id AND c.agent_id = t.agent_id
		WHERE t.released_at IS NULL AND t.item_id IS NOT NULL AND c.item_id IS NULL
	`)
	if err != nil {
		return nil, err
	}
	for orphRows.Next() {
		var a, p, i string
		_ = orphRows.Scan(&a, &p, &i)
		findings = append(findings, fmt.Sprintf("orphan touch: %s by %s (item %s not claimed)", p, a, i))
	}
	_ = orphRows.Close()

	staleAg, err := d.sqldb.QueryContext(ctx,
		`SELECT id, last_tick_at FROM agents WHERE status='active' AND last_tick_at < ?`, now-3600)
	if err != nil {
		return nil, err
	}
	for staleAg.Next() {
		var id string
		var ts int64
		_ = staleAg.Scan(&id, &ts)
		findings = append(findings, fmt.Sprintf("ghost agent: %s (last tick %s, marked active)",
			id, time.Unix(ts, 0).Format("15:04")))
	}
	_ = staleAg.Close()

	var ic string
	if err := d.sqldb.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&ic); err != nil {
		findings = append(findings, "integrity_check failed: "+err.Error())
	} else if ic != "ok" {
		findings = append(findings, "integrity_check: "+ic)
	}

	return findings, nil
}

func runDoctor(args []string) int {
	ctx, sqldb, _, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	findings, err := d.doctor(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	if len(findings) == 0 {
		fmt.Println("doctor: all clear")
		return 0
	}
	fmt.Printf("doctor: %d finding(s):\n", len(findings))
	for _, f := range findings {
		fmt.Printf("  - %s\n", f)
	}
	return 0
}
