package caption

import "testing"

func TestModeString(t *testing.T) {
	tests := []struct {
		mode Mode
		want string
	}{
		{ModeOff, "off"},
		{ModePassThrough, "passthrough"},
		{ModeAuthor, "author"},
		{Mode(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.want {
			t.Errorf("Mode(%d).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		input string
		want  Mode
		ok    bool
	}{
		{"off", ModeOff, true},
		{"passthrough", ModePassThrough, true},
		{"author", ModeAuthor, true},
		{"invalid", ModeOff, false},
		{"", ModeOff, false},
	}
	for _, tt := range tests {
		got, ok := ParseMode(tt.input)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseMode(%q) = (%v, %v), want (%v, %v)", tt.input, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCCPairIsNull(t *testing.T) {
	if !NullPair().IsNull() {
		t.Error("NullPair should be null")
	}
	if (CCPair{Data: [2]byte{0x41, 0x42}}).IsNull() {
		t.Error("non-null pair should not be null")
	}
}
