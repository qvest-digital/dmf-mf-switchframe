package main

import (
	"strings"

	"github.com/zsiec/switchframe/server/asr"
)

// longestCommonPrefix returns the number of leading words that match
// between two hypotheses. Comparison is case-insensitive and ignores
// surrounding whitespace and trailing/leading punctuation -- whisper
// frequently re-decides on punctuation between adjacent inferences and
// we don't want that to break agreement.
func longestCommonPrefix(prev, curr []asr.TranscriptWord) int {
	n := len(prev)
	if len(curr) < n {
		n = len(curr)
	}
	for i := 0; i < n; i++ {
		if !wordsAgree(prev[i], curr[i]) {
			return i
		}
	}
	return n
}

func wordsAgree(a, b asr.TranscriptWord) bool {
	return normalizeWordText(a.Text) == normalizeWordText(b.Text)
}

func normalizeWordText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, ".,!?;:\"'()-")
	return strings.ToLower(s)
}

// streamingState implements the LocalAgreement-2 commit policy: a word in
// the current utterance is committed (i.e. emitted to downstream) only
// once two consecutive whisper hypotheses agree on it. Eliminates the
// "wrong word, then correction" flicker users see with raw streaming.
//
// Per-utterance lifetime: created at speech start, fed each new full-
// utterance hypothesis via acceptHypothesis, and finalised on speech end
// via flushFinal which emits the still-uncommitted tail of the last hyp.
type streamingState struct {
	prevHyp        []asr.TranscriptWord // previous full-utterance hypothesis
	committedCount int                  // number of words already emitted
}

func newStreamingState() *streamingState {
	return &streamingState{}
}

// acceptHypothesis records a new full-utterance hypothesis and returns
// the words that have crossed the agreement threshold since the last
// call. The first call always returns nothing -- it just establishes the
// baseline for the next comparison.
func (s *streamingState) acceptHypothesis(hyp []asr.TranscriptWord) []asr.TranscriptWord {
	common := longestCommonPrefix(s.prevHyp, hyp)
	var emit []asr.TranscriptWord
	if common > s.committedCount {
		// Take from the new hypothesis (more accurate confidence/timing
		// than the prev hyp's view of the same word).
		emit = append(emit, hyp[s.committedCount:common]...)
	}
	s.committedCount = common
	s.prevHyp = hyp
	return emit
}

// flushFinal returns every remaining uncommitted word from a final
// utterance hypothesis. Called when VAD reports speech end -- there will
// be no further hypothesis to confirm the tail, so we emit it on faith.
func (s *streamingState) flushFinal(finalHyp []asr.TranscriptWord) []asr.TranscriptWord {
	if s.committedCount >= len(finalHyp) {
		return nil
	}
	emit := append([]asr.TranscriptWord(nil), finalHyp[s.committedCount:]...)
	s.committedCount = len(finalHyp)
	s.prevHyp = finalHyp
	return emit
}

// reset clears state for a new utterance.
func (s *streamingState) reset() {
	s.prevHyp = nil
	s.committedCount = 0
}
