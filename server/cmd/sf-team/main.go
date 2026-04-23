package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

const versionString = "0.1.0-dev"

func version() string { return versionString }

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(4)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "version", "-v", "--version":
		fmt.Println(version())
	case "register":
		os.Exit(runRegister(args))
	case "claim":
		os.Exit(runClaim(args))
	case "release":
		os.Exit(runRelease(args))
	case "done":
		os.Exit(runDone(args))
	case "blocked":
		os.Exit(runBlocked(args))
	case "reassign":
		os.Exit(runReassign(args))
	case "say":
		os.Exit(runSay(args))
	case "ask":
		os.Exit(runAsk(args))
	case "answer":
		os.Exit(runAnswer(args))
	case "review-request":
		os.Exit(runReviewRequest(args))
	case "knock":
		os.Exit(runKnock(args))
	case "progress":
		os.Exit(runProgress(args))
	case "touch":
		os.Exit(runTouch(args))
	case "untouch":
		os.Exit(runUntouch(args))
	case "tick":
		os.Exit(runTick(args))
	case "who":
		os.Exit(runWho(args))
	case "status":
		os.Exit(runStatus(args))
	case "history":
		os.Exit(runHistory(args))
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(4)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sf-team <command> [args...]")
	fmt.Fprintln(os.Stderr, "commands: register, claim, release, say, tick, who, tail, ... (see --help)")
}

func setupDBAndIdentity() (context.Context, *sql.DB, string, func(), int) {
	if err := ensureHome(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, "", nil, 4
	}
	path, err := dbPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, "", nil, 4
	}
	sqldb, err := openDB(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, "", nil, 4
	}
	wt := detectWorktree()
	id, err := agentID(wt)
	if err != nil {
		sqldb.Close()
		fmt.Fprintln(os.Stderr, err)
		return nil, nil, "", nil, 4
	}
	cleanup := func() { sqldb.Close() }
	return context.Background(), sqldb, id, cleanup, 0
}
