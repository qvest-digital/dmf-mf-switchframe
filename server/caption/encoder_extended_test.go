package caption

import (
	"bytes"
	"log/slog"
	"math/bits"
	"strings"
	"sync"
	"testing"
	"time"
)

func captureSlog(t *testing.T) (*syncBuf, func()) {
	t.Helper()
	prev := slog.Default()
	buf := &syncBuf{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return buf, func() { slog.SetDefault(prev) }
}

type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func drainEncoder(enc *Encoder, skip int) []CCPair {
	for i := 0; i < skip; i++ {
		enc.NextPair()
	}
	var out []CCPair
	for {
		p := enc.NextPair()
		if p == nil {
			return out
		}
		out = append(out, *p)
	}
}

func flattenPairs(pairs []CCPair) []byte {
	var out []byte
	for _, p := range pairs {
		out = append(out, p.Data[0], p.Data[1])
	}
	return out
}

func containsSeq(haystack, needle []byte) bool {
	return bytes.Contains(haystack, needle)
}

func TestEncoder_BasicASCIIUnchanged(t *testing.T) {
	enc := NewEncoder(2)
	enc.IngestText("HELLO")

	pairs := drainEncoder(enc, 3)
	got := flattenPairs(pairs)

	want := []byte{
		oddParity('H'), oddParity('E'),
		oddParity('L'), oddParity('L'),
		oddParity('O'), 0x80,
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("HELLO bytes = % 02X, want % 02X", got, want)
	}
}

// CEA-608-C basic table: certain ASCII slots are remapped to Latin chars.
// é, í, ó, ú, á, ñ, Ñ, ç use single basic bytes (no extended escape).
func TestEncoder_BasicLatinSubstitutionsUseBasicBytes(t *testing.T) {
	cases := []struct {
		name string
		r    rune
		raw  byte // raw 7-bit basic table value (parity applied at encode time)
	}{
		{"á", 'á', 0x2A},
		{"é", 'é', 0x5C},
		{"í", 'í', 0x5E},
		{"ó", 'ó', 0x5F},
		{"ú", 'ú', 0x60},
		{"ç", 'ç', 0x7B},
		{"÷", '÷', 0x7C},
		{"Ñ", 'Ñ', 0x7D},
		{"ñ", 'ñ', 0x7E},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(2)
			enc.IngestText(string(tc.r))

			pairs := drainEncoder(enc, 3)
			got := flattenPairs(pairs)
			want := []byte{oddParity(tc.raw), 0x80}

			if !bytes.Equal(got, want) {
				t.Fatalf("rune %q: bytes = % 02X; want % 02X", string(tc.r), got, want)
			}
		})
	}
}

func TestEncoder_LatinExtendedMappedToCEA608Extended(t *testing.T) {
	// Chars NOT in the basic table — must use Extended sequences.
	cases := []struct {
		name   string
		r      rune
		prefix byte
		data   byte
	}{
		{"ü", 'ü', 0x12, 0x25},
		{"Ü", 'Ü', 0x12, 0x24},
		{"É", 'É', 0x12, 0x21},
		{"Á", 'Á', 0x12, 0x20},
		{"Ó", 'Ó', 0x12, 0x22},
		{"Ú", 'Ú', 0x12, 0x23},
		{"À", 'À', 0x12, 0x30},
		{"Â", 'Â', 0x12, 0x31},
		{"Ç", 'Ç', 0x12, 0x32},
		{"Ê", 'Ê', 0x12, 0x34},
		{"Ë", 'Ë', 0x12, 0x35},
		{"ë", 'ë', 0x12, 0x36},
		{"Ã", 'Ã', 0x13, 0x20},
		{"ã", 'ã', 0x13, 0x21},
		{"Í", 'Í', 0x13, 0x22},
		{"Ò", 'Ò', 0x13, 0x25},
		{"Õ", 'Õ', 0x13, 0x27},
		{"ä", 'ä', 0x13, 0x31},
		{"ö", 'ö', 0x13, 0x33},
		{"ß", 'ß', 0x13, 0x34},
		{"Å", 'Å', 0x13, 0x38},
		{"Ø", 'Ø', 0x13, 0x3A},
		{"ø", 'ø', 0x13, 0x3B},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(2)
			enc.IngestText(string(tc.r))

			pairs := drainEncoder(enc, 3)
			got := flattenPairs(pairs)
			want := []byte{oddParity(tc.prefix), oddParity(tc.data)}

			if !containsSeq(got, want) {
				t.Fatalf("rune %q: bytes = % 02X; want subseq % 02X (raw prefix=%02X, data=%02X)",
					string(tc.r), got, want, tc.prefix, tc.data)
			}
		})
	}
}

