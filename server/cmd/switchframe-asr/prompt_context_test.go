package main

import (
	"strings"
	"testing"
)

func TestUpdatePromptContext_AppendWithinLimit(t *testing.T) {
	got := updatePromptContext(nil, []string{"hello", "world"}, 200)
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Errorf("got %v, want [hello world]", got)
	}
}

func TestUpdatePromptContext_TrimsOldestWhenOverLimit(t *testing.T) {
	prev := make([]string, 0, 3)
	for _, w := range []string{"a", "b", "c"} {
		prev = append(prev, w)
	}
	got := updatePromptContext(prev, []string{"d", "e"}, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 words, got %d: %v", len(got), got)
	}
	want := []string{"c", "d", "e"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q (full: %v)", i, got[i], w, got)
		}
	}
}

func TestUpdatePromptContext_NoNewWordsLeavesPrevAlone(t *testing.T) {
	prev := []string{"a", "b"}
	got := updatePromptContext(prev, nil, 200)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v, want [a b]", got)
	}
}

func TestUpdatePromptContext_MaxZeroReturnsEmpty(t *testing.T) {
	got := updatePromptContext([]string{"a", "b"}, []string{"c"}, 0)
	if len(got) != 0 {
		t.Errorf("max=0 should return empty slice, got %v", got)
	}
}

func TestUpdatePromptContext_StableUnderRepeatedAppend(t *testing.T) {
	const maxWords = 5
	ctx := []string(nil)
	for i := 0; i < 100; i++ {
		ctx = updatePromptContext(ctx, []string{"x"}, maxWords)
		if len(ctx) > maxWords {
			t.Fatalf("ctx grew to %d at iteration %d, max %d", len(ctx), i, maxWords)
		}
	}
	if len(ctx) != maxWords {
		t.Errorf("after 100 appends, len = %d, want %d", len(ctx), maxWords)
	}
	for i, w := range ctx {
		if w != "x" {
			t.Errorf("ctx[%d] = %q, want x", i, w)
		}
	}
}

func TestPromptContextToString(t *testing.T) {
	got := promptContextToString([]string{"hello", "world", "foo"})
	want := "hello world foo"
	if got != want {
		t.Errorf("promptContextToString(...) = %q, want %q", got, want)
	}

	if got := promptContextToString(nil); got != "" {
		t.Errorf("nil input should give empty string, got %q", got)
	}

	// Defensive: trailing/leading whitespace already stripped from tokens.
	in := []string{" leading", "trailing ", "ok"}
	if got := promptContextToString(in); !strings.Contains(got, "leading") {
		t.Errorf("got %q", got)
	}
}
