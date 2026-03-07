//go:build !cgo || !mxl

package mxl

import (
	"log/slog"
	"time"
)

func init() {
	slog.Debug("mxl: MXL not available (built without mxl tag)")

	// Provide a stub CurrentIndex using monotonic clock.
	CurrentIndex = func(rate Rational) uint64 {
		if rate.Denominator == 0 || rate.Numerator == 0 {
			return 0
		}
		ns := uint64(time.Now().UnixNano())
		// index = ns * numerator / (denominator * 1e9)
		return ns / (uint64(rate.Denominator) * 1_000_000_000 / uint64(rate.Numerator))
	}
}

// Instance is a stub for builds without the mxl build tag.
type Instance struct{}

// NewInstance returns ErrMXLNotAvailable when the mxl build tag is not set.
func NewInstance(domain string) (*Instance, error) {
	return nil, ErrMXLNotAvailable
}

// OpenReader returns ErrMXLNotAvailable.
func (i *Instance) OpenReader(flowID string) (DiscreteReader, error) {
	return nil, ErrMXLNotAvailable
}

// OpenAudioReader returns ErrMXLNotAvailable.
func (i *Instance) OpenAudioReader(flowID string) (ContinuousReader, error) {
	return nil, ErrMXLNotAvailable
}

// OpenWriter returns ErrMXLNotAvailable.
func (i *Instance) OpenWriter(flowDef string) (DiscreteWriter, error) {
	return nil, ErrMXLNotAvailable
}

// OpenAudioWriter returns ErrMXLNotAvailable.
func (i *Instance) OpenAudioWriter(flowDef string) (ContinuousWriter, error) {
	return nil, ErrMXLNotAvailable
}

// GetFlowConfig returns ErrMXLNotAvailable.
func (i *Instance) GetFlowConfig(flowID string) (FlowConfig, error) {
	return FlowConfig{}, ErrMXLNotAvailable
}

// IsFlowActive returns ErrMXLNotAvailable.
func (i *Instance) IsFlowActive(flowID string) (bool, error) {
	return false, ErrMXLNotAvailable
}

// Close is a no-op stub.
func (i *Instance) Close() error { return nil }

// Discover returns ErrMXLNotAvailable when the mxl build tag is not set.
func Discover(domain string) ([]FlowInfo, error) {
	return nil, ErrMXLNotAvailable
}
