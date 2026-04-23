package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func emitDigestJSON(dg digest) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(dg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	return 0
}

func emitDigestHuman(dg digest) int {
	t := time.Unix(dg.NowTS, 0).Format("15:04:05")
	fmt.Printf("=== sf-team tick (%s, %s) ===\n\n", dg.Agent, t)

	quiet := len(dg.Knocks) == 0 && len(dg.Mentions) == 0 && len(dg.YourThreads) == 0 &&
		len(dg.Global) == 0 && len(dg.LostClaims) == 0

	if quiet {
		fmt.Printf("tick ok, no news. agent=%s\n", dg.Agent)
		return 0
	}

	if len(dg.Knocks) > 0 {
		for _, m := range dg.Knocks {
			fmt.Printf("*** KNOCK from %s (%s): %s\n",
				m.Agent, time.Unix(m.TS, 0).Format("15:04"), m.Body)
		}
		fmt.Println()
	}
	if len(dg.LostClaims) > 0 {
		fmt.Printf("LOST CLAIMS:\n")
		for _, id := range dg.LostClaims {
			fmt.Printf("  %s (was yours, reclaimed as stale)\n", id)
		}
		fmt.Println()
	}
	if len(dg.Mentions) > 0 {
		fmt.Printf("MENTIONS (%d):\n", len(dg.Mentions))
		for _, m := range dg.Mentions {
			fmt.Printf("  [%s] %s in #%s: %s\n",
				time.Unix(m.TS, 0).Format("15:04"), m.Agent, m.Thread, m.Body)
		}
		fmt.Println()
	}
	if len(dg.YourThreads) > 0 {
		fmt.Printf("IN YOUR ITEM THREADS (%d):\n", len(dg.YourThreads))
		for _, m := range dg.YourThreads {
			fmt.Printf("  [%s] %s → #%s: %s\n",
				time.Unix(m.TS, 0).Format("15:04"), m.Agent, m.Thread, m.Body)
		}
		fmt.Println()
	}
	if len(dg.Global) > 0 {
		fmt.Printf("GLOBAL (%d):\n", len(dg.Global))
		for _, m := range dg.Global {
			fmt.Printf("  [%s] %s: %s\n",
				time.Unix(m.TS, 0).Format("15:04"), m.Agent, m.Body)
		}
	}
	return 0
}
