package main

import (
	"testing"

	"github.com/zsiec/switchframe/server/asr"
)

func TestFilterWords_SingleCharFiltering(t *testing.T) {
	words := []asr.TranscriptWord{
		{Text: "x", Confidence: 0.9},
		{Text: "hello", Confidence: 0.9},
		{Text: "I", Confidence: 0.9},
		{Text: "z", Confidence: 0.9},
		{Text: "a", Confidence: 0.9},
	}
	got := filterWords(words)
	if len(got) != 3 {
		t.Fatalf("expected 3 words, got %d: %+v", len(got), got)
	}
	if got[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", got[0].Text)
	}
	if got[1].Text != "I" {
		t.Errorf("expected 'I', got %q", got[1].Text)
	}
	if got[2].Text != "a" {
		t.Errorf("expected 'a', got %q", got[2].Text)
	}
}

func TestFilterWords_LowConfidence(t *testing.T) {
	words := []asr.TranscriptWord{
		{Text: "hello", Confidence: 0.9},
		{Text: "um", Confidence: 0.2},
		{Text: "world", Confidence: 0.8},
	}
	got := filterWords(words)
	if len(got) != 2 {
		t.Fatalf("expected 2 words, got %d: %+v", len(got), got)
	}
	if got[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", got[0].Text)
	}
	if got[1].Text != "world" {
		t.Errorf("expected 'world', got %q", got[1].Text)
	}
}

func TestFilterWords_ZeroConfidenceNotFiltered(t *testing.T) {
	// Confidence of 0.0 means "not set" — should not be filtered by the < 0.3 check.
	words := []asr.TranscriptWord{
		{Text: "hello", Confidence: 0},
		{Text: "world", Confidence: 0},
	}
	got := filterWords(words)
	if len(got) != 2 {
		t.Fatalf("expected 2 words, got %d: %+v", len(got), got)
	}
}

func TestFilterWords_Deloop(t *testing.T) {
	words := []asr.TranscriptWord{
		{Text: "no", Confidence: 0.9},
		{Text: "no", Confidence: 0.9},  // 1 repeat — allowed
		{Text: "no", Confidence: 0.9},  // 2nd repeat — blocked
		{Text: "no", Confidence: 0.9},  // 3rd repeat — blocked
		{Text: "yes", Confidence: 0.9}, // new word — allowed
	}
	got := filterWords(words)
	if len(got) != 3 {
		t.Fatalf("expected 3 words, got %d: %+v", len(got), got)
	}
	if got[0].Text != "no" || got[1].Text != "no" || got[2].Text != "yes" {
		t.Errorf("expected [no, no, yes], got %+v", got)
	}
}

func TestFilterWords_SingleLowConfidenceDiscard(t *testing.T) {
	// If only 1 word remains and it has low confidence, discard entire segment.
	words := []asr.TranscriptWord{
		{Text: "um", Confidence: 0.4},
	}
	got := filterWords(words)
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestFilterWords_SingleHighConfidenceKept(t *testing.T) {
	words := []asr.TranscriptWord{
		{Text: "hello", Confidence: 0.9},
	}
	got := filterWords(words)
	if len(got) != 1 {
		t.Fatalf("expected 1 word, got %d", len(got))
	}
}

func TestFilterWords_NilEmpty(t *testing.T) {
	if got := filterWords(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %+v", got)
	}
	if got := filterWords([]asr.TranscriptWord{}); got != nil {
		t.Fatalf("expected nil for empty input, got %+v", got)
	}
}
