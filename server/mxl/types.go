package mxl

import "errors"

// ErrMXLNotAvailable is returned by stub constructors when the mxl build tag
// is not set. Callers should check for this at startup and provide a clear
// message to the user.
var ErrMXLNotAvailable = errors.New("MXL support not compiled (build with -tags mxl)")

// FlowMode specifies whether a flow is opened for reading or writing.
type FlowMode int

const (
	FlowModeReader FlowMode = iota
	FlowModeWriter
)

// DataFormat identifies the media type of an MXL flow.
type DataFormat int

const (
	DataFormatUnspecified DataFormat = iota
	DataFormatVideo                 // Discrete grain-based (V210)
	DataFormatAudio                 // Continuous sample-based (Float32)
	DataFormatData                  // Discrete grain-based (ANC/metadata)
)

// Rational represents a rational number (e.g., frame rate 30000/1001).
type Rational struct {
	Numerator   int64
	Denominator int64
}

// Float64 returns the rational as a float64.
func (r Rational) Float64() float64 {
	if r.Denominator == 0 {
		return 0
	}
	return float64(r.Numerator) / float64(r.Denominator)
}

// FlowConfig describes an MXL flow's configuration.
type FlowConfig struct {
	Format    DataFormat
	GrainRate Rational // Frame rate for video, sample rate for audio

	// Discrete flow fields (video/data).
	SliceSizes [4]uint32 // Per-plane byte sizes
	GrainCount uint32    // Ring buffer depth in grains

	// Continuous flow fields (audio).
	ChannelCount uint32 // Number of audio channels
	BufferLength uint32 // Samples per channel in ring buffer
}

// FlowInfo describes a discovered MXL flow.
type FlowInfo struct {
	ID         string     // Flow UUID
	Name       string     // Human-readable label from flow definition
	Format     DataFormat // Video, audio, or data
	MediaType  string     // e.g. "video/v210", "audio/float32"
	Width      int        // Video only
	Height     int        // Video only
	SampleRate int        // Audio only
	Channels   int        // Audio only
	GrainRate  Rational   // Frame/sample rate
	Active     bool       // Whether a writer is currently attached
}

// GrainInfo carries metadata about a read or written grain.
type GrainInfo struct {
	Index       uint64
	GrainSize   uint32
	TotalSlices uint16
	ValidSlices uint16
	Invalid     bool // MXL_GRAIN_FLAG_INVALID
}

// FlowOpener creates flow readers and writers from an MXL instance.
// Implemented by Instance (cgo) and mocks (tests).
type FlowOpener interface {
	// OpenReader opens a flow for reading by flow UUID.
	OpenReader(flowID string) (DiscreteReader, error)

	// OpenAudioReader opens an audio flow for reading by flow UUID.
	OpenAudioReader(flowID string) (ContinuousReader, error)

	// OpenWriter opens a flow for writing with the given JSON flow definition.
	OpenWriter(flowDef string) (DiscreteWriter, error)

	// OpenAudioWriter opens an audio flow for writing with the given JSON flow definition.
	OpenAudioWriter(flowDef string) (ContinuousWriter, error)

	// GetFlowConfig returns the configuration for a flow by ID.
	GetFlowConfig(flowID string) (FlowConfig, error)

	// IsFlowActive checks whether a flow has an active writer.
	IsFlowActive(flowID string) (bool, error)

	// Close releases the MXL instance.
	Close() error
}

// DiscreteReader reads video/data grains from an MXL flow.
// The returned data is a copy — safe to retain after the next read.
type DiscreteReader interface {
	// ReadGrain reads the grain at the given index. Blocks up to timeoutNs.
	// Returns the grain payload (copied from shared memory) and metadata.
	ReadGrain(index uint64, timeoutNs uint64) ([]byte, GrainInfo, error)

	// ConfigInfo returns the flow configuration.
	ConfigInfo() FlowConfig

	// HeadIndex returns the current write-head position in the ring buffer.
	HeadIndex() (uint64, error)

	// Close releases the reader.
	Close() error
}

// ContinuousReader reads audio samples from an MXL flow.
type ContinuousReader interface {
	// ReadSamples reads count samples starting at index. Blocks up to timeoutNs.
	// Returns de-interleaved float32 channels (one slice per channel).
	ReadSamples(index uint64, count int, timeoutNs uint64) ([][]float32, error)

	// ConfigInfo returns the flow configuration.
	ConfigInfo() FlowConfig

	// Close releases the reader.
	Close() error
}

// DiscreteWriter writes video/data grains to an MXL flow.
type DiscreteWriter interface {
	// WriteGrain writes a grain at the given index.
	WriteGrain(index uint64, data []byte) error

	// Close releases the writer.
	Close() error
}

// ContinuousWriter writes audio samples to an MXL flow.
type ContinuousWriter interface {
	// WriteSamples writes de-interleaved float32 channels at the given index.
	WriteSamples(index uint64, channels [][]float32) error

	// Close releases the writer.
	Close() error
}

// CurrentIndex returns the current grain/sample index for the given rate.
// This is a time function — wraps mxlGetCurrentIndex in cgo builds,
// uses a monotonic clock approximation in stub builds.
var CurrentIndex func(rate Rational) uint64
