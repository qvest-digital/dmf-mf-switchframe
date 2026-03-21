package gpu

// KeyBridge provides key processor state to the GPU key node.
// Implemented by an adapter wrapping graphics.KeyProcessorBridge.
type KeyBridge interface {
	HasEnabledKeysWithFills() bool
	HasEnabledKeys() bool // checks if any keys are configured+enabled, regardless of CPU fills
	SnapshotEnabledKeys() []EnabledKeySnapshot
	GPUFill(sourceKey string) *GPUFrame // returns ref'd frame from GPU cache, or nil
}

// EnabledKeySnapshot is a deep-copied snapshot of one enabled key's state.
type EnabledKeySnapshot struct {
	SourceKey      string
	Type           string // "chroma" or "luma"
	KeyCb, KeyCr   uint8
	Similarity     float32
	Smoothness     float32
	SpillSuppress  float32
	SpillReplaceCb uint8
	SpillReplaceCr uint8
	LowClip        float32
	HighClip       float32
	Softness       float32
	FillYUV        []byte // YUV420p fill frame (deep copy)
	FillW, FillH   int
}

// CompositorState provides graphics compositor state to the GPU DSK node.
// Implemented by an adapter wrapping graphics.Compositor.
type CompositorState interface {
	HasActiveLayers() bool
	SnapshotVisibleLayers() []VisibleLayerSnapshot
}

// VisibleLayerSnapshot is a deep-copied snapshot of one visible layer's state.
type VisibleLayerSnapshot struct {
	ID               int
	Rect             Rect
	Alpha            float32 // 0.0-1.0 (fade position)
	Overlay          []byte  // RGBA pixel data (deep copy)
	OverlayW, OverlayH int
	Gen              uint64 // generation counter for cache invalidation
}

// LayoutState provides layout compositor state to the GPU layout node.
// Implemented by an adapter wrapping layout.Compositor.
type LayoutState interface {
	Active() bool
	SnapshotSlots() []SlotSnapshot
	GPUFill(sourceKey string) *GPUFrame // returns ref'd frame from GPU cache, or nil
}

// SlotSnapshot is a deep-copied snapshot of one layout slot's state.
type SlotSnapshot struct {
	Index        int
	Enabled      bool
	SourceKey    string
	Rect         Rect
	FillYUV      []byte // YUV420p source frame (deep copy, nil if no signal)
	FillW, FillH int
	FillPTS      int64
	Border       BorderSnapshot
	Alpha        float32
	ScaleMode    string     // "stretch" (default) or "fill"
	CropAnchor   [2]float64 // [x,y] 0.0-1.0, anchor point for crop (0.5,0.5 = center)
}

// BorderSnapshot holds border configuration for a layout slot.
type BorderSnapshot struct {
	ColorY, ColorCb, ColorCr uint8
	Thickness                int
}

// STMapState provides ST map state to the GPU stmap node.
// Implemented by stmap.Registry directly (no circular dependency).
type STMapState interface {
	HasProgramMap() bool
	ProgramMapName() string
	ProgramSTArrays() (s, t []float32)
	IsAnimated() bool
	AdvanceAnimatedIndex() int
	AnimatedSTArraysAt(idx int) (s, t []float32)
}

// SourceSTMapProvider provides per-source ST map state to the GPU source manager.
// Implemented by stmap.Registry via SourceMapName and SourceSTArrays methods.
type SourceSTMapProvider interface {
	// SourceMapName returns the map name assigned to a source, or "" if none.
	SourceMapName(sourceKey string) string
	// SourceSTArrays returns the S and T float32 arrays for the source's
	// assigned map, or (nil, nil) if no map is assigned.
	SourceSTArrays(sourceKey string) (s, t []float32)
}

// SegmentationState provides AI segmentation state for the GPU pipeline node.
// Implemented by an adapter bridging SegmentationEngine + Switcher.
type SegmentationState interface {
	// HasEnabledSources returns true if any source has AI segmentation active.
	HasEnabledSources() bool
	// ProgramSourceKey returns the current program source key.
	ProgramSourceKey() string
	// MaskForSource returns the latest GPU-resident segmentation mask for the source.
	// Returns nil if no mask is available. The returned GPUFrame is a uint8
	// single-plane mask (255 = foreground, 0 = background).
	MaskForSource(key string) *GPUFrame
	// ConfigForSource returns the AI segmentation config for a source.
	// Returns nil if the source has no AI segmentation config.
	ConfigForSource(key string) *AISegmentConfig
}

// AISegmentConfig holds the background replacement configuration for a source.
type AISegmentConfig struct {
	Background  string  // "transparent"|"blur:N"|"color:RRGGBB"
	Sensitivity float32 // 0.0-1.0 (maps to segmentation threshold)
	EdgeSmooth  float32 // 0.0-1.0 (maps to EMA temporal smoothing alpha)
}

// PreviewConfig configures per-source GPU preview encoding.
type PreviewConfig struct {
	Width, Height  int
	Bitrate        int
	FPSNum, FPSDen int
	OnPreview      func(data []byte, isIDR bool, pts int64)
}