func TestEncoder_ExtendedSequenceInsideText(t *testing.T) {
	// "Hüt" — H basic, ü extended (0x12 0x25), t basic.
	// Per CEA-608-E §8.5.4–5.6, the 2-byte extended code must occupy a
	// single transmitted pair with 0x12 in byte[0]. Because 'H' is a
	// single basic byte (odd position), the encoder must pad it with 0x80
	// before emitting the extended pair, otherwise the 0x12 prefix would
	// land in byte[1] and conformant decoders would drop/garble the 'ü'.
	enc := NewEncoder(2)
	enc.IngestText("Hüt")

	pairs := drainEncoder(enc, 3)
	got := flattenPairs(pairs)

	want := []byte{
		oddParity('H'), 0x80,
		oddParity(0x12), oddParity(0x25),
		oddParity('t'), 0x80,
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("Hüt bytes = % 02X, want % 02X", got, want)
	}
}

func TestEncoder_TwoByteCodeAlwaysInPairByteZero(t *testing.T) {
	// For every extended/special code emitted, the control prefix (0x11/0x12/0x13)
	// must sit in byte[0] of a transmitted pair. Exercise multiple basic/extended
	// interleavings so both odd- and even-position extended codes are covered.
	inputs := []string{
		"Hüt",    // single extended mid-word, odd position
		"aßz",    // extended at odd position (different table, 0x13)
		"©abc",   // extended at position 0 (already even)
		"ab©cd",  // extended at even position
		"a©b©c",  // multiple extendeds
		"Hüllö",  // multiple extendeds, mixed
		"¿Hola?", // special at start, basic rest
	}
	controlPrefixes := map[byte]bool{0x11: true, 0x12: true, 0x13: true}

	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			enc := NewEncoder(2)
			enc.IngestText(in)

			pairs := drainEncoder(enc, 3)
			for i, p := range pairs {
				b1 := p.Data[1] & 0x7F
				if controlPrefixes[b1] {
					t.Errorf("pair %d: control prefix 0x%02X found in byte[1] of pair (% 02X); "+
						"spec requires it in byte[0]", i, b1, p.Data)
				}
			}
		})
	}
}

func TestEncoder_SpecialCharactersMapped(t *testing.T) {
	// Special chars use 0x11 prefix (CC1 channel 1).
	cases := []struct {
		name string
		r    rune
		data byte
	}{
		{"®", '®', 0x30},
		{"°", '°', 0x31},
		{"½", '½', 0x32},
		{"¿", '¿', 0x33},
		{"™", '™', 0x34},
		{"¢", '¢', 0x35},
		{"£", '£', 0x36},
		{"♪", '♪', 0x37},
		{"à", 'à', 0x38},
		{"è", 'è', 0x3A},
		{"â", 'â', 0x3B},
		{"ê", 'ê', 0x3C},
		{"î", 'î', 0x3D},
		{"ô", 'ô', 0x3E},
		{"û", 'û', 0x3F},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(2)
			enc.IngestText(string(tc.r))

			pairs := drainEncoder(enc, 3)
			got := flattenPairs(pairs)
			want := []byte{oddParity(0x11), oddParity(tc.data)}

			if !containsSeq(got, want) {
				t.Fatalf("rune %q: bytes = % 02X; want subseq % 02X (raw prefix=11, data=%02X)",
					string(tc.r), got, want, tc.data)
			}
		})
	}
}

func TestEncoder_CopyrightExtended(t *testing.T) {
	// © is in the extended Spanish/Misc table: 0x12 0x2B.
	enc := NewEncoder(2)
	enc.IngestText("©")

	pairs := drainEncoder(enc, 3)
	got := flattenPairs(pairs)
	want := []byte{oddParity(0x12), oddParity(0x2B)}

	if !containsSeq(got, want) {
		t.Fatalf("© bytes = % 02X; want subseq % 02X", got, want)
	}
}

func TestEncoder_SmartQuotesMappedToASCII(t *testing.T) {
	enc := NewEncoder(2)
	enc.IngestText("\u201Chi\u201D\u2019")

	pairs := drainEncoder(enc, 3)
	got := flattenPairs(pairs)
	want := []byte{
		oddParity('"'), oddParity('h'),
		oddParity('i'), oddParity('"'),
		oddParity('\''), 0x80,
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("smart quotes bytes = % 02X; want % 02X", got, want)
	}
}

func TestEncoder_EmDashMappedToDoubleHyphen(t *testing.T) {
	enc := NewEncoder(2)
	enc.IngestText("a\u2014b")

	pairs := drainEncoder(enc, 3)
	got := flattenPairs(pairs)
	want := []byte{
		oddParity('a'), oddParity('-'),
		oddParity('-'), oddParity('b'),
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("em-dash bytes = % 02X; want % 02X", got, want)
	}
}

func TestEncoder_EnDashMappedToSingleHyphen(t *testing.T) {
	enc := NewEncoder(2)
	enc.IngestText("x\u2013y")

	pairs := drainEncoder(enc, 3)
	got := flattenPairs(pairs)
	want := []byte{
		oddParity('x'), oddParity('-'),
		oddParity('y'), 0x80,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("en-dash bytes = % 02X; want % 02X", got, want)
	}
}

