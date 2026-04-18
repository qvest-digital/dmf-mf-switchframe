package main

import "strings"

// updatePromptContext maintains a rolling window of recently-confirmed
// transcript words for use as whisper's initial_prompt. Returns the new
// window, trimmed to the most recent maxWords. Caller stores the result.
//
// We re-allocate (rather than use slice tricks) so the returned slice is
// independent of any aliasing the caller may have on the input slices.
func updatePromptContext(prev, newWords []string, maxWords int) []string {
	if maxWords <= 0 {
		return []string{}
	}

	total := len(prev) + len(newWords)
	if total <= maxWords {
		out := make([]string, 0, total)
		out = append(out, prev...)
		out = append(out, newWords...)
		return out
	}

	// Combined exceeds limit: keep last maxWords.
	out := make([]string, 0, maxWords)
	if len(newWords) >= maxWords {
		out = append(out, newWords[len(newWords)-maxWords:]...)
		return out
	}
	keepFromPrev := maxWords - len(newWords)
	out = append(out, prev[len(prev)-keepFromPrev:]...)
	out = append(out, newWords...)
	return out
}

// promptContextToString joins the rolling window into a single string for
// passing as whisper_full_params.initial_prompt. Tokens already had
// surrounding whitespace stripped at emit time, so a single-space join
// reconstructs a reasonable approximation of the original utterance --
// enough for whisper to anchor its decoder on prior context.
func promptContextToString(words []string) string {
	if len(words) == 0 {
		return ""
	}
	return strings.Join(words, " ")
}
