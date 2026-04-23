package main

import (
	"regexp"
	"strings"
)

var mentionRe = regexp.MustCompile(`(^|\s)@([a-zA-Z0-9_-]+)`)

func parseMentions(body string) []string {
	matches := mentionRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		name := strings.TrimPrefix(m[2], "@")
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}
