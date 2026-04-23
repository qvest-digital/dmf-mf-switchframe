package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type db struct {
	sqldb     *sql.DB
	now       func() time.Time
	publisher publisherIface
}

type publisherIface interface {
	Publish(e event)
}

func newDB(sqldb *sql.DB) db {
	return db{sqldb: sqldb, now: time.Now}
}

func (d db) nowUnix() int64 {
	return d.now().Unix()
}

func (d db) postMessage(ctx context.Context, tx *sql.Tx, ts int64, agentID, thread, kind, body string, mentions []string, priority string) error {
	if priority == "" {
		priority = "normal"
	}
	if mentions == nil {
		mentions = []string{}
	}
	mjson, err := json.Marshal(mentions)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO messages (ts, agent_id, thread, kind, body, mentions, priority)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, ts, agentID, thread, kind, body, string(mjson), priority)
	return err
}
