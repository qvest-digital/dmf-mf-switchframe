package mxl

import (
	"context"
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
	// preparing is set while a retarget is opening its flow.
	preparing bool
	// expect, when set, checks a flow's configuration before it is read.
	expect func(FlowConfig) error
	// done ends retry loops when the reader is closed.
	done     chan struct{}
	doneOnce sync.Once
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
	if err := r.check(r.flowID, h); err != nil {
		_ = h.Close()
		r.log.Error("mxl: flow does not match the source, not reading it", "error", err)
		return h, false
	}
	r.mu.Lock()
	r.adoptShape(h)
	r.mu.Unlock()
	r.cur, r.isOpen, r.fresh = h, true, true
	r.log.Info("mxl: flow reopened", "flowID", r.flowID)
	return h, true
}

// check reports why h cannot be read by the reader loops, or nil: it must
// have the shape the loops were set up for, and pass the reader's own check
// when there is one. A reader created without a flow takes its shape from
// the first flow it reads.
func (r *reopener[T]) check(id string, h T) error {
	got := h.ConfigInfo()
	if r.expect != nil {
		if err := r.expect(got); err != nil {
			return fmt.Errorf("%w: flow %s: %v", ErrShape, id, err)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hasWant && !sameShape(got, r.want) {
		return fmt.Errorf("%w: flow %s is %+v, the source reads %+v", ErrShape, id, got, r.want)
	}
	return nil
}

// adoptShape records the shape of the first flow read. Called with mu held.
func (r *reopener[T]) adoptShape(h T) {
	if !r.hasWant {
		r.want, r.hasWant = h.ConfigInfo(), true
	}
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
// reads nothing, and otherwise once id is open and has the source's shape,
// retrying while the flow is not there. The flow being read is read until
// then. A flow of another shape is refused and the old one kept.
func (r *reopener[T]) retarget(id string) {
	gen, skip := r.begin(id)
	if skip || id == "" {
		return
	}
	go func() {
		for {
			err := r.prepare(id, gen)
			if err == nil || errors.Is(err, ErrShape) || errors.Is(err, errSuperseded) {
				if errors.Is(err, ErrShape) {
					r.log.Error("mxl: not moving the input to a flow it cannot read", "error", err)
				}
				return
			}
			r.log.Debug("mxl: retargeted flow not open yet", "flowID", id, "error", err)
			select {
			case <-r.done:
				return
			case <-time.After(reopenInterval):
			}
		}
	}()
}

// connect moves the reader to id as retarget does, but tries once and
// reports the outcome: an error when id cannot be opened within ctx or has
// another shape, in which case the reader keeps what it reads. An empty id
// disconnects.
func (r *reopener[T]) connect(ctx context.Context, id string) error {
	gen, skip := r.begin(id)
	if skip || id == "" {
		return nil
	}
	res := make(chan error, 1)
	go func() { res <- r.prepare(id, gen) }()
	select {
	case err := <-res:
		return err
	case <-ctx.Done():
		r.supersede(gen)
		return fmt.Errorf("flow %s not open: %w", id, ctx.Err())
	}
}

// errSuperseded is returned by prepare for a retarget a later one replaced.
var errSuperseded = errors.New("mxl: retarget superseded")

// begin starts a retarget to id and returns its generation. skip is true
// when the reader already reads id and nothing else is under way, so a
// repeated connection does not reopen the flow. An empty id disconnects at
// once.
func (r *reopener[T]) begin(id string) (gen uint64, skip bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != "" && id == r.reading && r.pending == nil && !r.preparing {
		return r.gen, true
	}
	r.gen++
	if id == "" {
		r.preparing = false
		r.replacePending(&pendingFlow[T]{})
		return r.gen, false
	}
	r.preparing = true
	return r.gen, false
}

// supersede abandons retarget gen, if it is still the latest.
func (r *reopener[T]) supersede(gen uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gen == gen {
		r.gen++
		r.preparing = false
	}
}

// replacePending sets the retarget to take over, closing one that was
// waiting. Called with mu held.
func (r *reopener[T]) replacePending(p *pendingFlow[T]) {
	if old := r.pending; old != nil && old.id != "" {
		_ = old.h.Close()
	}
	r.pending = p
}

// prepare opens id for retarget gen once, checks it and hands it to the
// reader loop. It returns the open error, a shape error, or errSuperseded
// when a later retarget or close came first.
func (r *reopener[T]) prepare(id string, gen uint64) error {
	h, err := r.open(id)
	if err != nil {
		return fmt.Errorf("flow %s: %w", id, err)
	}
	if err := r.check(id, h); err != nil {
		_ = h.Close()
		r.supersede(gen)
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gen != gen || r.closed {
		_ = h.Close()
		return errSuperseded
	}
	r.adoptShape(h)
	r.preparing = false
	r.replacePending(&pendingFlow[T]{id: id, h: h})
	return nil
}

// shape is the configuration the reader loops read.
func (r *reopener[T]) shape() FlowConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.want
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
	r.doneOnce.Do(func() {
		if r.done != nil {
			close(r.done)
		}
	})
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
		reading: flowID, done: make(chan struct{}),
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

func (w *reopeningReader) ConfigInfo() FlowConfig     { return w.r.shape() }
func (w *reopeningReader) HeadIndex() (uint64, error) { return w.r.headIndex() }
func (w *reopeningReader) Close() error               { return w.r.close() }

// Retarget makes the reader read flowID from its next read on, or nothing
// for an empty id. Safe to call from any goroutine.
func (w *reopeningReader) Retarget(flowID string) { w.r.retarget(flowID) }

// Connect moves the reader to flowID as Retarget does, but tries once and
// returns an error when the flow cannot be opened within ctx or has
// another shape; the reader then keeps what it reads.
func (w *reopeningReader) Connect(ctx context.Context, flowID string) error {
	return w.r.connect(ctx, flowID)
}

// FlowID is the flow the reader reads.
func (w *reopeningReader) FlowID() string { return w.r.current() }

// NewRetargetableReader is a reader that names no flow until retargeted.
// expect, when not nil, refuses a flow whose configuration the reader loop
// cannot read; besides, the first flow read sets the shape every later one
// must have.
func NewRetargetableReader(open func(string) (DiscreteReader, error), expect func(FlowConfig) error, log *slog.Logger) DiscreteReader {
	if log == nil {
		log = slog.Default()
	}
	return &reopeningReader{r: reopener[DiscreteReader]{open: open, expect: expect, log: log, done: make(chan struct{})}}
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
		reading: flowID, done: make(chan struct{}),
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

func (w *reopeningAudioReader) ConfigInfo() FlowConfig     { return w.r.shape() }
func (w *reopeningAudioReader) HeadIndex() (uint64, error) { return w.r.headIndex() }
func (w *reopeningAudioReader) Close() error               { return w.r.close() }

// Retarget behaves as reopeningReader.Retarget does.
func (w *reopeningAudioReader) Retarget(flowID string) { w.r.retarget(flowID) }

// Connect behaves as reopeningReader.Connect does.
func (w *reopeningAudioReader) Connect(ctx context.Context, flowID string) error {
	return w.r.connect(ctx, flowID)
}

// FlowID is the flow the reader reads.
func (w *reopeningAudioReader) FlowID() string { return w.r.current() }

// NewRetargetableAudioReader is the ContinuousReader counterpart of
// NewRetargetableReader.
func NewRetargetableAudioReader(open func(string) (ContinuousReader, error), expect func(FlowConfig) error, log *slog.Logger) ContinuousReader {
	if log == nil {
		log = slog.Default()
	}
	return &reopeningAudioReader{r: reopener[ContinuousReader]{open: open, expect: expect, log: log, done: make(chan struct{})}}
}

// Retargetable is a reader that can be moved to another flow while its
// reader loop runs.
type Retargetable interface {
	// Retarget moves to flowID once it opens, retrying while it is not
	// there; an empty id disconnects.
	Retarget(flowID string)
	// Connect moves to flowID or reports why it cannot.
	Connect(ctx context.Context, flowID string) error
	FlowID() string
}
