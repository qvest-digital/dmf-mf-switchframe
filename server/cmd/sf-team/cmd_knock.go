package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func (d db) knock(ctx context.Context, agentID, target, body string) error {
	full := fmt.Sprintf("@%s KNOCK: %s", target, body)
	return d.say(ctx, agentID, "global", "knock", full, []string{target}, "high")
}

func runKnock(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: sf-team knock @<agent> <message>")
		return 4
	}
	target := strings.TrimPrefix(args[0], "@")
	body := strings.Join(args[1:], " ")

	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	if err := d.knock(ctx, id, target, body); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	fmt.Printf("knocked @%s\n", target)
	return 0
}
