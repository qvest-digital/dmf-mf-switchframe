package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

func (d db) ask(ctx context.Context, agentID, thread, target, body string) error {
	full := fmt.Sprintf("@%s %s", target, body)
	return d.say(ctx, agentID, thread, "ask", full, []string{target}, "normal")
}

func (d db) answer(ctx context.Context, agentID string, ref int64, body string) error {
	full := fmt.Sprintf("re:%d %s", ref, body)
	return d.say(ctx, agentID, "global", "answer", full, nil, "normal")
}

func (d db) reviewRequest(ctx context.Context, agentID, itemID, reviewer string) error {
	body := fmt.Sprintf("review requested on %s", itemID)
	var mentions []string
	if reviewer != "" {
		body = fmt.Sprintf("@%s review requested on %s", reviewer, itemID)
		mentions = []string{reviewer}
	}
	return d.say(ctx, agentID, itemID, "review_req", body, mentions, "normal")
}

func runAsk(args []string) int {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	to := fs.String("to", "global", "thread")
	_ = fs.Parse(args)
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: sf-team ask @<agent> [--to ITEM|global] <message>")
		return 4
	}
	target := strings.TrimPrefix(fs.Arg(0), "@")
	body := strings.Join(fs.Args()[1:], " ")

	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	if err := d.ask(ctx, id, *to, target, body); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	return 0
}

func runAnswer(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: sf-team answer <msg-id> <message>")
		return 4
	}
	var ref int64
	_, _ = fmt.Sscanf(args[0], "%d", &ref)
	body := strings.Join(args[1:], " ")

	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	if err := d.answer(ctx, id, ref, body); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	return 0
}

func runReviewRequest(args []string) int {
	args = reorderFlags(args, nil)
	fs := flag.NewFlagSet("review-request", flag.ExitOnError)
	mention := fs.String("mention", "", "reviewer agent id (without @)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: sf-team review-request <ITEM-ID> [--mention @<agent>]")
		return 4
	}
	itemID := fs.Arg(0)
	reviewer := strings.TrimPrefix(*mention, "@")

	ctx, sqldb, id, cleanup, code := setupDBAndIdentity()
	if cleanup != nil {
		defer cleanup()
	}
	if code != 0 {
		return code
	}
	d := newDB(sqldb)
	if err := d.reviewRequest(ctx, id, itemID, reviewer); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	return 0
}
