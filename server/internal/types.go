// Package internal provides shared types for the Switchframe server.
package internal

// SourceKeyConfig describes the upstream key configuration for a source,
// included in SourceInfo so the browser knows the current key state.
type SourceKeyConfig struct {
	Type           string  `json:"type"` // "chroma", "luma", or ""
	Enabled        bool    `json:"enabled"`
	KeyColorY      uint8   `json:"keyColorY,omitempty"`
	KeyColorCb     uint8   `json:"keyColorCb,omitempty"`
	KeyColorCr     uint8   `json:"keyColorCr,omitempty"`
	Similarity     float32 `json:"similarity,omitempty"`
	Smoothness     float32 `json:"smoothness,omitempty"`
	SpillSuppress  float32 `json:"spillSuppress,omitempty"`
	SpillReplaceCb uint8   `json:"spillReplaceCb,omitempty"`
	SpillReplaceCr uint8   `json:"spillReplaceCr,omitempty"`
	LowClip        float32 `json:"lowClip,omitempty"`
	HighClip       float32 `json:"highClip,omitempty"`
	Softness       float32 `json:"softness,omitempty"`
	FillSource     string  `json:"fillSource,omitempty"`
}

// SourceInfo describes a connected video source and its current state.
type SourceInfo struct {
	Key       string           `json:"key"`
	Label     string           `json:"label,omitempty"`
	Status    string           `json:"status"`
	Position  int              `json:"position"`
	DelayMs   int              `json:"delayMs,omitempty"`
	KeyConfig *SourceKeyConfig `json:"keyConfig,omitempty"`
	IsVirtual bool             `json:"isVirtual,omitempty"`
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
	GainReduction float64            `json:"gainReduction"`          // compressor GR in dB
	AudioDelayMs  int                `json:"audioDelayMs,omitempty"` // lip-sync delay (0-500ms)
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
// keyer (DSK) graphics overlay layers, included in ControlRoomState.
type GraphicsState struct {
	Layers []GraphicsLayerState `json:"layers,omitempty"`
}

// GraphicsLayerState is the JSON-serializable state for a single graphics layer.
type GraphicsLayerState struct {
	ID            int     `json:"id"`
	Template      string  `json:"template,omitempty"`
	Active        bool    `json:"active"`
	FadePosition  float64 `json:"fadePosition,omitempty"`
	AnimationMode string  `json:"animationMode,omitempty"`
	AnimationHz   float64 `json:"animationHz,omitempty"`
	ZOrder        int     `json:"zOrder"`
	X             int     `json:"x"`
	Y             int     `json:"y"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
}

// ReplayState is the JSON-serializable state for the instant replay system,
// included in ControlRoomState for the browser.
type ReplayState struct {
	State      string             `json:"state"`
	Source     string             `json:"source,omitempty"`
	Speed      float64            `json:"speed,omitempty"`
	Loop       bool               `json:"loop,omitempty"`
	Position   float64            `json:"position,omitempty"`
	MarkIn     *int64             `json:"markIn,omitempty"`  // Unix ms
	MarkOut    *int64             `json:"markOut,omitempty"` // Unix ms
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

// DestinationInfo describes an output destination for ControlRoomState broadcast.
type DestinationInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	Type           string `json:"type"`
	Address        string `json:"address,omitempty"`
	Port           int    `json:"port"`
	State          string `json:"state"`
	BytesWritten   int64  `json:"bytesWritten,omitempty"`
	DroppedPackets int64  `json:"droppedPackets,omitempty"`
	Connections    int    `json:"connections,omitempty"`
	Error          string `json:"error,omitempty"`
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

// PipelineFormatInfo describes the current video pipeline format.
type PipelineFormatInfo struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	FPSNum int    `json:"fpsNum"`
	FPSDen int    `json:"fpsDen"`
	Name   string `json:"name"`
}

// ControlRoomState is the full state of the switcher control room,
// broadcast to all connected browsers via the MoQ "control" track.
type ControlRoomState struct {
	ProgramSource        string                  `json:"programSource"`
	PreviewSource        string                  `json:"previewSource"`
	TransitionType       string                  `json:"transitionType"`
	TransitionDurationMs int                     `json:"transitionDurationMs,omitempty"`
	TransitionPosition   float64                 `json:"transitionPosition,omitempty"`
	TransitionEasing     string                  `json:"transitionEasing,omitempty"`
	InTransition         bool                    `json:"inTransition,omitempty"`
	FTBActive            bool                    `json:"ftbActive,omitempty"`
	AudioChannels        map[string]AudioChannel `json:"audioChannels"`
	MasterLevel          float64                 `json:"masterLevel"`
	ProgramPeak          [2]float64              `json:"programPeak"`
	GainReduction        float64                 `json:"gainReduction,omitempty"`
	MomentaryLUFS        float64                 `json:"momentaryLufs,omitempty"`
	ShortTermLUFS        float64                 `json:"shortTermLufs,omitempty"`
	IntegratedLUFS       float64                 `json:"integratedLufs,omitempty"`
	TallyState           map[string]string       `json:"tallyState"`
	Recording            *RecordingStatus        `json:"recording,omitempty"`
	SRTOutput            *SRTOutputStatus        `json:"srtOutput,omitempty"`
	Destinations         []DestinationInfo       `json:"destinations,omitempty"`
	Sources              map[string]SourceInfo   `json:"sources"`
	Presets              []PresetInfo            `json:"presets,omitempty"`
	Graphics             *GraphicsState          `json:"graphics,omitempty"`
	Layout               *LayoutState            `json:"layout,omitempty"`
	Replay               *ReplayState            `json:"replay,omitempty"`
	Operators            []OperatorInfo          `json:"operators,omitempty"`
	Locks                map[string]LockInfo     `json:"locks,omitempty"`
	PipelineFormat       *PipelineFormatInfo     `json:"pipelineFormat,omitempty"`
	SCTE35               *SCTE35State            `json:"scte35,omitempty"`
	Macro                *MacroExecutionState    `json:"macro,omitempty"`
	LastChangedBy        string                  `json:"lastChangedBy,omitempty"`
	Seq                  uint64                  `json:"seq"`
	Timestamp            int64                   `json:"timestamp"`
}

// MacroExecutionState represents the progress of a running macro.
type MacroExecutionState struct {
	Running     bool             `json:"running"`
	MacroName   string           `json:"macroName"`
	Steps       []MacroStepState `json:"steps"`
	CurrentStep int              `json:"currentStep"`
	Error       string           `json:"error,omitempty"`
}

// MacroStepState tracks the execution state of one macro step.
type MacroStepState struct {
	Action      string `json:"action"`
	Summary     string `json:"summary"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	WaitMs      int    `json:"waitMs,omitempty"`
	WaitStartMs int64  `json:"waitStartMs,omitempty"`
}

// SCTE35State represents the current SCTE-35 signaling state.
type SCTE35State struct {
	Enabled        bool                       `json:"enabled"`
	SCTE104Enabled bool                       `json:"scte104Enabled,omitempty"`
	ActiveEvents   map[uint32]SCTE35Active    `json:"activeEvents"`
	EventLog       []SCTE35Event              `json:"eventLog"`
	HeartbeatOK    bool                       `json:"heartbeatOk"`
	Config         SCTE35Config               `json:"config"`
}

// SCTE35Active describes an in-progress SCTE-35 event.
type SCTE35Active struct {
	EventID       uint32                 `json:"eventId"`
	CommandType   string                 `json:"commandType"`
	IsOut         bool                   `json:"isOut"`
	DurationMs    *int64                 `json:"durationMs,omitempty"`
	ElapsedMs     int64                  `json:"elapsedMs"`
	RemainingMs   *int64                 `json:"remainingMs,omitempty"`
	AutoReturn    bool                   `json:"autoReturn"`
	Held          bool                   `json:"held"`
	SpliceTimePTS int64                  `json:"spliceTimePts"`
	StartedAt     int64                  `json:"startedAt"`
	Descriptors   []SCTE35DescriptorInfo `json:"descriptors,omitempty"`
}

// SCTE35DescriptorInfo describes a segmentation descriptor in an active event.
type SCTE35DescriptorInfo struct {
	SegEventID           uint32 `json:"segEventId"`
	SegmentationType     uint8  `json:"segmentationType"`
	SegmentationTypeName string `json:"segmentationTypeName"`
	UPIDType             uint8  `json:"upidType"`
	UPIDTypeName         string `json:"upidTypeName"`
	UPID                 string `json:"upid"`
	DurationMs           *int64 `json:"durationMs,omitempty"`
	SubSegmentNum        uint8  `json:"subSegmentNum,omitempty"`
	SubSegmentsExpected  uint8  `json:"subSegmentsExpected,omitempty"`
	Cancelled            bool   `json:"cancelled,omitempty"`
}

// SCTE35Event describes a logged SCTE-35 event.
type SCTE35Event struct {
	EventID        uint32                 `json:"eventId"`
	CommandType    string                 `json:"commandType"`
	IsOut          bool                   `json:"isOut"`
	DurationMs     *int64                 `json:"durationMs,omitempty"`
	AutoReturn     bool                   `json:"autoReturn"`
	Descriptors    []SCTE35DescriptorInfo `json:"descriptors,omitempty"`
	AvailNum       *uint8                 `json:"availNum,omitempty"`
	AvailsExpected *uint8                 `json:"availsExpected,omitempty"`
	SpliceTimePTS  *int64                 `json:"spliceTimePts,omitempty"`
	Timestamp      int64                  `json:"timestamp"`
	Status         string                 `json:"status"`
	Source         string                 `json:"source,omitempty"`
	DestinationID  string                 `json:"destinationId,omitempty"`
}

// SCTE35Config describes the SCTE-35 injector configuration.
type SCTE35Config struct {
	HeartbeatIntervalMs int64  `json:"heartbeatIntervalMs"`
	DefaultPreRollMs    int64  `json:"defaultPreRollMs"`
	PID                 uint16 `json:"pid"`
	VerifyEncoding      bool   `json:"verifyEncoding"`
	WebhookURL          string `json:"webhookUrl,omitempty"`
}

// LayoutState represents the current layout configuration for state broadcast.
type LayoutState struct {
	ActivePreset string            `json:"activePreset"`
	Slots        []LayoutSlotState `json:"slots"`
}

// LayoutSlotState represents a single layout slot in the state broadcast.
type LayoutSlotState struct {
	ID         int        `json:"id"`
	SourceKey  string     `json:"sourceKey"`
	Enabled    bool       `json:"enabled"`
	X          int        `json:"x"`
	Y          int        `json:"y"`
	Width      int        `json:"width"`
	Height     int        `json:"height"`
	ZOrder     int        `json:"zOrder"`
	Animating  bool       `json:"animating,omitempty"`
	ScaleMode  string     `json:"scaleMode,omitempty"`
	CropAnchor [2]float64 `json:"cropAnchor,omitempty"`
}
