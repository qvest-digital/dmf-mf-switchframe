package main

import (
	"math"
	"testing"

	"github.com/zsiec/switchframe/server/asr"
)

func tk(text string, prob float32) rawToken {
	return rawToken{Text: text, Prob: prob}
}

func TestTokensToWords_SingleWord(t *testing.T) {
	got := tokensToWords([]rawToken{tk(" hello", 0.9)})
	want := []asr.TranscriptWord{{Text: "hello", Confidence: 0.9}}
	assertWords(t, got, want)
}

func TestTokensToWords_TwoSeparateWords(t *testing.T) {
	got := tokensToWords([]rawToken{tk(" hello", 0.9), tk(" world", 0.8)})
	want := []asr.TranscriptWord{
		{Text: "hello", Confidence: 0.9},
		{Text: "world", Confidence: 0.8},
	}
	assertWords(t, got, want)
}

func TestTokensToWords_SplitWordRejoined(t *testing.T) {
	// Whisper tokenizes "wizards" as " wiz" + "ards". We must rejoin.
	got := tokensToWords([]rawToken{tk(" wiz", 0.9), tk("ards", 0.5)})
	want := []asr.TranscriptWord{{Text: "wizards", Confidence: 0.7}}
	assertWords(t, got, want)
}

func TestTokensToWords_TrailingPunctuationAttaches(t *testing.T) {
	got := tokensToWords([]rawToken{tk(" hello", 0.9), tk(".", 0.95)})
	want := []asr.TranscriptWord{{Text: "hello.", Confidence: 0.925}}
	assertWords(t, got, want)
}

func TestTokensToWords_FirstTokenWithoutLeadingSpace(t *testing.T) {
	got := tokensToWords([]rawToken{tk("hello", 0.9), tk(" world", 0.8)})
	want := []asr.TranscriptWord{
		{Text: "hello", Confidence: 0.9},
		{Text: "world", Confidence: 0.8},
	}
	assertWords(t, got, want)
}

func TestTokensToWords_EmptyTokensSkipped(t *testing.T) {
	got := tokensToWords([]rawToken{tk(" hello", 0.9), tk("", 0.0), tk(" world", 0.8)})
	want := []asr.TranscriptWord{
		{Text: "hello", Confidence: 0.9},
		{Text: "world", Confidence: 0.8},
	}
	assertWords(t, got, want)
}

func TestTokensToWords_NilEmptyInput(t *testing.T) {
	if got := tokensToWords(nil); got != nil {
		t.Errorf("nil input -> %v, want nil", got)
	}
	if got := tokensToWords([]rawToken{}); got != nil {
		t.Errorf("empty input -> %v, want nil", got)
	}
}

func TestTokensToWords_MultipleSegmentsWithoutSpaceBetween(t *testing.T) {
	// A token starting without a space mid-stream attaches to the previous
	// word. Simulates "Stocks tumbled" splitting into " Stock" + "s" + " tumbled"
	// (whisper sometimes splits the plural -s).
	got := tokensToWords([]rawToken{
		tk(" Stock", 0.9), tk("s", 0.85), tk(" tumbled", 0.8),
	})
	want := []asr.TranscriptWord{
		{Text: "Stocks", Confidence: 0.875},
		{Text: "tumbled", Confidence: 0.8},
	}
	assertWords(t, got, want)
}

func TestTokensToWords_PureWhitespaceTokenBecomesSeparator(t *testing.T) {
	got := tokensToWords([]rawToken{tk(" hello", 0.9), tk(" ", 0.99), tk(" world", 0.8)})
	want := []asr.TranscriptWord{
		{Text: "hello", Confidence: 0.9},
		{Text: "world", Confidence: 0.8},
	}
	assertWords(t, got, want)
}

func TestTokensToWords_StandaloneEllipsisIsOneWord(t *testing.T) {
	got := tokensToWords([]rawToken{tk(" ...", 0.6)})
	want := []asr.TranscriptWord{{Text: "...", Confidence: 0.6}}
	assertWords(t, got, want)
}

func assertWords(t *testing.T, got, want []asr.TranscriptWord) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d words %+v, want %d words %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i].Text != want[i].Text {
			t.Errorf("got[%d].Text = %q, want %q", i, got[i].Text, want[i].Text)
		}
		if math.Abs(float64(got[i].Confidence-want[i].Confidence)) > 1e-5 {
			t.Errorf("got[%d].Confidence = %f, want %f", i, got[i].Confidence, want[i].Confidence)
		}
	}
}
