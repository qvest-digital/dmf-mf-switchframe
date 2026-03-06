// Package internal provides shared types for the Switchframe server.
package internal

// SourceKeyConfig describes the upstream key configuration for a source,
// included in SourceInfo so the browser knows the current key state.
type SourceKeyConfig struct {
	Type          string  `json:"type"`                    // "chroma", "luma", or ""
	Enabled       bool    `json:"enabled"`
	KeyColorY     uint8   `json:"keyColorY,omitempty"`
	KeyColorCb    uint8   `json:"keyColorCb,omitempty"`
	KeyColorCr    uint8   `json:"keyColorCr,omitempty"`
	Similarity    float32 `json:"similarity,omitempty"`
	Smoothness    float32 `json:"smoothness,omitempty"`
	SpillSuppress float32 `json:"spillSuppress,omitempty"`
	LowClip       float32 `json:"lowClip,omitempty"`
	HighClip      float32 `json:"highClip,omitempty"`
	Softness      float32 `json:"softness,omitempty"`
	FillSource    string  `json:"fillSource,omitempty"`
}

// SourceInfo describes a connected video source and its current state.
type SourceInfo struct {
	Key       string             `json:"key"`
	Label     string             `json:"label,omitempty"`
	Status    string             `json:"status"`
	DelayMs   int                `json:"delayMs,omitempty"`
	KeyConfig *SourceKeyConfig   `json:"keyConfig,omitempty"`
	IsVirtual bool               `json:"isVirtual,omitempty"`
}

// EQBand describes the settings for a single EQ band.
type EQBand struct {
	Frequency float64 `json:"frequency"`
	Gain      float64 `json:"gain"`
	Q         float64 `json:"q"`
	Enabled   bool    `json:"enabled"`
}

// CompressorSettings describes the settings for a channel compressor.
type CompressorSettings struct {
	Threshold  float64 `json:"threshold"`
	Ratio      float64 `json:"ratio"`
	Attack     float64 `json:"attack"`
	Release    float64 `json:"release"`
	MakeupGain float64 `json:"makeupGain"`
}

// AudioChannel describes the audio mixer state for a single source.
type AudioChannel struct {
	Level         float64            `json:"level"` // dB (-inf to +12)
	Trim          float64            `json:"trim"`  // dB (-20 to +20), input gain
	Muted         bool               `json:"muted"`
	AFV           bool               `json:"afv"`   // audio-follows-video
	PeakL         float64            `json:"peakL"` // dBFS, updated per frame
	PeakR         float64            `json:"peakR"` // dBFS
	EQ            [3]EQBand          `json:"eq"`
	Compressor    CompressorSettings `json:"compressor"`
	GainReduction float64            `json:"gainReduction"` // compressor GR in dB
}

// RecordingStatus is the JSON-serializable status for recording output,
// included in ControlRoomState for the browser.
type RecordingStatus struct {
	Active         bool    `json:"active"`
	Filename       string  `json:"filename,omitempty"`
	BytesWritten   int64   `json:"bytesWritten,omitempty"`
	DurationSecs   float64 `json:"durationSecs,omitempty"`
	DroppedPackets int64   `json:"droppedPackets,omitempty"`
	Error          string  `json:"error,omitempty"`
}

// SRTOutputStatus is the JSON-serializable status for SRT output,
// included in ControlRoomState for the browser.
type SRTOutputStatus struct {
	Active         bool   `json:"active"`
	Mode           string `json:"mode,omitempty"`
	Address        string `json:"address,omitempty"`
	Port           int    `json:"port,omitempty"`
	State          string `json:"state,omitempty"`
	Connections    int    `json:"connections,omitempty"`
	BytesWritten   int64  `json:"bytesWritten,omitempty"`
	DroppedPackets int64  `json:"droppedPackets,omitempty"`
	OverflowCount  int64  `json:"overflowCount,omitempty"`
	Error          string `json:"error,omitempty"`
}

// PresetInfo is a summary of a saved preset, included in ControlRoomState
// so the browser knows which presets are available for recall.
type PresetInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GraphicsState is the JSON-serializable state for the downstream
// keyer (DSK) graphics overlay, included in ControlRoomState.
type GraphicsState struct {
	Active       bool    `json:"active"`
	Template     string  `json:"template,omitempty"`
	FadePosition float64 `json:"fadePosition,omitempty"`
}

// ReplayState is the JSON-serializable state for the instant replay system,
// included in ControlRoomState for the browser.
type ReplayState struct {
	State      string             `json:"state"`
	Source     string             `json:"source,omitempty"`
	Speed      float64            `json:"speed,omitempty"`
	Loop       bool               `json:"loop,omitempty"`
	Position   float64            `json:"position,omitempty"`
	MarkIn     *int64             `json:"markIn,omitempty"`     // Unix ms
	MarkOut    *int64             `json:"markOut,omitempty"`    // Unix ms
	MarkSource string             `json:"markSource,omitempty"`
	Buffers    []ReplayBufferInfo `json:"buffers,omitempty"`
}

// ReplayBufferInfo describes the buffer state for a single replay source.
type ReplayBufferInfo struct {
	Source       string  `json:"source"`
	FrameCount   int     `json:"frameCount"`
	GOPCount     int     `json:"gopCount"`
	DurationSecs float64 `json:"durationSecs"`
	BytesUsed    int64   `json:"bytesUsed"`
}

// OperatorInfo describes a registered operator for ControlRoomState broadcast.
type OperatorInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Connected bool   `json:"connected"`
}

// LockInfo describes an active subsystem lock for ControlRoomState broadcast.
type LockInfo struct {
	HolderID   string `json:"holderId"`
	HolderName string `json:"holderName"`
	AcquiredAt int64  `json:"acquiredAt"` // Unix ms
}

// ControlRoomState is the full state of the switcher control room,
// broadcast to all connected browsers via the MoQ "control" track.
type ControlRoomState struct {
	ProgramSource        string                    `json:"programSource"`
	PreviewSource        string                    `json:"previewSource"`
	TransitionType       string                    `json:"transitionType"`
	TransitionDurationMs int                       `json:"transitionDurationMs,omitempty"`
	TransitionPosition   float64                   `json:"transitionPosition,omitempty"`
	InTransition         bool                      `json:"inTransition,omitempty"`
	FTBActive            bool                      `json:"ftbActive,omitempty"`
	AudioChannels        map[string]AudioChannel   `json:"audioChannels"`
	MasterLevel          float64                   `json:"masterLevel"`
	ProgramPeak          [2]float64                `json:"programPeak"`
	GainReduction        float64                   `json:"gainReduction,omitempty"`
	TallyState           map[string]string          `json:"tallyState"`
	Recording            *RecordingStatus           `json:"recording,omitempty"`
	SRTOutput            *SRTOutputStatus           `json:"srtOutput,omitempty"`
	Sources              map[string]SourceInfo     `json:"sources"`
	Presets              []PresetInfo              `json:"presets,omitempty"`
	Graphics             *GraphicsState            `json:"graphics,omitempty"`
	Replay               *ReplayState              `json:"replay,omitempty"`
	Operators            []OperatorInfo            `json:"operators,omitempty"`
	Locks                map[string]LockInfo       `json:"locks,omitempty"`
	LastChangedBy        string                    `json:"lastChangedBy,omitempty"`
	Seq                  uint64                    `json:"seq"`
	Timestamp            int64                     `json:"timestamp"`
}
