package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func (d db) tick(ctx context.Context, agentID string) (digest, error) {
	lost, err := d.heartbeat(ctx, agentID)
	if err != nil {
		return digest{}, err
	}
	if _, err := d.reclaimStale(ctx); err != nil {
		return digest{}, err
	}
	dg, err := d.digestFor(ctx, agentID)
	if err != nil {
		return digest{}, err
	}
	dg.LostClaims = lost

	updateRead := func(thread string, msgs []digestMessage) error {
		if len(msgs) == 0 {
			return nil
		}
		maxID := msgs[len(msgs)-1].ID
		_, err := d.sqldb.ExecContext(ctx, `
			INSERT INTO reads (agent_id, thread, last_msg_id) VALUES (?, ?, ?)
			ON CONFLICT(agent_id, thread) DO UPDATE SET last_msg_id = excluded.last_msg_id
		`, agentID, thread, maxID)
		return err
	}
	globalAll := append(append([]digestMessage(nil), dg.Knocks...), dg.Mentions...)
	globalAll = append(globalAll, dg.Global...)
	if err := updateRead("global", globalAll); err != nil {
		return dg, err
	}
	threadMsgs := map[string][]digestMessage{}
	for _, m := range dg.YourThreads {
		threadMsgs[m.Thread] = append(threadMsgs[m.Thread], m)
	}
	for thread, msgs := range threadMsgs {
		if err := updateRead(thread, msgs); err != nil {
			return dg, err
		}
	}
	return dg, nil
}

func runTick(args []string) int {
	args = reorderFlags(args, map[string]bool{"json": true})
	fs := flag.NewFlagSet("tick", flag.ExitOnError)
	jsonFlag := fs.Bool("json", false, "machine-readable output")
	_ = fs.Parse(args)

	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	dg, err := d.tick(ctx, id)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	if *jsonFlag {
		return emitDigestJSON(dg)
	}
	return emitDigestHuman(dg)
}
