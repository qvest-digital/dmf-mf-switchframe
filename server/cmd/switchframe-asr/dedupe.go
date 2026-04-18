package main

import (
	"github.com/zsiec/switchframe/server/asr"
)

// dedupePhraseRuns drops a leading phrase prefix when it also appears as
// a contiguous run later in the same word sequence. Catches whisper's
// "segment restart" behavior where a fragment of an utterance is emitted
// in one segment and then re-emitted in full as part of the next:
//
//	"quick brown fox The quick brown fox jumps over the lazy dog"
//	-> "The quick brown fox jumps over the lazy dog"
//
// And triple-or-more repeats:
//
//	"hello world hello world hello world" -> "hello world"
//
// Algorithm: at each pass, find the largest k (k >= 2) such that
// words[0:k] equals words[j:j+k] for some j >= k. If found, drop
// words[0:k] and run another pass. Single-word leading repeats are
// never folded -- they're indistinguishable from legitimate function-
// word repetition like "the cat sat on the mat" without semantic
// context.
//
// Comparison is case- and surrounding-punctuation-insensitive, matching
// streamingState.longestCommonPrefix's normalization.
func dedupePhraseRuns(words []asr.TranscriptWord) []asr.TranscriptWord {
	if len(words) == 0 {
		return nil
	}
	for {
		before := len(words)
		words = dedupePhraseRunsOnePass(words)
		if len(words) == before {
			break
		}
	}
	return words
}

func dedupePhraseRunsOnePass(words []asr.TranscriptWord) []asr.TranscriptWord {
	if len(words) < 4 {
		return words // need k>=2 + a window of length>=2 starting after k
	}
	maxK := len(words) / 2
	// Largest k first so we strip the broadest restart on this pass.
	for k := maxK; k >= 2; k-- {
		// Prefer adjacent (j=k) since that's the strict-repeat case we're
		// most certain about, but accept any j >= k.
		if windowsAgree(words[:k], words[k:2*k]) {
			return words[k:]
		}
	}
	for k := maxK; k >= 2; k-- {
		for j := k + 1; j+k <= len(words); j++ {
			if windowsAgree(words[:k], words[j:j+k]) {
				return words[k:]
			}
		}
	}
	return words
}

func windowsAgree(a, b []asr.TranscriptWord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if normalizeWordText(a[i].Text) != normalizeWordText(b[i].Text) {
			return false
		}
	}
	return true
}
