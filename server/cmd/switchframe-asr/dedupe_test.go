package main

import (
	"testing"

	"github.com/zsiec/switchframe/server/asr"
)

func wordsFromTexts(texts ...string) []asr.TranscriptWord {
	out := make([]asr.TranscriptWord, len(texts))
	for i, t := range texts {
		out[i] = asr.TranscriptWord{Text: t, Confidence: 0.9}
	}
	return out
}

func textsFromWords(ws []asr.TranscriptWord) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Text
	}
	return out
}

func TestDedupePhraseRuns_NoDuplicates(t *testing.T) {
	in := wordsFromTexts("the", "quick", "brown", "fox")
	got := dedupePhraseRuns(in)
	if !slicesEqualText(textsFromWords(got), []string{"the", "quick", "brown", "fox"}) {
		t.Errorf("got %v", textsFromWords(got))
	}
}

func TestDedupePhraseRuns_RemovesAdjacentRepeatedPhrase(t *testing.T) {
	// "quick brown fox The quick brown fox jumps" -> "The quick brown fox jumps"
	in := wordsFromTexts("quick", "brown", "fox", "The", "quick", "brown", "fox", "jumps")
	got := textsFromWords(dedupePhraseRuns(in))
	want := []string{"The", "quick", "brown", "fox", "jumps"}
	if !slicesEqualText(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDedupePhraseRuns_TripleRepeatCollapsesToOnce(t *testing.T) {
	in := wordsFromTexts("hello", "world", "hello", "world", "hello", "world")
	got := textsFromWords(dedupePhraseRuns(in))
	want := []string{"hello", "world"}
	if !slicesEqualText(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDedupePhraseRuns_DoesNotMergeAcrossNonRepeats(t *testing.T) {
	// Don't accidentally fold non-repeated phrases.
	in := wordsFromTexts("the", "cat", "sat", "on", "the", "mat")
	got := textsFromWords(dedupePhraseRuns(in))
	want := []string{"the", "cat", "sat", "on", "the", "mat"}
	if !slicesEqualText(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDedupePhraseRuns_CaseAndPunctuationIgnoredForMatching(t *testing.T) {
	// Whisper sometimes emits the same phrase with different case or
	// trailing punctuation across segment restarts.
	in := wordsFromTexts("Hello", "world.", "hello", "world", "today")
	got := textsFromWords(dedupePhraseRuns(in))
	// Should keep the second occurrence (longer continuation) and drop
	// the leading dup.
	want := []string{"hello", "world", "today"}
	if !slicesEqualText(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDedupePhraseRuns_NilEmpty(t *testing.T) {
	if got := dedupePhraseRuns(nil); got != nil {
		t.Errorf("nil input -> %v, want nil", got)
	}
	if got := dedupePhraseRuns([]asr.TranscriptWord{}); got != nil {
		t.Errorf("empty input -> %v, want nil", got)
	}
}

func TestDedupePhraseRuns_SingleWordRepeatedNotConsideredPhrase(t *testing.T) {
	// Single-word repeats are handled by filterWords (the deloop pass).
	// dedupePhraseRuns shouldn't fold "hello hello" by itself; it needs
	// at least a 2-word phrase to feel safe collapsing.
	in := wordsFromTexts("hello", "hello", "world")
	got := textsFromWords(dedupePhraseRuns(in))
	want := []string{"hello", "hello", "world"}
	if !slicesEqualText(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
