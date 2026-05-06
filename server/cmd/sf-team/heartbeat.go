package main

import "context"

func (d db) heartbeat(ctx context.Context, agentID string) (lost []string, err error) {
	now := d.nowUnix()

	rows, err := d.sqldb.QueryContext(ctx, `SELECT item_id FROM claim_history
		WHERE agent_id = ? AND outcome = 'reclaimed' AND released_at > COALESCE(
			(SELECT last_tick_at FROM agents WHERE id = ?), 0)`, agentID, agentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		lost = append(lost, id)
	}
	_ = rows.Close()

	if _, err := d.sqldb.ExecContext(ctx, `
		UPDATE agents SET last_tick_at = ?, status = 'active' WHERE id = ?
	`, now, agentID); err != nil {
		return nil, err
	}
	if _, err := d.sqldb.ExecContext(ctx, `
		UPDATE claims SET last_touch = ? WHERE agent_id = ?
	`, now, agentID); err != nil {
		return nil, err
	}
	return lost, nil
}
