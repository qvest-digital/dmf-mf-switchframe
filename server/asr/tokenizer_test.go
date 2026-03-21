package asr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTokenizer_IsSpecialToken(t *testing.T) {
	tok := NewTokenizer()

	// Special tokens start at eotToken (50257).
	if !tok.IsSpecial(50257) {
		t.Error("token 50257 (EOT) should be special")
	}
	if !tok.IsSpecial(50258) {
		t.Error("token 50258 (SOT) should be special")
	}
	if !tok.IsSpecial(50360) {
		t.Error("token 50360 (transcribe) should be special")
	}
	if !tok.IsSpecial(51864) {
		t.Error("token 51864 (last timestamp) should be special")
	}

	// Regular BPE tokens are not special.
	if tok.IsSpecial(0) {
		t.Error("token 0 should not be special")
	}
	if tok.IsSpecial(100) {
		t.Error("token 100 should not be special")
	}
	if tok.IsSpecial(50256) {
		t.Error("token 50256 should not be special")
	}
}

func TestTokenizer_EOTToken(t *testing.T) {
	tok := NewTokenizer()
	if got := tok.EOT(); got != 50257 {
		t.Errorf("EOT() = %d, want 50257", got)
	}
}

func TestTokenizer_SOTToken(t *testing.T) {
	tok := NewTokenizer()
	if got := tok.SOT(); got != 50258 {
		t.Errorf("SOT() = %d, want 50258", got)
	}
}

func TestTokenizer_TranscribeToken(t *testing.T) {
	tok := NewTokenizer()
	if got := tok.Transcribe(); got != 50360 {
		t.Errorf("Transcribe() = %d, want 50360", got)
	}
}

func TestTokenizer_NoTimestampsToken(t *testing.T) {
	tok := NewTokenizer()
	if got := tok.NoTimestamps(); got != 50364 {
		t.Errorf("NoTimestamps() = %d, want 50364", got)
	}
}

func TestTokenizer_NoSpeechToken(t *testing.T) {
	tok := NewTokenizer()
	if got := tok.NoSpeech(); got != 50363 {
		t.Errorf("NoSpeech() = %d, want 50363", got)
	}
}

func TestTokenizer_VocabSize(t *testing.T) {
	tok := NewTokenizer()
	if got := tok.VocabSize(); got != 51865 {
		t.Errorf("VocabSize() = %d, want 51865", got)
	}
}

func TestTokenizer_LanguageToken(t *testing.T) {
	tok := NewTokenizer()

	tests := []struct {
		lang string
		want int
	}{
		{"en", 50259},
		{"zh", 50260},
		{"de", 50261},
		{"es", 50262},
		{"ja", 50266},
		{"fr", 50265},
		{"su", 50357}, // Last language token (offset 98).
	}
	for _, tt := range tests {
		if got := tok.LanguageToken(tt.lang); got != tt.want {
			t.Errorf("LanguageToken(%q) = %d, want %d", tt.lang, got, tt.want)
		}
	}
}

func TestTokenizer_LanguageToken_UnknownFallsBackToEnglish(t *testing.T) {
	tok := NewTokenizer()
	if got := tok.LanguageToken("xx"); got != 50259 {
		t.Errorf("LanguageToken(\"xx\") = %d, want 50259 (English fallback)", got)
	}
}

func TestTokenizer_DecodeSpecialTokensEmpty(t *testing.T) {
	tok := NewTokenizer()
	// All special tokens should be filtered out, resulting in empty string.
	tokens := []int{50258, 50259, 50360, 50364, 50257}
	got := tok.Decode(tokens)
	if got != "" {
		t.Errorf("Decode(special tokens) = %q, want empty string", got)
	}
}

func TestTokenizer_DecodeOutOfRange(t *testing.T) {
	tok := NewTokenizer()
	// Negative and out-of-range tokens should be silently skipped.
	tokens := []int{-1, 999999}
	got := tok.Decode(tokens)
	if got != "" {
		t.Errorf("Decode(out of range) = %q, want empty string", got)
	}
}

func TestTokenizer_DecodeEmptyTokens(t *testing.T) {
	tok := NewTokenizer()
	got := tok.Decode(nil)
	if got != "" {
		t.Errorf("Decode(nil) = %q, want empty string", got)
	}
	got = tok.Decode([]int{})
	if got != "" {
		t.Errorf("Decode(empty) = %q, want empty string", got)
	}
}

