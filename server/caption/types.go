package caption

// Mode represents the caption operating mode.
type Mode int

const (
	// ModeOff disables all caption processing.
	ModeOff Mode = iota
	// ModePassThrough forwards captions from the program source unchanged.
	ModePassThrough
	// ModeAuthor enables live caption authoring from keyboard input.
	ModeAuthor
)

// String returns the human-readable name of the mode.
func (m Mode) String() string {
	switch m {
	case ModeOff:
		return "off"
	case ModePassThrough:
		return "passthrough"
	case ModeAuthor:
		return "author"
	default:
		return "unknown"
	}
}

// ParseMode converts a string to a Mode.
func ParseMode(s string) (Mode, bool) {
	switch s {
	case "off":
		return ModeOff, true
	case "passthrough":
		return ModePassThrough, true
	case "author":
		return ModeAuthor, true
	default:
		return ModeOff, false
	}
}

// State represents the caption system state for ControlRoomState broadcast.
type State struct {
	Mode           string          `json:"mode"`
	AuthorBuffer   string          `json:"authorBuffer,omitempty"`
	SourceCaptions map[string]bool `json:"sourceCaptions,omitempty"`
}

// CCPair represents a CEA-608 closed caption byte pair.
// Each frame carries one pair (2 bytes) of caption data on each field.
type CCPair struct {
	Data [2]byte
}

// IsNull returns true if this is a null/padding pair (0x80, 0x80).
func (p CCPair) IsNull() bool {
	return p.Data[0] == 0x80 && p.Data[1] == 0x80
}

// NullPair returns a null caption pair used for padding.
func NullPair() CCPair {
	return CCPair{Data: [2]byte{0x80, 0x80}}
}
