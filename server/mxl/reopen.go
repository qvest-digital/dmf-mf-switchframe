package mxl

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
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
//
// It also moves to another flow when retargeted. The new flow is opened and
// checked beside the old one, which is read until the new handle is ready,
// and the reader loop, its grain counter and everything downstream of it
// stay as they are. An input switched to another flow therefore keeps its
// stream without a gap; only the pictures and samples change.
//
// The handle fields belong to the reader loop's goroutine; mu guards what
// Retarget shares with it.
type reopener[T flowHandle] struct {
	flowID string
	open   func(flowID string) (T, error)
	log    *slog.Logger

	cur       T
	isOpen    bool
	nextRetry time.Time

	// fresh is set when a new handle was opened and no read has gone
	// through it yet. The reader loop's index belongs to the flow that was
	// replaced, so that first read reports ErrTooLate instead, which sends
	// the loop to the head of the new ring.
	fresh bool

	mu sync.Mutex
	// want is the configuration the reader loops were set up for. A
	// recreated or retargeted flow with a different format, rate or
	// geometry is not read, because the loops would misinterpret every
	// grain of it. Unset until the first flow is open on a reader created
	// without one.
	want    FlowConfig
	hasWant bool
	// gen counts retargets, so a slow open for a superseded one is dropped.
	gen uint64
	// pending is a retarget ready to take over at the loop's next read.
	pending *pendingFlow[T]
	// reading is the flow id the loop reads, for FlowID.
	reading string
	closed  bool
}

// pendingFlow is the handle a retarget opened, or none for a disconnect.
type pendingFlow[T flowHandle] struct {
	id string
	h  T
}

// handle returns the current handle, first taking over a retarget that is
// ready and reopening the flow when there is none and the retry interval
// has passed. ok is false while the flow cannot be read.
func (r *reopener[T]) handle() (h T, ok bool) {
	r.mu.Lock()
	p := r.pending
	r.pending = nil
	r.mu.Unlock()
	if p != nil {
		r.takeOver(p)
	}
	if r.isOpen {
		return r.cur, true
	}
	if r.flowID == "" {
		return h, false
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
	if !r.accept(r.flowID, h) {
		_ = h.Close()
		return h, false
	}
	r.cur, r.isOpen, r.fresh = h, true, true
	r.log.Info("mxl: flow reopened", "flowID", r.flowID)
	return h, true
}

// accept reports whether h has the shape the reader loops were set up for,
// and sets that shape from the first flow when none was known.
func (r *reopener[T]) accept(id string, h T) bool {
	got := h.ConfigInfo()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.hasWant {
		r.want, r.hasWant = got, true
		return true
	}
	if !sameShape(got, r.want) {
		r.log.Error("mxl: flow does not match the source, not reading it",
			"flowID", id, "want", r.want, "got", got)
		return false
	}
	return true
}

// takeOver replaces the current handle with a retarget's.
func (r *reopener[T]) takeOver(p *pendingFlow[T]) {
	if r.isOpen {
		_ = r.cur.Close()
	}
	r.log.Info("mxl: input moved to another flow", "from", r.flowID, "to", p.id)
	r.flowID = p.id
	r.cur = p.h
	r.isOpen = p.id != ""
	r.fresh = r.isOpen
	r.nextRetry = time.Time{}
	r.mu.Lock()
	r.reading = p.id
	r.mu.Unlock()
}

// retarget moves the reader to id: at its next read for an empty id, which
// reads nothing, and otherwise once id is open and has the source's shape.
// The flow being read is read until then. A flow of another shape is
// refused and the old one kept.
func (r *reopener[T]) retarget(id string) {
	r.mu.Lock()
	r.gen++
	gen := r.gen
	if id == "" {
		r.replacePending(&pendingFlow[T]{})
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	go r.prepare(id, gen)
}

// replacePending sets the retarget to take over, closing one that was
// waiting. Called with mu held.
func (r *reopener[T]) replacePending(p *pendingFlow[T]) {
	if old := r.pending; old != nil && old.id != "" {
		_ = old.h.Close()
	}
	r.pending = p
}

// prepare opens id for retarget gen, retrying while the flow is not there,
// until it is open or the retarget is superseded.
func (r *reopener[T]) prepare(id string, gen uint64) {
	for {
		h, err := r.open(id)
		r.mu.Lock()
		if r.gen != gen || r.closed {
			r.mu.Unlock()
			if err == nil {
				_ = h.Close()
			}
			return
		}
		r.mu.Unlock()
		if err == nil {
			if !r.accept(id, h) {
				_ = h.Close()
				return
			}
			r.mu.Lock()
			if r.gen != gen || r.closed {
				r.mu.Unlock()
				_ = h.Close()
				return
			}
			r.replacePending(&pendingFlow[T]{id: id, h: h})
			r.mu.Unlock()
			return
		}
		r.log.Debug("mxl: retargeted flow not open yet", "flowID", id, "error", err)
		time.Sleep(reopenInterval)
	}
}

// current is the flow id the reader reads.
func (r *reopener[T]) current() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reading
}

// invalidate drops the current handle after libmxl reported it invalid.
func (r *reopener[T]) invalidate() {
	if !r.isOpen {
		return
	}
	_ = r.cur.Close()
	var zero T
	r.cur, r.isOpen, r.fresh = zero, false, false
	r.nextRetry = time.Time{}
	r.log.Warn("mxl: flow was recreated, reopening", "flowID", r.flowID)
}

// waitNoFlow stands in for a read that timed out while there is no flow to
// read, so a reader loop polling with a short timeout does not spin.
func waitNoFlow(timeoutNs uint64, flowID string) error {
	time.Sleep(time.Duration(timeoutNs))
	return fmt.Errorf("%w: flow %s is being reopened", ErrTimeout, flowID)
}

// takeFresh reports whether the current handle has not been read through
// yet, and marks it read.
func (r *reopener[T]) takeFresh() bool {
	f := r.fresh
	r.fresh = false
	return f
}

func (r *reopener[T]) headIndex() (uint64, error) {
	h, ok := r.handle()
	if !ok {
		return 0, fmt.Errorf("%w: flow %q is not open", ErrNoFlow, r.flowID)
	}
	return h.HeadIndex()
}

func (r *reopener[T]) close() error {
	r.mu.Lock()
	r.closed = true
	r.replacePending(nil)
	r.mu.Unlock()
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
		want: first.ConfigInfo(), hasWant: true, cur: first, isOpen: true,
		reading: flowID,
	}}
}

