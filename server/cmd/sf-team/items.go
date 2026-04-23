package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type item struct {
	ID       string `yaml:"id"`
	Title    string `yaml:"title"`
	Type     string `yaml:"type"`
	Priority string `yaml:"priority"`
	Area     string `yaml:"area"`
	Status   string `yaml:"status"`
	Estimate string `yaml:"estimate"`
	Risk     string `yaml:"risk"`
	Created  string `yaml:"created"`
	Updated  string `yaml:"updated"`

	ACTotal   int
	ACChecked int
	BodyPath  string
}

var (
	frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)
	acHeaderRe    = regexp.MustCompile(`(?m)^##\s+Acceptance\s+criteria\s*$`)
	nextHeaderRe  = regexp.MustCompile(`(?m)^##\s+`)
	checkboxRe    = regexp.MustCompile(`^\s*[-*]\s*\[([ xX])\]\s+`)
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
		for scanner.Scan() {
			line := scanner.Text()
			m := checkboxRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			it.ACTotal++
			if strings.EqualFold(m[1], "x") {
				it.ACChecked++
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