func TestDecodeBPEToken_SpacePrefix(t *testing.T) {
	// 'Ġ' (U+0120) represents a space prefix in GPT-2 BPE.
	got := decodeBPEToken("Ġhello")
	if got != " hello" {
		t.Errorf("decodeBPEToken(\"Ġhello\") = %q, want \" hello\"", got)
	}
}

func TestDecodeBPEToken_Newline(t *testing.T) {
	// 'Ċ' (U+010A) represents a newline.
	got := decodeBPEToken("Ċ")
	if got != "\n" {
		t.Errorf("decodeBPEToken(\"Ċ\") = %q, want \"\\n\"", got)
	}
}

func TestDecodeBPEToken_Tab(t *testing.T) {
	// 'ĉ' (U+0109) represents a tab.
	got := decodeBPEToken("ĉ")
	if got != "\t" {
		t.Errorf("decodeBPEToken(\"ĉ\") = %q, want \"\\t\"", got)
	}
}

func TestDecodeBPEToken_PlainASCII(t *testing.T) {
	got := decodeBPEToken("hello")
	if got != "hello" {
		t.Errorf("decodeBPEToken(\"hello\") = %q, want \"hello\"", got)
	}
}

func TestDecodeBPEToken_Unicode(t *testing.T) {
	// Actual Unicode characters (not part of the byte mapping) pass through.
	got := decodeBPEToken("café")
	if got != "café" {
		t.Errorf("decodeBPEToken(\"café\") = %q, want \"café\"", got)
	}
}

func TestTokenizer_LoadVocabAndDecode(t *testing.T) {
	// Create a temporary vocab.json with a few entries.
	dir := t.TempDir()
	vocab := map[string]int{
		"Ġhello": 100,
		"Ġworld": 200,
		"!":      300,
	}
	data, err := json.Marshal(vocab)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vocab.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	tok := NewTokenizer()
	if err := tok.LoadVocab(dir); err != nil {
		t.Fatalf("LoadVocab: %v", err)
	}

	got := tok.Decode([]int{100, 200, 300})
	want := "hello world!"
	if got != want {
		t.Errorf("Decode = %q, want %q", got, want)
	}
}

func TestTokenizer_LoadVocabMixedSpecialAndRegular(t *testing.T) {
	dir := t.TempDir()
	vocab := map[string]int{
		"Ġhello": 100,
		"Ġworld": 200,
	}
	data, err := json.Marshal(vocab)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vocab.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	tok := NewTokenizer()
	if err := tok.LoadVocab(dir); err != nil {
		t.Fatalf("LoadVocab: %v", err)
	}

	// SOT + lang + transcribe + regular tokens + EOT
	got := tok.Decode([]int{50258, 50259, 50360, 50364, 100, 200, 50257})
	want := "hello world"
	if got != want {
		t.Errorf("Decode = %q, want %q", got, want)
	}
}

func TestTokenizer_LoadVocab_NotFound(t *testing.T) {
	tok := NewTokenizer()
	err := tok.LoadVocab("/nonexistent/path")
	if err == nil {
		t.Error("expected error for missing vocab.json")
	}
}

func TestTokenizer_LoadVocab_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vocab.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	tok := NewTokenizer()
	err := tok.LoadVocab(dir)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestTokenizer_DecodeWithoutVocab(t *testing.T) {
	// Without loading vocab, regular token IDs produce empty strings.
	tok := NewTokenizer()
	got := tok.Decode([]int{100, 200})
	if got != "" {
		t.Errorf("Decode without vocab = %q, want empty string", got)
	}
}

func TestTokenizer_TimestampTokensAreSpecial(t *testing.T) {
	tok := NewTokenizer()

	// First timestamp token.
	if !tok.IsSpecial(50365) {
		t.Error("token 50365 (first timestamp) should be special")
	}
	// Last timestamp token.
	if !tok.IsSpecial(51864) {
		t.Error("token 51864 (last timestamp) should be special")
	}
}

func TestTokenizer_AllLanguageOffsets(t *testing.T) {
	tok := NewTokenizer()

	// Verify all languages produce tokens in the valid range.
	for lang, offset := range languageOffsets {
		got := tok.LanguageToken(lang)
		if got < firstLangToken || got > lastLangToken {
			t.Errorf("LanguageToken(%q) = %d, out of range [%d, %d]",
				lang, got, firstLangToken, lastLangToken)
		}
		wantID := firstLangToken + offset
		if got != wantID {
			t.Errorf("LanguageToken(%q) = %d, want %d", lang, got, wantID)
		}
	}
}
