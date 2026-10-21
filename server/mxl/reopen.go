package mxl

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// reopenInterval spaces out attempts to reopen a flow that is not there yet,
// so a writer that is gone for good costs one open per interval rather than
// one per read.
const reopenInterval = time.Second

type flowHandle interface {
	ConfigInfo() FlowConfig
	HeadIndex() (uint64, error)
	Close() error
}

// reopener holds the current handle on one flow and replaces it once libmxl
// reports the flow invalid. A writer that restarts recreates its flow under
// the same id, and the handle opened before that keeps pointing at the old
// data file: it never reads another grain, so the reader loops would give up
// on it and the source would stay dark until the process restarts.
type reopener[T flowHandle] struct {
	flowID string
	open   func(flowID string) (T, error)
	log    *slog.Logger

	// want is the configuration the reader loops were set up for. A
	// recreated flow with a different format, rate or geometry is not read,
	// because the loops would misinterpret every grain of it.
	want FlowConfig

	cur       T
	isOpen    bool
	nextRetry time.Time
}

// handle returns the current handle, reopening the flow first when there is
// none and the retry interval has passed. ok is false while the flow cannot
// be read.
func (r *reopener[T]) handle() (h T, ok bool) {
	if r.isOpen {
		return r.cur, true
	}
	now := time.Now()
	if now.Before(r.nextRetry) {
		return h, false
	}
	r.nextRetry = now.Add(reopenInterval)

	h, err := r.open(r.flowID)
	if err != nil {
		r.log.Debug("mxl: flow not reopened yet", "flowID", r.flowID, "error", err)
		return h, false
	}
	if got := h.ConfigInfo(); !sameShape(got, r.want) {
		_ = h.Close()
		r.log.Error("mxl: recreated flow no longer matches the source, not reading it",
			"flowID", r.flowID, "want", r.want, "got", got)
		return h, false
	}
	r.cur, r.isOpen = h, true
	r.log.Info("mxl: flow reopened", "flowID", r.flowID)
	return h, true
}

// invalidate drops the current handle after libmxl reported it invalid.
func (r *reopener[T]) invalidate() {
	if !r.isOpen {
		return
	}
	_ = r.cur.Close()
	var zero T
	r.cur, r.isOpen = zero, false
	r.nextRetry = time.Time{}
	r.log.Warn("mxl: flow was recreated, reopening", "flowID", r.flowID)
}

// waitNoFlow stands in for a read that timed out while there is no flow to
// read, so a reader loop polling with a short timeout does not spin.
func waitNoFlow(timeoutNs uint64, flowID string) error {
	time.Sleep(time.Duration(timeoutNs))
	return fmt.Errorf("%w: flow %s is being reopened", ErrTimeout, flowID)
}

func (r *reopener[T]) headIndex() (uint64, error) {
	h, ok := r.handle()
	if !ok {
		return 0, fmt.Errorf("mxl: flow %s is being reopened", r.flowID)
	}
	return h.HeadIndex()
}

func (r *reopener[T]) close() error {
	if !r.isOpen {
		return nil
	}
	r.isOpen = false
	return r.cur.Close()
}

// sameShape reports whether two configurations describe grains a reader set
// up for one of them can read from the other. Ring depth is a property of
// the domain the flow was created in and may differ.
func sameShape(a, b FlowConfig) bool {
	return a.Format == b.Format &&
		a.GrainRate == b.GrainRate &&
		a.SliceSizes == b.SliceSizes &&
		a.ChannelCount == b.ChannelCount
}

// reopeningReader is a DiscreteReader that survives its writer recreating
// the flow. Not safe for concurrent use; each reader loop owns its handle.
type reopeningReader struct {
	r reopener[DiscreteReader]
}

// NewReopeningReader wraps first, an open handle on flowID, so that a flow
// recreated by its writer is reopened with open instead of ending the read.
func NewReopeningReader(flowID string, first DiscreteReader, open func(string) (DiscreteReader, error), log *slog.Logger) DiscreteReader {
	if log == nil {
		log = slog.Default()
	}
	return &reopeningReader{r: reopener[DiscreteReader]{
		flowID: flowID, open: open, log: log,
		want: first.ConfigInfo(), cur: first, isOpen: true,
	}}
}

// ReadGrain reads from the current handle. After the flow was recreated it
// returns ErrTooLate once the new flow is open, which sends the reader loop
// back to the head of the new ring, and ErrTimeout until then.
func (w *reopeningReader) ReadGrain(index, timeoutNs uint64) ([]byte, GrainInfo, error) {
	h, ok := w.r.handle()
	if !ok {
		return nil, GrainInfo{}, waitNoFlow(timeoutNs, w.r.flowID)
	}
	data, info, err := h.ReadGrain(index, timeoutNs)
	if !errors.Is(err, ErrFlowInvalid) {
		return data, info, err
	}
	w.r.invalidate()
	if _, ok := w.r.handle(); ok {
		return nil, GrainInfo{}, fmt.Errorf("%w: flow %s reopened", ErrTooLate, w.r.flowID)
	}
	return nil, GrainInfo{}, waitNoFlow(timeoutNs, w.r.flowID)
}

func (w *reopeningReader) ConfigInfo() FlowConfig     { return w.r.want }
func (w *reopeningReader) HeadIndex() (uint64, error) { return w.r.headIndex() }
func (w *reopeningReader) Close() error               { return w.r.close() }

// reopeningAudioReader is the ContinuousReader counterpart of
// reopeningReader.
type reopeningAudioReader struct {
	r reopener[ContinuousReader]
}

// NewReopeningAudioReader wraps first, an open handle on flowID, so that a
// flow recreated by its writer is reopened with open instead of ending the
// read.
func NewReopeningAudioReader(flowID string, first ContinuousReader, open func(string) (ContinuousReader, error), log *slog.Logger) ContinuousReader {
	if log == nil {
		log = slog.Default()
	}
	return &reopeningAudioReader{r: reopener[ContinuousReader]{
		flowID: flowID, open: open, log: log,
		want: first.ConfigInfo(), cur: first, isOpen: true,
	}}
}

// ReadSamples behaves as reopeningReader.ReadGrain does for grains.
func (w *reopeningAudioReader) ReadSamples(index uint64, count int, timeoutNs uint64) ([][]float32, error) {
	h, ok := w.r.handle()
	if !ok {
		return nil, waitNoFlow(timeoutNs, w.r.flowID)
	}
	pcm, err := h.ReadSamples(index, count, timeoutNs)
	if !errors.Is(err, ErrFlowInvalid) {
		return pcm, err
	}
	w.r.invalidate()
	if _, ok := w.r.handle(); ok {
		return nil, fmt.Errorf("%w: flow %s reopened", ErrTooLate, w.r.flowID)
	}
	return nil, waitNoFlow(timeoutNs, w.r.flowID)
}

func (w *reopeningAudioReader) ConfigInfo() FlowConfig     { return w.r.want }
func (w *reopeningAudioReader) HeadIndex() (uint64, error) { return w.r.headIndex() }
func (w *reopeningAudioReader) Close() error               { return w.r.close() }
