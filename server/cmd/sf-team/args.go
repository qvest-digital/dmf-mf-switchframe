package main

import "strings"

// reorderFlags moves all flag-looking tokens (-x, --x, -x=..., --x=...)
// plus their immediately following values (for non-boolean flags) to the
// front, preserving relative order. Positional args go after. This lets
// users write "sf-team claim BUG-X --intent ..." and have flag.Parse work.
//
// boolFlags is the set of flag NAMES (without leading dashes) that take no
// value. Unknown flags are assumed to take a value (safer).
func reorderFlags(args []string, boolFlags map[string]bool) []string {
	var flags, positional []string
	skipNext := false
	for i, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") {
				flags = append(flags, a)
				continue
			}
			flags = append(flags, a)
			if !boolFlags[name] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				skipNext = true
			}
		} else {
			positional = append(positional, a)
		}
	}
	return append(flags, positional...)
}
