package internal

// FullStateSnapshot captures the complete restorable engine state for
// multi-region active-active redundancy. When a browser detects sustained
// state divergence between engines, it pulls a full snapshot from the
// authoritative engine and pushes it to the lagging one.
//
// This is a "super preset" — it covers everything needed to reconstruct the
// complete engine state. Unlike ControlRoomState (which includes transient
// metering data for real-time display), this only includes restorable state.
//
// The Version field enables future schema evolution. The CapturedAt field
// (Unix microseconds) enables staleness detection.
type FullStateSnapshot struct {
	// Schema version for forward/backward compatibility.
	Version string `json:"version"`

	// Unix microseconds when this snapshot was captured.
	CapturedAt int64 `json:"capturedAt"`

	// --- Switching ---

	ProgramSource string `json:"programSource"`
	PreviewSource string `json:"previewSource"`

	// --- Transition ---

	TransitionType       string `json:"transitionType"`
	TransitionDurationMs int    `json:"transitionDurationMs"`
	TransitionEasing     string `json:"transitionEasing"`
	FTBActive            bool   `json:"ftbActive,omitempty"`

	// --- Audio (per-channel) ---

	// AudioChannels maps source key to its audio channel configuration.
	AudioChannels map[string]AudioChannelSnapshot `json:"audioChannels"`

	// --- Audio master bus ---

	AudioMaster AudioMasterSnapshot `json:"audioMaster"`

	// --- Graphics (DSK layers) ---

	Graphics []GraphicsLayerSnapshot `json:"graphics,omitempty"`

	// --- DVE / Layout ---

	Layout LayoutSnapshot `json:"layout"`

	// --- Captions ---

	// CaptionMode is "off", "passthrough", or "author".
	CaptionMode string `json:"captionMode"`

	// --- Clip players ---

	ClipPlayers []ClipPlayerSnapshot `json:"clipPlayers,omitempty"`

	// --- Source labels and positions ---

	// Sources maps source key to its label and position configuration.
	Sources map[string]SourceConfigSnapshot `json:"sources,omitempty"`

	// --- Upstream keys ---

	// UpstreamKeys maps source key to its upstream key configuration.
	UpstreamKeys map[string]UpstreamKeySnapshot `json:"upstreamKeys,omitempty"`

	// --- Color grading ---

	ColorGrade *ColorGradeSnapshot `json:"colorGrade,omitempty"`

	// --- ST map assignments ---

	STMaps *STMapSnapshot `json:"stmaps,omitempty"`
}

// AudioChannelSnapshot captures the restorable audio state for a single channel.
// Omits transient metering data (PeakL, PeakR, GainReduction, GateReduction).
type AudioChannelSnapshot struct {
	Level        float64            `json:"level"`
	Trim         float64            `json:"trim"`
	Muted        bool               `json:"muted"`
	AFV          bool               `json:"afv"`
	PhaseInvert  bool               `json:"phaseInvert,omitempty"`
	Balance      float64            `json:"balance"`
	HPFFrequency float64            `json:"hpfFrequency,omitempty"`
	HPFEnabled   bool               `json:"hpfEnabled,omitempty"`
	EQ           [3]EQBandSnapshot  `json:"eq"`
	Compressor   CompressorSnapshot `json:"compressor"`
	Gate         GateSnapshot       `json:"gate"`
	AudioDelayMs int                `json:"audioDelayMs,omitempty"`
}

// EQBandSnapshot captures the settings for a single parametric EQ band.
type EQBandSnapshot struct {
	Frequency float64 `json:"frequency"`
	Gain      float64 `json:"gain"`
	Q         float64 `json:"q"`
	Enabled   bool    `json:"enabled"`
}

// CompressorSnapshot captures channel compressor settings.
type CompressorSnapshot struct {
	Threshold  float64 `json:"threshold"`
	Ratio      float64 `json:"ratio"`
	Attack     float64 `json:"attack"`
	Release    float64 `json:"release"`
	MakeupGain float64 `json:"makeupGain"`
}

// GateSnapshot captures channel noise gate settings.
type GateSnapshot struct {
	Threshold float64 `json:"threshold"`
	Range     float64 `json:"range"`
	Attack    float64 `json:"attack"`
	Release   float64 `json:"release"`
	Hold      float64 `json:"hold"`
	Enabled   bool    `json:"enabled"`
}

// AudioMasterSnapshot captures the master bus settings.
// Omits transient data (ProgramPeak, GainReduction, LUFS meters).
type AudioMasterSnapshot struct {
	MasterLevel      float64 `json:"masterLevel"`
	LimiterThreshold float64 `json:"limiterThreshold"`
	LimiterCeiling   float64 `json:"limiterCeiling"`
}

