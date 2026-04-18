package main

import (
	"math"
	"testing"
)

func TestNormalizeWords(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "Hello", []string{"hello"}},
		{"strip punctuation", "Hello, world!", []string{"hello", "world"}},
		{"collapse whitespace", "  hello   world  ", []string{"hello", "world"}},
		{"contractions kept whole", "don't won't", []string{"don't", "won't"}},
		{"em dash splits", "yes—no", []string{"yes", "no"}},
		{"quotes stripped", `"hello," she said.`, []string{"hello", "she", "said"}},
		{"mixed case lowered", "The Quick Brown FOX", []string{"the", "quick", "brown", "fox"}},
		{"hyphens in compound preserved", "five-year-old", []string{"five-year-old"}},
		{"numbers kept", "March 14, 2026 at 9:30", []string{"march", "14", "2026", "at", "9:30"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeWords(tt.in)
			if !slicesEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWER_PerfectMatch(t *testing.T) {
	w := wer("hello world", "hello world")
	if w != 0 {
		t.Errorf("identical input WER = %v, want 0", w)
	}
}

func TestWER_SingleSubstitution(t *testing.T) {
	// "hello world" vs "hello there": 1 substitution / 2 ref words = 0.5
	w := wer("hello world", "hello there")
	if !approxEqual(w, 0.5) {
		t.Errorf("got %v, want 0.5", w)
	}
}

func TestWER_SingleInsertion(t *testing.T) {
	// "hello world" vs "hello big world": 1 insertion / 2 ref words = 0.5
	w := wer("hello world", "hello big world")
	if !approxEqual(w, 0.5) {
		t.Errorf("got %v, want 0.5", w)
	}
}

func TestWER_SingleDeletion(t *testing.T) {
	// "hello big world" vs "hello world": 1 deletion / 3 ref words = 0.333...
	w := wer("hello big world", "hello world")
	if !approxEqual(w, 1.0/3.0) {
		t.Errorf("got %v, want 0.333...", w)
	}
}

func TestWER_AllWrong(t *testing.T) {
	w := wer("a b c", "x y z")
	if !approxEqual(w, 1.0) {
		t.Errorf("got %v, want 1.0", w)
	}
}

func TestWER_EmptyHypothesis(t *testing.T) {
	w := wer("a b c", "")
	if !approxEqual(w, 1.0) {
		t.Errorf("got %v, want 1.0", w)
	}
}

func TestWER_EmptyRefIsZeroIfHypAlsoEmpty(t *testing.T) {
	w := wer("", "")
	if w != 0 {
		t.Errorf("got %v, want 0 (both empty)", w)
	}
}

func TestWER_EmptyRefWithHypIsInfinity(t *testing.T) {
	// No reference words -> any insertion is infinite WER. Conventional
	// metrics report this as +Inf so it's visibly wrong vs 0.
	w := wer("", "extra words")
	if !math.IsInf(w, 1) {
		t.Errorf("got %v, want +Inf (no ref, insertions)", w)
	}
}

func TestWER_IgnoresCaseAndPunctuation(t *testing.T) {
	w := wer("Hello, World!", "hello world")
	if w != 0 {
		t.Errorf("punctuation-only diff WER = %v, want 0", w)
	}
}

// slicesEqual is a strict element-wise comparison; nil and empty are equal.
func slicesEqual[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
