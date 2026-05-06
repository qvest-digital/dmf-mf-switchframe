package main

import (
	"context"
	"strings"
)

type digestMessage struct {
	ID       int64
	TS       int64
	Agent    string
	Thread   string
	Kind     string
	Body     string
	Priority string
}

type digest struct {
	Agent         string
	NowTS         int64
	Knocks        []digestMessage
	Mentions      []digestMessage
	YourThreads   []digestMessage
	Global        []digestMessage
	ActiveClaims  []digestMessage
	FileConflicts []string
	StaleClaims   []string
	LostClaims    []string
}

func (d db) digestFor(ctx context.Context, agentID string) (digest, error) {
	dg := digest{Agent: agentID, NowTS: d.nowUnix()}

	unread := func(thread string) ([]digestMessage, error) {
		rows, err := d.sqldb.QueryContext(ctx, `
			SELECT m.id, m.ts, m.agent_id, m.thread, m.kind, COALESCE(m.body, ''), m.priority
			FROM messages m
			LEFT JOIN reads r ON r.agent_id = ? AND r.thread = m.thread
			WHERE m.thread = ? AND m.id > COALESCE(r.last_msg_id, 0) AND m.agent_id != ?
			ORDER BY m.id
		`, agentID, thread, agentID)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var out []digestMessage
		for rows.Next() {
			var m digestMessage
			if err := rows.Scan(&m.ID, &m.TS, &m.Agent, &m.Thread, &m.Kind, &m.Body, &m.Priority); err != nil {
				return nil, err
			}
			out = append(out, m)
		}
		return out, nil
	}

	globalMsgs, err := unread("global")
	if err != nil {
		return dg, err
	}
	for _, m := range globalMsgs {
		if m.Kind == "knock" && m.Priority == "high" {
			dg.Knocks = append(dg.Knocks, m)
			continue
		}
		if strings.Contains(m.Body, "@"+agentID) {
			dg.Mentions = append(dg.Mentions, m)
			continue
		}
		dg.Global = append(dg.Global, m)
	}

	rows, err := d.sqldb.QueryContext(ctx,
		`SELECT item_id FROM claims WHERE agent_id = ?`, agentID)
	if err != nil {
		return dg, err
	}
	var myItems []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return dg, err
		}
		myItems = append(myItems, id)
	}
	_ = rows.Close()
	for _, item := range myItems {
		msgs, err := unread(item)
		if err != nil {
			return dg, err
		}
		dg.YourThreads = append(dg.YourThreads, msgs...)
	}

	claimRows, err := d.sqldb.QueryContext(ctx,
		`SELECT item_id, agent_id, claimed_at, COALESCE(intent, '') FROM claims ORDER BY claimed_at`)
	if err != nil {
		return dg, err
	}
	for claimRows.Next() {
		var m digestMessage
		var intent string
		if err := claimRows.Scan(&m.Thread, &m.Agent, &m.TS, &intent); err != nil {
			_ = claimRows.Close()
			return dg, err
		}
		m.Body = intent
		m.Kind = "claim"
		dg.ActiveClaims = append(dg.ActiveClaims, m)
	}
	_ = claimRows.Close()

	confRows, err := d.sqldb.QueryContext(ctx, `
		SELECT path FROM touches WHERE released_at IS NULL
		GROUP BY path HAVING COUNT(DISTINCT agent_id) > 1`)
	if err != nil {
		return dg, err
	}
	for confRows.Next() {
		var p string
		if err := confRows.Scan(&p); err != nil {
			_ = confRows.Close()
			return dg, err
		}
		dg.FileConflicts = append(dg.FileConflicts, p)
	}
	_ = confRows.Close()

	return dg, nil
}