// GraphicsLayerSnapshot captures the restorable state of a single DSK layer.
// Omits transient data (FadePosition, AnimationState, HTML5 status/data, sequence state).
type GraphicsLayerSnapshot struct {
	ID        int    `json:"id"`
	Active    bool   `json:"active"`
	ZOrder    int    `json:"zOrder"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Template  string `json:"template,omitempty"`
	ImageName string `json:"imageName,omitempty"`
}

// LayoutSnapshot captures the full DVE layout configuration.
type LayoutSnapshot struct {
	ActivePreset string            `json:"activePreset"`
	Slots        []DVESlotSnapshot `json:"slots,omitempty"`
}

// DVESlotSnapshot captures the full DVE transform for a single layout slot.
// Omits transient data (Animating flag).
type DVESlotSnapshot struct {
	SourceKey    string  `json:"sourceKey"`
	Enabled      bool    `json:"enabled"`
	ZOrder       int     `json:"zOrder"`
	PositionX    float64 `json:"positionX"`
	PositionY    float64 `json:"positionY"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	RotationDeg  float64 `json:"rotationDeg,omitempty"`
	AnchorX      float64 `json:"anchorX,omitempty"`
	AnchorY      float64 `json:"anchorY,omitempty"`
	PerspectiveX float64 `json:"perspectiveX,omitempty"`
	PerspectiveY float64 `json:"perspectiveY,omitempty"`
	CropTop      float64 `json:"cropTop,omitempty"`
	CropBottom   float64 `json:"cropBottom,omitempty"`
	CropLeft     float64 `json:"cropLeft,omitempty"`
	CropRight    float64 `json:"cropRight,omitempty"`
	CropSoftness float64 `json:"cropSoftness,omitempty"`
	Opacity      float64 `json:"opacity,omitempty"`
	ScaleMode    string  `json:"scaleMode,omitempty"`
	CropAnchor   [2]float64         `json:"cropAnchor,omitempty"`
	Border       *DVEBorderSnapshot `json:"border,omitempty"`
	Shadow       *DVEShadowSnapshot `json:"shadow,omitempty"`

	// Image effects
	DefocusRadius float64 `json:"defocusRadius,omitempty"`
	MirrorH       bool    `json:"mirrorH,omitempty"`
	MirrorV       bool    `json:"mirrorV,omitempty"`
	MosaicSize    int     `json:"mosaicSize,omitempty"`
	Saturation    float64 `json:"saturation,omitempty"`
	Brightness    float64 `json:"brightness,omitempty"`
	Contrast      float64 `json:"contrast,omitempty"`

	// 3D transforms
	Mode3D  string  `json:"mode3d,omitempty"`
	RotateX float64 `json:"rotateX,omitempty"`
	RotateY float64 `json:"rotateY,omitempty"`
	RotateZ float64 `json:"rotateZ,omitempty"`
}

// DVEBorderSnapshot captures DVE border configuration.
type DVEBorderSnapshot struct {
	Width        float64 `json:"width"`
	ColorY       byte    `json:"colorY"`
	ColorCb      byte    `json:"colorCb"`
	ColorCr      byte    `json:"colorCr"`
	CornerRadius float64 `json:"cornerRadius,omitempty"`
	Softness     float64 `json:"softness,omitempty"`
	InnerWidth   float64 `json:"innerWidth,omitempty"`
}

// DVEShadowSnapshot captures DVE shadow configuration.
type DVEShadowSnapshot struct {
	Enabled bool    `json:"enabled"`
	OffsetX float64 `json:"offsetX,omitempty"`
	OffsetY float64 `json:"offsetY,omitempty"`
	Blur    float64 `json:"blur,omitempty"`
	Opacity float64 `json:"opacity,omitempty"`
	ColorY  byte    `json:"colorY,omitempty"`
}

// ClipPlayerSnapshot captures the restorable state of a single clip player.
// Omits transient data (Position — playback cursor is not transferable).
type ClipPlayerSnapshot struct {
	ID     int     `json:"id"`
	ClipID string  `json:"clipId,omitempty"`
	State  string  `json:"state"`
	Speed  float64 `json:"speed,omitempty"`
	Loop   bool    `json:"loop,omitempty"`
}

// SourceConfigSnapshot captures the user-configurable per-source settings.
type SourceConfigSnapshot struct {
	Label    string `json:"label"`
	Position int    `json:"position"`
}

// UpstreamKeySnapshot captures the upstream key configuration for a source.
type UpstreamKeySnapshot struct {
	Type           string  `json:"type"`
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

// ColorGradeSnapshot captures the color grading configuration.
// Omits the Available list (static, not engine state).
type ColorGradeSnapshot struct {
	Enabled   bool    `json:"enabled"`
	LookName  string  `json:"lookName"`
	LookGroup string  `json:"lookGroup"`
	Intensity float32 `json:"intensity"`
}

// STMapSnapshot captures ST map source and program assignments.
type STMapSnapshot struct {
	// Sources maps source key to assigned ST map name.
	Sources map[string]string `json:"sources,omitempty"`
	// Program is the program-level ST map name (empty if none).
	Program string `json:"program,omitempty"`
}
