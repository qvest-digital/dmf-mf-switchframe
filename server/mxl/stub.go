//go:build !cgo || !mxl

package mxl

import (
	"log/slog"
	"time"

	"github.com/zsiec/switchframe/server/clock"
)

func init() {
	slog.Debug("mxl: MXL not available (built without mxl tag)")

	// Without libmxl, the system clock stands in for TAI, with libmxl's
	// rounding.
	CurrentIndex = func(rate Rational) uint64 {
		return clock.TimestampToIndex(clock.Rate{Num: rate.Numerator, Den: rate.Denominator}, clock.TAINow())
	}
	NsUntilIndex = func(index uint64, rate Rational) uint64 {
		r := clock.Rate{Num: rate.Numerator, Den: rate.Denominator}
		if r.Num <= 0 || r.Den <= 0 {
			return 0
		}
		if d := clock.IndexToTimestamp(r, index) - clock.TAINow(); d > 0 {
			return uint64(d)
		}
		return 0
	}
}

// Instance is a stub for builds without the mxl build tag.
type Instance struct{}

// NewInstance returns ErrMXLNotAvailable when the mxl build tag is not set.
func NewInstance(domain string) (*Instance, error) {
	return nil, ErrMXLNotAvailable
}

// NewInstanceGC returns ErrMXLNotAvailable when the mxl build tag is not set.
func NewInstanceGC(domain string, gcInterval time.Duration) (*Instance, error) {
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

// Close is a no-op stub. It is safe to call concurrently or multiple times.
func (i *Instance) Close() error {
	return nil
}

// Discover returns ErrMXLNotAvailable when the mxl build tag is not set.
func Discover(domain string) ([]FlowInfo, error) {
	return nil, ErrMXLNotAvailable
}
