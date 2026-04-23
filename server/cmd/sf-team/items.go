package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type ACItem struct {
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
}

type item struct {
	ID        string   `yaml:"id"`
	Title     string   `yaml:"title"`
	Type      string   `yaml:"type"`
	Priority  string   `yaml:"priority"`
	Area      string   `yaml:"area"`
	Status    string   `yaml:"status"`
	Estimate  string   `yaml:"estimate"`
	Risk      string   `yaml:"risk"`
	Created   string   `yaml:"created"`
	Updated   string   `yaml:"updated"`
	NotBefore string   `yaml:"not-before"`
	BlockedBy []string `yaml:"blocked-by"`
	RelatesTo []string `yaml:"relates-to"`

	ACTotal   int
	ACChecked int
	ACItems   []ACItem
	Body      string
	BodyPath  string
}

var (
	frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)
	acHeaderRe    = regexp.MustCompile(`(?m)^##\s+Acceptance\s+criteria\s*$`)
	nextHeaderRe  = regexp.MustCompile(`(?m)^##\s+`)
	checkboxRe    = regexp.MustCompile(`^\s*[-*]\s*\[([ xX])\]\s+`)
	subBulletRe   = regexp.MustCompile(`^\s{2,}[-*]\s+`)
)

func parseItemFile(path string) (item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return item{}, err
	}

	match := frontmatterRe.FindSubmatch(data)
	if match == nil {
		return item{}, fmt.Errorf("no frontmatter in %s", path)
	}
	var it item
	if err := yaml.Unmarshal(match[1], &it); err != nil {
		return item{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	it.BodyPath = path

	rest := data[len(match[0]):]
	it.Body = string(rest)
	hdr := acHeaderRe.FindIndex(rest)
	if hdr != nil {
		start := hdr[1]
		var end int
		if nxt := nextHeaderRe.FindIndex(rest[start:]); nxt != nil {
			end = start + nxt[0]
		} else {
			end = len(rest)
		}
		scanner := bufio.NewScanner(strings.NewReader(string(rest[start:end])))
		var cur *ACItem
		for scanner.Scan() {
			line := scanner.Text()
			if m := checkboxRe.FindStringSubmatch(line); m != nil {
				trimmed := strings.TrimSpace(checkboxRe.ReplaceAllString(line, ""))
				it.ACTotal++
				checked := strings.EqualFold(m[1], "x")
				if checked {
					it.ACChecked++
				}
				it.ACItems = append(it.ACItems, ACItem{Text: trimmed, Checked: checked})
				cur = &it.ACItems[len(it.ACItems)-1]
				continue
			}
			if cur != nil && subBulletRe.MatchString(line) {
				sub := strings.TrimSpace(subBulletRe.ReplaceAllString(line, ""))
				if sub != "" {
					cur.Text += "\n" + sub
				}
			}
		}
	}
	return it, nil
}

func (it item) ProgressPct() int {
	if it.ACTotal == 0 {
		return 0
	}
	return (it.ACChecked * 100) / it.ACTotal
}
