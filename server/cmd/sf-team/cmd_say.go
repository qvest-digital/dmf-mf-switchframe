package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

func (d db) say(ctx context.Context, agentID, thread, kind, body string, mentions []string, priority string) error {
	tx, err := d.sqldb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if mentions == nil {
		mentions = parseMentions(body)
	}
	if err := d.postMessage(ctx, tx, d.nowUnix(), agentID, thread, kind, body, mentions, priority); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	d.publish("message", map[string]any{"thread": thread, "kind": kind, "agent_id": agentID, "body": body})
	return nil
}

func runSay(args []string) int {
	args = reorderFlags(args, nil)
	fs := flag.NewFlagSet("say", flag.ExitOnError)
	to := fs.String("to", "global", "thread: 'global' or '<ITEM-ID>'")
	mention := fs.String("mention", "", "comma-separated @ids")
	fs.Parse(args)

	body := strings.Join(fs.Args(), " ")
	if body == "" {
		fmt.Fprintln(os.Stderr, "usage: sf-team say [--to ITEM|global] [--mention a,b] <message>")
		return 4
	}

	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)

	var explicit []string
	if *mention != "" {
		for _, m := range strings.Split(*mention, ",") {
			if m = strings.TrimPrefix(strings.TrimSpace(m), "@"); m != "" {
				explicit = append(explicit, m)
			}
		}
	}
	if explicit == nil {
		explicit = parseMentions(body)
	}

	if err := d.say(ctx, id, *to, "say", body, explicit, "normal"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	return 0
}