// ReadGrain reads from the current handle. After the flow was recreated it
// returns ErrTimeout until the new flow is open and ErrTooLate on the first
// read after that, which sends the reader loop to the head of the new ring.
func (w *reopeningReader) ReadGrain(index, timeoutNs uint64) ([]byte, GrainInfo, error) {
	h, ok := w.r.handle()
	if !ok {
		return nil, GrainInfo{}, waitNoFlow(timeoutNs, w.r.flowID)
	}
	if w.r.takeFresh() {
		return nil, GrainInfo{}, fmt.Errorf("%w: flow %s reopened", ErrTooLate, w.r.flowID)
	}
	data, info, err := h.ReadGrain(index, timeoutNs)
	if errors.Is(err, ErrFlowInvalid) {
		w.r.invalidate()
		return nil, GrainInfo{}, waitNoFlow(0, w.r.flowID)
	}
	return data, info, err
}

func (w *reopeningReader) ConfigInfo() FlowConfig     { return w.r.want }
func (w *reopeningReader) HeadIndex() (uint64, error) { return w.r.headIndex() }
func (w *reopeningReader) Close() error               { return w.r.close() }

// Retarget makes the reader read flowID from its next read on, or nothing
// for an empty id. Safe to call from any goroutine.
func (w *reopeningReader) Retarget(flowID string) { w.r.retarget(flowID) }

// FlowID is the flow the reader reads, or is moving to.
func (w *reopeningReader) FlowID() string { return w.r.current() }

// NewRetargetableReader is a reader that names no flow until retargeted.
// The first flow it opens sets the shape every later one must have.
func NewRetargetableReader(open func(string) (DiscreteReader, error), log *slog.Logger) DiscreteReader {
	if log == nil {
		log = slog.Default()
	}
	return &reopeningReader{r: reopener[DiscreteReader]{open: open, log: log}}
}

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
		want: first.ConfigInfo(), hasWant: true, cur: first, isOpen: true,
		reading: flowID,
	}}
}

// ReadSamples behaves as reopeningReader.ReadGrain does for grains.
func (w *reopeningAudioReader) ReadSamples(index uint64, count int, timeoutNs uint64) ([][]float32, error) {
	h, ok := w.r.handle()
	if !ok {
		return nil, waitNoFlow(timeoutNs, w.r.flowID)
	}
	if w.r.takeFresh() {
		return nil, fmt.Errorf("%w: flow %s reopened", ErrTooLate, w.r.flowID)
	}
	pcm, err := h.ReadSamples(index, count, timeoutNs)
	if errors.Is(err, ErrFlowInvalid) {
		w.r.invalidate()
		return nil, waitNoFlow(0, w.r.flowID)
	}
	return pcm, err
}

func (w *reopeningAudioReader) ConfigInfo() FlowConfig     { return w.r.want }
func (w *reopeningAudioReader) HeadIndex() (uint64, error) { return w.r.headIndex() }
func (w *reopeningAudioReader) Close() error               { return w.r.close() }

// Retarget behaves as reopeningReader.Retarget does.
func (w *reopeningAudioReader) Retarget(flowID string) { w.r.retarget(flowID) }

// FlowID is the flow the reader reads, or is moving to.
func (w *reopeningAudioReader) FlowID() string { return w.r.current() }

// NewRetargetableAudioReader is the ContinuousReader counterpart of
// NewRetargetableReader.
func NewRetargetableAudioReader(open func(string) (ContinuousReader, error), log *slog.Logger) ContinuousReader {
	if log == nil {
		log = slog.Default()
	}
	return &reopeningAudioReader{r: reopener[ContinuousReader]{open: open, log: log}}
}

// Retargetable is a reader that can be moved to another flow while its
// reader loop runs.
type Retargetable interface {
	Retarget(flowID string)
	FlowID() string
}
