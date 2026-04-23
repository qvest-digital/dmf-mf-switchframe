package main

import (
	"context"
	"fmt"
)

const (
	staleThresholdSec     = 30 * 60
	staleThresholdLongSec = 2 * 60 * 60
)

func (d db) reclaimStale(ctx context.Context) ([]string, error) {
	now := d.nowUnix()
	tx, err := d.sqldb.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT item_id, agent_id, claimed_at, last_touch, long FROM claims
		WHERE (long = 0 AND last_touch < ?) OR (long = 1 AND last_touch < ?)
	`, now-staleThresholdSec, now-staleThresholdLongSec)
	if err != nil {
		return nil, err
	}

	type staleClaim struct {
		item, agent string
		claimedAt   int64
	}
	var stales []staleClaim
	for rows.Next() {
		var s staleClaim
		var lt, long int64
		if err := rows.Scan(&s.item, &s.agent, &s.claimedAt, &lt, &long); err != nil {
			rows.Close()
			return nil, err
		}
		stales = append(stales, s)
	}
	rows.Close()

	ids := make([]string, 0, len(stales))
	for _, s := range stales {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO claim_history (item_id, agent_id, claimed_at, released_at, outcome)
			VALUES (?, ?, ?, ?, 'reclaimed')
		`, s.item, s.agent, s.claimedAt, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM claims WHERE item_id=?`, s.item); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE touches SET released_at = ? WHERE item_id = ? AND agent_id = ? AND released_at IS NULL
		`, now, s.item, s.agent); err != nil {
			return nil, err
		}
		body := fmt.Sprintf("reclaimed %s from %s (stale)", s.item, s.agent)
		if err := d.postMessage(ctx, tx, now, s.agent, "global", "system", body, nil, "normal"); err != nil {
			return nil, err
		}
		ids = append(ids, s.item)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}
