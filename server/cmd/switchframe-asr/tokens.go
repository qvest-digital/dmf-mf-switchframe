package main

import (
	"strings"

	"github.com/zsiec/switchframe/server/asr"
)

// rawToken is a single whisper output token: its raw text (which may
// have a leading space marking a word boundary) and its decoder
// probability. Kept as a pure-Go type so the grouping logic is
// unit-testable without cgo.
type rawToken struct {
	Text string
	Prob float32
}

// tokensToWords groups whisper BPE tokens into whole words for emission
// to the caption pipeline. Whisper's tokenizer indicates word boundaries
// by a leading space on the token starting a new word; tokens without a
// leading space continue the previous word ("wizards" -> " wiz" + "ards").
//
// The previous implementation TrimSpace'd each token and emitted them
// individually, which produced "wiz ards" in the captions. This grouper
// preserves whisper's word boundaries: tokens with a leading space start
// a new word; tokens without a leading space attach to the current
// word; pure-whitespace tokens act as separators.
//
// Confidence per word is the arithmetic mean of its constituent token
// probabilities -- a small approximation, but it preserves the per-token
// signal we use for confidence-based filtering downstream.
func tokensToWords(tokens []rawToken) []asr.TranscriptWord {
	if len(tokens) == 0 {
		return nil
	}

	var (
		out    []asr.TranscriptWord
		buf    strings.Builder
		probSum float32
		probN   int
	)

	flush := func() {
		if buf.Len() == 0 {
			return
		}
		text := strings.TrimSpace(buf.String())
		if text != "" && probN > 0 {
			out = append(out, asr.TranscriptWord{
				Text:       text,
				Confidence: probSum / float32(probN),
			})
		}
		buf.Reset()
		probSum = 0
		probN = 0
	}

	for _, t := range tokens {
		if t.Text == "" {
			continue
		}
		// Pure-whitespace token: act as a separator.
		if strings.TrimSpace(t.Text) == "" {
			flush()
			continue
		}
		// A token with a leading space starts a new word -- but only if
		// we already have something buffered. Otherwise it's the first
		// word of the segment.
		if startsWithSpace(t.Text) && buf.Len() > 0 {
			flush()
		}
		buf.WriteString(t.Text)
		probSum += t.Prob
		probN++
	}
	flush()

	if len(out) == 0 {
		return nil
	}
	return out
}

func startsWithSpace(s string) bool {
	return len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n')
}
