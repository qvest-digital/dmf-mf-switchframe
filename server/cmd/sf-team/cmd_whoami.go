package main

import (
	"flag"
	"fmt"
	"os"
)

// runWhoami prints the agent id this session resolves to, matching the
// value every other sf-team subcommand would use. Exists so skills and
// shells can get the id without reading the (wrong under per-session
// isolation) legacy ~/.switchframe-team/agent-id.txt file directly.
func runWhoami(args []string) int {
	fs := flag.NewFlagSet("whoami", flag.ExitOnError)
	fs.Parse(args)

	if err := ensureHome(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	id, err := agentID(detectWorktree())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(id)
	return 0
}
