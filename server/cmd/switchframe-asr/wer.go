package main

import (
	"math"
	"strings"
	"unicode"
)

// normalizeWords tokenizes text into a comparable word list:
// lowercase, punctuation-stripped, whitespace-collapsed. Apostrophes
// inside words ("don't") and hyphens between word characters
// ("five-year-old", "9:30") are kept; em dashes and stray punctuation
// are stripped or used as separators.
func normalizeWords(s string) []string {
	if s == "" {
		return nil
	}

	// Replace separators that should split words but aren't ASCII space.
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\u2014', '\u2013': // em dash, en dash
			return ' '
		}
		return r
	}, s)

	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		// Trim leading/trailing punctuation but keep internal apostrophes,
		// hyphens, colons (numeric times like 9:30).
		f = strings.TrimFunc(f, func(r rune) bool {
			return unicode.IsPunct(r) || unicode.IsSymbol(r)
		})
		if f == "" {
			continue
		}
		out = append(out, strings.ToLower(f))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// wer returns the word error rate of hyp against ref. Both inputs are
// normalized via normalizeWords before comparison.
//
// Special cases:
//   - empty ref + empty hyp: 0
//   - empty ref + non-empty hyp: +Inf (conventional)
func wer(ref, hyp string) float64 {
	r := normalizeWords(ref)
	h := normalizeWords(hyp)
	if len(r) == 0 && len(h) == 0 {
		return 0
	}
	if len(r) == 0 {
		return math.Inf(1)
	}
	dist := levenshtein(r, h)
	return float64(dist) / float64(len(r))
}

// levenshtein returns the word-level edit distance between two slices.
func levenshtein(a, b []string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(
				prev[j]+1,        // deletion
				curr[j-1]+1,      // insertion
				prev[j-1]+cost,   // substitution / match
			)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
