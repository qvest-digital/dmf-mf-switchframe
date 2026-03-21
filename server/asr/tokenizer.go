package asr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	vocabSize       = 51865
	eotToken        = 50257
	sotToken        = 50258
	translateToken  = 50358
	transcribeToken = 50360
	noSpeechToken   = 50363
	noTimestamps    = 50364
	firstLangToken  = 50259
	lastLangToken   = 50357
	firstTimestamp  = 50365
)

// languageOffsets maps ISO 639-1 language codes to their offset from firstLangToken.
// Whisper multilingual supports 99 languages.
var languageOffsets = map[string]int{
	"en": 0, "zh": 1, "de": 2, "es": 3, "ru": 4,
	"ko": 5, "fr": 6, "ja": 7, "pt": 8, "tr": 9,
	"pl": 10, "ca": 11, "nl": 12, "ar": 13, "sv": 14,
	"it": 15, "id": 16, "hi": 17, "fi": 18, "vi": 19,
	"he": 20, "uk": 21, "el": 22, "ms": 23, "cs": 24,
	"ro": 25, "da": 26, "hu": 27, "ta": 28, "no": 29,
	"th": 30, "ur": 31, "hr": 32, "bg": 33, "lt": 34,
	"la": 35, "mi": 36, "ml": 37, "cy": 38, "sk": 39,
	"te": 40, "fa": 41, "lv": 42, "bn": 43, "sr": 44,
	"az": 45, "sl": 46, "kn": 47, "et": 48, "mk": 49,
	"br": 50, "eu": 51, "is": 52, "hy": 53, "ne": 54,
	"mn": 55, "bs": 56, "kk": 57, "sq": 58, "sw": 59,
	"gl": 60, "mr": 61, "pa": 62, "si": 63, "km": 64,
	"sn": 65, "yo": 66, "so": 67, "af": 68, "oc": 69,
	"ka": 70, "be": 71, "tg": 72, "sd": 73, "gu": 74,
	"am": 75, "yi": 76, "lo": 77, "uz": 78, "fo": 79,
	"ht": 80, "ps": 81, "tk": 82, "nn": 83, "mt": 84,
	"sa": 85, "lb": 86, "my": 87, "bo": 88, "tl": 89,
	"mg": 90, "as": 91, "tt": 92, "haw": 93, "ln": 94,
	"ha": 95, "ba": 96, "jw": 97, "su": 98,
}

// Tokenizer decodes Whisper BPE token IDs to text.
// The vocabulary is loaded at runtime from a vocab.json file.
type Tokenizer struct {
	vocab []string // token ID -> decoded string
}

// NewTokenizer creates a Tokenizer. Call LoadVocab before decoding regular tokens.
func NewTokenizer() *Tokenizer {
	return &Tokenizer{}
}

// LoadVocab loads the BPE vocabulary from a vocab.json file in modelDir.
// The file is a JSON object mapping token strings to integer IDs: {"token": id, ...}
func (t *Tokenizer) LoadVocab(modelDir string) error {
	path := filepath.Join(modelDir, "vocab.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("asr: read vocab.json: %w", err)
	}

	var vocabMap map[string]int
	if err := json.Unmarshal(data, &vocabMap); err != nil {
		return fmt.Errorf("asr: parse vocab.json: %w", err)
	}

	t.vocab = make([]string, vocabSize)
	for token, id := range vocabMap {
		if id >= 0 && id < vocabSize {
			t.vocab[id] = token
		}
	}
	return nil
}

// Decode converts token IDs to text, filtering out special tokens.
func (t *Tokenizer) Decode(tokens []int) string {
	var sb strings.Builder
	for _, id := range tokens {
		if t.IsSpecial(id) {
			continue
		}
		if id < 0 || id >= len(t.vocab) {
			continue
		}
		token := t.vocab[id]
		decoded := decodeBPEToken(token)
		sb.WriteString(decoded)
	}
	return strings.TrimSpace(sb.String())
}

// decodeBPEToken converts a GPT-2 byte-level BPE token string to plain text.
// GPT-2 BPE maps bytes 0-255 to printable Unicode code points to avoid whitespace
// and control character issues in the vocabulary file. The key mappings:
//
//	U+0120 ('Ġ') = space (0x20)
//	U+010A ('Ċ') = newline (0x0A)
//	U+0109 ('ĉ') = tab (0x09)
func decodeBPEToken(token string) string {
	var sb strings.Builder
	for _, r := range token {
		switch {
		case r == 'Ġ': // U+0120 = space prefix
			sb.WriteByte(' ')
		case r == 'Ċ': // U+010A = newline
			sb.WriteByte('\n')
		case r == 'ĉ': // U+0109 = tab
			sb.WriteByte('\t')
		case r >= 0x100 && r <= 0x1FF:
			// Extended byte mapping: U+0100..U+01FF -> bytes.
			// GPT-2 maps non-printable bytes to this range.
			sb.WriteByte(byte(r - 0x100))
		default:
			if r < utf8.RuneSelf {
				sb.WriteByte(byte(r))
			} else {
				// Actual Unicode character — write as-is.
				sb.WriteRune(r)
			}
		}
	}
	return sb.String()
}

// IsSpecial returns true if the token ID is a special token (EOT, SOT, language,
// translate, transcribe, nospeech, notimestamps, or timestamp).
func (t *Tokenizer) IsSpecial(id int) bool {
	return id >= eotToken
}

// EOT returns the end-of-text token ID.
func (t *Tokenizer) EOT() int { return eotToken }

// SOT returns the start-of-transcript token ID.
func (t *Tokenizer) SOT() int { return sotToken }

// Transcribe returns the transcribe task token ID.
func (t *Tokenizer) Transcribe() int { return transcribeToken }

// NoTimestamps returns the no-timestamps token ID.
func (t *Tokenizer) NoTimestamps() int { return noTimestamps }

// NoSpeech returns the no-speech token ID.
func (t *Tokenizer) NoSpeech() int { return noSpeechToken }

// VocabSize returns the total vocabulary size (51865 for Whisper multilingual).
func (t *Tokenizer) VocabSize() int { return vocabSize }

// LanguageToken returns the token ID for the given ISO 639-1 language code.
// Falls back to English if the language is unknown.
func (t *Tokenizer) LanguageToken(lang string) int {
	if offset, ok := languageOffsets[lang]; ok {
		return firstLangToken + offset
	}
	return firstLangToken // fallback to English
}
