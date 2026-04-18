package main

import (
	"reflect"
	"testing"

	"github.com/zsiec/switchframe/server/asr"
)

func words(texts ...string) []asr.TranscriptWord {
	out := make([]asr.TranscriptWord, len(texts))
	for i, t := range texts {
		out[i] = asr.TranscriptWord{Text: t, Confidence: 0.9}
	}
	return out
}

func TestLongestCommonPrefix_EmptyInputs(t *testing.T) {
	if got := longestCommonPrefix(nil, nil); got != 0 {
		t.Errorf("nil/nil = %d, want 0", got)
	}
	if got := longestCommonPrefix(words("a"), nil); got != 0 {
		t.Errorf("non-empty/nil = %d, want 0", got)
	}
	if got := longestCommonPrefix(nil, words("a")); got != 0 {
		t.Errorf("nil/non-empty = %d, want 0", got)
	}
}

func TestLongestCommonPrefix_FullMatch(t *testing.T) {
	a := words("hello", "world")
	b := words("hello", "world")
	if got := longestCommonPrefix(a, b); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestLongestCommonPrefix_PartialMatch(t *testing.T) {
	a := words("the", "quick", "brown", "fox")
	b := words("the", "quick", "brown", "wolf")
	if got := longestCommonPrefix(a, b); got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

func TestLongestCommonPrefix_DivergeAtFirst(t *testing.T) {
	a := words("hello")
	b := words("world")
	if got := longestCommonPrefix(a, b); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestLongestCommonPrefix_PrevShorter(t *testing.T) {
	a := words("the", "quick")
	b := words("the", "quick", "brown", "fox")
	if got := longestCommonPrefix(a, b); got != 2 {
		t.Errorf("got %d, want 2 (prev exhausted)", got)
	}
}

func TestLongestCommonPrefix_CurrShorter(t *testing.T) {
	a := words("the", "quick", "brown", "fox")
	b := words("the", "quick")
	if got := longestCommonPrefix(a, b); got != 2 {
		t.Errorf("got %d, want 2 (curr exhausted)", got)
	}
}

func TestLongestCommonPrefix_CaseInsensitive(t *testing.T) {
	a := words("The", "Quick")
	b := words("the", "quick")
	if got := longestCommonPrefix(a, b); got != 2 {
		t.Errorf("got %d, want 2 (case shouldn't matter)", got)
	}
}

func TestLongestCommonPrefix_PunctuationStripped(t *testing.T) {
	// Whisper sometimes adds/drops trailing punctuation between hypotheses;
	// don't let that break the agreement.
	a := words("hello,", "world")
	b := words("hello", "world.")
	if got := longestCommonPrefix(a, b); got != 2 {
		t.Errorf("got %d, want 2 (punctuation should not break agreement)", got)
	}
}

// LocalAgreement-2 streaming state machine.

func TestStreamingCommit_FirstHypothesisCommitsNothing(t *testing.T) {
	s := newStreamingState()
	commit := s.acceptHypothesis(words("hello", "world"))
	if len(commit) != 0 {
		t.Errorf("first hypothesis should commit 0 words (need a second to confirm), got %v", commit)
	}
}

func TestStreamingCommit_SecondHypothesisCommitsAgreedPrefix(t *testing.T) {
	s := newStreamingState()
	s.acceptHypothesis(words("hello", "world", "this"))
	commit := s.acceptHypothesis(words("hello", "world", "is"))
	want := words("hello", "world")
	if !reflect.DeepEqual(textsOf(commit), textsOf(want)) {
		t.Errorf("got %v, want %v", textsOf(commit), textsOf(want))
	}
}

func TestStreamingCommit_OnlyEmitsNewlyCommitted(t *testing.T) {
	s := newStreamingState()
	s.acceptHypothesis(words("a", "b", "c"))
	first := s.acceptHypothesis(words("a", "b", "c"))
	if len(first) != 3 {
		t.Fatalf("expected 3 committed on second matching hyp, got %d: %v", len(first), textsOf(first))
	}
	second := s.acceptHypothesis(words("a", "b", "c", "d"))
	if len(second) != 0 {
		t.Errorf("d not yet confirmed, got %v", textsOf(second))
	}
	third := s.acceptHypothesis(words("a", "b", "c", "d", "e"))
	if len(third) != 1 || third[0].Text != "d" {
		t.Errorf("expected just [d], got %v", textsOf(third))
	}
}

func TestStreamingCommit_DivergenceRollsBackTentative(t *testing.T) {
	s := newStreamingState()
	s.acceptHypothesis(words("a", "b", "c"))
	s.acceptHypothesis(words("a", "b", "c")) // commits a, b, c
	// Now hypothesis disagrees on c -- but c was already emitted, can't unsay.
	// New disagreement just means subsequent words won't commit yet.
	commit := s.acceptHypothesis(words("a", "b", "c", "x"))
	if len(commit) != 0 {
		t.Errorf("nothing new should commit (x not confirmed), got %v", textsOf(commit))
	}
}

func TestStreamingCommit_FinalFlushReturnsUncommittedTail(t *testing.T) {
	s := newStreamingState()
	s.acceptHypothesis(words("a", "b", "c"))
	s.acceptHypothesis(words("a", "b", "c", "d")) // commits a, b, c
	tail := s.flushFinal(words("a", "b", "c", "d", "e"))
	want := words("d", "e")
	if !reflect.DeepEqual(textsOf(tail), textsOf(want)) {
		t.Errorf("flushFinal got %v, want %v", textsOf(tail), textsOf(want))
	}
}

func TestStreamingCommit_ResetClearsState(t *testing.T) {
	s := newStreamingState()
	s.acceptHypothesis(words("a", "b"))
	s.acceptHypothesis(words("a", "b")) // commits a, b
	s.reset()
	commit := s.acceptHypothesis(words("a", "b"))
	if len(commit) != 0 {
		t.Errorf("after reset, first hypothesis again commits nothing, got %v", textsOf(commit))
	}
}

func textsOf(ws []asr.TranscriptWord) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Text
	}
	return out
}
