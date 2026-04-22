package main

import (
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
	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Println(version())
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(4)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sf-team <command> [args...]")
	fmt.Fprintln(os.Stderr, "commands: register, claim, release, say, tick, who, tail, ... (see --help)")
}
