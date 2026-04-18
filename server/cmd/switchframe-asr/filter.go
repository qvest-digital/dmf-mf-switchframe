package main

import (
	"strings"

	"github.com/zsiec/switchframe/server/asr"
)

func filterWords(words []asr.TranscriptWord) []asr.TranscriptWord {
	if len(words) == 0 {
		return nil
	}

	var filtered []asr.TranscriptWord
	var prevText string
	repeatCount := 0

	for _, w := range words {
		text := w.Text

		// Skip single-character non-words (common TDT hallucination).
		if len(text) == 1 {
			lower := strings.ToLower(text)
			if lower != "i" && lower != "a" {
				continue
			}
		}

		// Skip low-confidence tokens. Threshold tuned for live broadcast:
		// the user-perceived cost of a wrong word then correction is worse
		// than a brief omission. Pairs with LocalAgreement-2 commit upstream.
		if w.Confidence > 0 && w.Confidence < 0.4 {
			continue
		}

		// Deloop: skip consecutive repeats (allow 1 repeat, skip 3+).
		if text == prevText {
			repeatCount++
			if repeatCount >= 2 {
				continue
			}
		} else {
			repeatCount = 0
		}
		prevText = text

		filtered = append(filtered, w)
	}

	// Discard entire segment if only 1 low-confidence word remains.
	if len(filtered) == 1 && filtered[0].Confidence > 0 && filtered[0].Confidence < 0.5 {
		return nil
	}

	return filtered
}
