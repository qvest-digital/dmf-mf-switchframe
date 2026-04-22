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
