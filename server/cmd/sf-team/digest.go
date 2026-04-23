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
		defer rows.Close()
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
			rows.Close()
			return dg, err
		}
		myItems = append(myItems, id)
	}
	rows.Close()
	for _, item := range myItems {
		msgs, err := unread(item)
		if err != nil {
			return dg, err
		}
		dg.YourThreads = append(dg.YourThreads, msgs...)
	}

	return dg, nil
}