func TestEncoder_EllipsisMappedToThreeDots(t *testing.T) {
	enc := NewEncoder(2)
	enc.IngestText("Hi\u2026")

	pairs := drainEncoder(enc, 3)
	got := flattenPairs(pairs)
	want := []byte{
		oddParity('H'), oddParity('i'),
		oddParity('.'), oddParity('.'),
		oddParity('.'), 0x80,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ellipsis bytes = % 02X; want % 02X", got, want)
	}
}

func TestEncoder_DiacriticFallbackStripsAccent(t *testing.T) {
	cases := []struct {
		in   rune
		want byte
	}{
		{'ą', 'a'},
		{'ř', 'r'},
		{'ě', 'e'},
		{'ł', 'l'},
		{'ė', 'e'},
		{'ć', 'c'},
		{'ń', 'n'},
		{'ý', 'y'},
		{'ž', 'z'},
		{'š', 's'},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			enc := NewEncoder(2)
			enc.IngestText(string(tc.in))

			pairs := drainEncoder(enc, 3)
			got := flattenPairs(pairs)
			want := []byte{oddParity(tc.want), 0x80}
			if !bytes.Equal(got, want) {
				t.Fatalf("rune %q: bytes = % 02X; want % 02X", string(tc.in), got, want)
			}
		})
	}
}

func TestEncoder_UnsupportedCharFallsBack(t *testing.T) {
	logBuf, restore := captureSlog(t)
	defer restore()

	enc := NewEncoder(2)
	enc.IngestText("Привет")

	pairs := drainEncoder(enc, 3)
	got := flattenPairs(pairs)

	want := []byte{
		oddParity('?'), oddParity('?'),
		oddParity('?'), oddParity('?'),
		oddParity('?'), oddParity('?'),
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Cyrillic bytes = % 02X; want % 02X", got, want)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "caption: characters substituted") {
		t.Fatalf("expected substitution warning in logs, got: %q", logs)
	}
	if c := strings.Count(logs, "caption: characters substituted"); c != 1 {
		t.Fatalf("expected 1 warning log, got %d: %q", c, logs)
	}
}

func TestEncoder_NoSilentDrop(t *testing.T) {
	inputs := []string{
		"日本語",     // CJK
		"العربية", // Arabic
		"한국어",     // Korean
		"\u2603",  // snowman
		"\u00B6",  // pilcrow ¶
		"\u00A7",  // section §
		"\u20AC",  // euro €
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			_, restore := captureSlog(t)
			defer restore()

			enc := NewEncoder(2)
			enc.IngestText(in)

			pairs := drainEncoder(enc, 3)
			runeCount := 0
			for range in {
				runeCount++
			}

			byteCount := 0
			for _, p := range pairs {
				if p.Data[0] != 0x80 {
					byteCount++
				}
				if p.Data[1] != 0x80 {
					byteCount++
				}
			}
			if byteCount < runeCount {
				t.Fatalf("input %q (%d runes) produced %d bytes; characters were silently dropped",
					in, runeCount, byteCount)
			}
		})
	}
}

func TestEncoder_RateLimitedWarning(t *testing.T) {
	logBuf, restore := captureSlog(t)
	defer restore()

	enc := NewEncoder(2)
	enc.IngestText("Привет")
	enc.IngestText("мир")

	logs := logBuf.String()
	if c := strings.Count(logs, "caption: characters substituted"); c != 1 {
		t.Fatalf("expected exactly 1 warning across two calls within rate-limit window, got %d: %q", c, logs)
	}

	enc.mu.Lock()
	enc.lastSubWarnAt = time.Now().Add(-2 * time.Minute)
	enc.mu.Unlock()

	enc.IngestText("больше")
	logs = logBuf.String()
	if c := strings.Count(logs, "caption: characters substituted"); c != 2 {
		t.Fatalf("after window elapsed, expected 2 total warnings, got %d: %q", c, logs)
	}
}

func TestEncoder_TwoByteParityCorrect(t *testing.T) {
	// Verify each emitted data byte has odd parity per CEA-608 spec.
	testRunes := []rune{'ü', 'É', 'Á', '©', '®', '°', '½', 'Ç', 'Ã', 'ä', 'ö', 'ß'}

	for _, r := range testRunes {
		t.Run(string(r), func(t *testing.T) {
			enc := NewEncoder(2)
			enc.IngestText(string(r))

			pairs := drainEncoder(enc, 3)
			for i, p := range pairs {
				for j := 0; j < 2; j++ {
					b := p.Data[j]
					if b == 0x80 {
						continue
					}
					if bits.OnesCount8(b)%2 != 1 {
						t.Errorf("rune %q pair %d byte %d (0x%02X) has even parity (count=%d), want odd",
							string(r), i, j, b, bits.OnesCount8(b))
					}
				}
			}
		})
	}
}
