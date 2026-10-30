package mxl

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// liveFlow is a flow whose writer keeps producing grains, each carrying the
// flow's tag, so a test can tell which flow a grain came from. Its head
// moves on every look, from 100 on; one with written set has no grains until
// written is closed, as a flow being mirrored onto the node has none at
// first.
type liveFlow struct {
	tag     byte
	config  FlowConfig
	written chan struct{}

	mu     sync.Mutex
	closed bool
	head   uint64
}

// headStep is how far a liveFlow's head moves per look.
const headStep = 64

func newLiveFlow(tag byte, config FlowConfig) *liveFlow {
	return &liveFlow{tag: tag, config: config}
}

func (f *liveFlow) ReadGrain(index, _ uint64) ([]byte, GrainInfo, error) {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return nil, GrainInfo{}, fmt.Errorf("%w: read on closed handle", ErrFlowInvalid)
	}
	time.Sleep(time.Millisecond)
	return []byte{f.tag}, GrainInfo{Index: index, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}, nil
}

func (f *liveFlow) ReadSamples(_ uint64, count int, _ uint64) ([][]float32, error) {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("%w: read on closed handle", ErrFlowInvalid)
	}
	time.Sleep(time.Millisecond)
	ch := make([][]float32, f.config.ChannelCount)
	for i := range ch {
		ch[i] = make([]float32, count)
		ch[i][0] = float32(f.tag)
	}
	return ch, nil
}

func (f *liveFlow) ConfigInfo() FlowConfig { return f.config }
func (f *liveFlow) HeadIndex() (uint64, error) {
	if f.written != nil {
		select {
		case <-f.written:
		default:
			return 0, nil
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.head += headStep
	return 100 + f.head, nil
}

func (f *liveFlow) lastHead() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return 100 + f.head
}
func (f *liveFlow) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *liveFlow) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// flowStore opens live flows by id; an id listed in gates opens only once
// its gate is closed, as a flow being mirrored onto the node does.
type flowStore struct {
	mu    sync.Mutex
	flows map[string]*liveFlow
	gates map[string]chan struct{}
	// held receives a value each time an open of a gated flow starts
	// waiting at its gate.
	held  map[string]chan struct{}
	opens map[string]int
}

func newFlowStore(flows map[string]*liveFlow) *flowStore {
	return &flowStore{flows: flows, gates: map[string]chan struct{}{}, held: map[string]chan struct{}{}, opens: map[string]int{}}
}

func (s *flowStore) gate(id string) chan struct{} {
	g := make(chan struct{})
	s.mu.Lock()
	s.gates[id] = g
	s.held[id] = make(chan struct{}, 16)
	s.mu.Unlock()
	return g
}

// heldAt returns once an open of gated flow id waits at its gate.
func (s *flowStore) heldAt(t *testing.T, id string) {
	t.Helper()
	s.mu.Lock()
	h := s.held[id]
	s.mu.Unlock()
	select {
	case <-h:
	case <-time.After(time.Second):
		t.Fatalf("flow %s never opened", id)
	}
}

func (s *flowStore) open(id string) (DiscreteReader, error) {
	s.mu.Lock()
	g, held := s.gates[id], s.held[id]
	s.mu.Unlock()
	if g != nil {
		select {
		case held <- struct{}{}:
		default:
		}
		<-g
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opens[id]++
	f, ok := s.flows[id]
	if !ok {
		return nil, fmt.Errorf("flow %s: %w", id, ErrTimeout)
	}
	// Each open is a new handle, as with libmxl.
	h := newLiveFlow(f.tag, f.config)
	h.written = f.written
	s.flows[id] = h
	return h, nil
}

func (s *flowStore) handle(id string) *liveFlow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flows[id]
}

// nextGrain returns the next grain or fails the test when the loop stopped
// or delivered nothing within a second.
func nextGrain(t *testing.T, r *Reader) VideoGrain {
	t.Helper()
	select {
	case g, ok := <-r.Video():
		if !ok {
			t.Fatal("reader loop ended")
		}
		return g
	case <-time.After(time.Second):
		t.Fatal("no grain within a second")
	}
	return VideoGrain{}
}

func startVideo(t *testing.T, flow DiscreteReader) *Reader {
	t.Helper()
	r := NewVideoReader(ReaderConfig{BufSize: 1, TimeoutMs: 1, Width: 6, Height: 1})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		for range r.Video() {
		}
		r.Wait()
	})
	r.StartVideo(ctx, flow)
	return r
}

// Switching an input to another flow keeps its reader loop and grain
// counter: the stream downstream continues, with the new flow's pictures.
func TestRetarget_KeepsReaderLoopAndCounter(t *testing.T) {
	store := newFlowStore(map[string]*liveFlow{
		"a": newLiveFlow(1, videoShape),
		"b": newLiveFlow(2, videoShape),
	})
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	r := startVideo(t, flow)

	last := nextGrain(t, r)
	if last.V210[0] != 1 {
		t.Fatalf("first grain from flow %d, want 1", last.V210[0])
	}
	flow.(Retargetable).Retarget("b")

	for {
		g := nextGrain(t, r)
		if g.PTS != last.PTS+1 {
			t.Fatalf("PTS jumped from %d to %d across the switch", last.PTS, g.PTS)
		}
		last = g
		if g.V210[0] == 2 {
			break
		}
	}
	if !first.(*liveFlow).isClosed() {
		t.Error("handle on the flow switched away from was not closed")
	}
	if got := flow.(Retargetable).FlowID(); got != "b" {
		t.Errorf("FlowID = %q, want b", got)
	}
}

// The flow being read stays on screen until the new one is open, so a flow
// that is still being mirrored onto the node costs no gap.
func TestRetarget_ReadsOldFlowUntilNewIsOpen(t *testing.T) {
	store := newFlowStore(map[string]*liveFlow{
		"a": newLiveFlow(1, videoShape),
		"b": newLiveFlow(2, videoShape),
	})
	gate := store.gate("b")
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	r := startVideo(t, flow)

	flow.(Retargetable).Retarget("b")
	for i := 0; i < 20; i++ {
		if g := nextGrain(t, r); g.V210[0] != 1 {
			t.Fatalf("grain %d from flow %d before flow b was open", i, g.V210[0])
		}
	}
	close(gate)
	deadline := time.Now().Add(time.Second)
	for nextGrain(t, r).V210[0] != 2 {
		if time.Now().After(deadline) {
			t.Fatal("never moved to flow b once it opened")
		}
	}
}

// A flow mirrored onto the node by the connection has no grains at first
// and then starts at the writer's index, so the input keeps the flow it
// reads until the new one is written here, instead of reading grains the
// node never received.
func TestConnect_WaitsUntilTheFlowIsWrittenHere(t *testing.T) {
	b := newLiveFlow(2, videoShape)
	b.written = make(chan struct{})
	store := newFlowStore(map[string]*liveFlow{"a": newLiveFlow(1, videoShape), "b": b})
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	r := startVideo(t, flow)

	res := make(chan error, 1)
	go func() { res <- flow.(Retargetable).Connect(context.Background(), "b") }()
	for i := 0; i < 20; i++ {
		if g := nextGrain(t, r); g.V210[0] != 1 {
			t.Fatalf("grain %d from flow %d before flow b was written", i, g.V210[0])
		}
	}
	select {
	case err := <-res:
		t.Fatalf("connected before flow b was written: %v", err)
	default:
	}
	close(b.written)
	if err := <-res; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for nextGrain(t, r).V210[0] != 2 {
		if time.Now().After(deadline) {
			t.Fatal("never moved to flow b once it was written")
		}
	}
}

// A connection to a flow never written here fails once its time is up and
// leaves the input on the flow it reads.
func TestConnect_FailsForAFlowNotWritten(t *testing.T) {
	b := newLiveFlow(2, videoShape)
	b.written = make(chan struct{})
	store := newFlowStore(map[string]*liveFlow{"a": newLiveFlow(1, videoShape), "b": b})
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	r := startVideo(t, flow)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := flow.(Retargetable).Connect(ctx, "b"); err == nil {
		t.Fatal("connected to a flow never written")
	}
	if got := flow.(Retargetable).FlowID(); got != "a" {
		t.Errorf("FlowID = %q, want a", got)
	}
	for i := 0; i < 5; i++ {
		if g := nextGrain(t, r); g.V210[0] != 1 {
			t.Fatalf("grain from flow %d after the failed connection", g.V210[0])
		}
	}
}

// A flow of another shape would be misread by the loop, so the input keeps
// the flow it reads.
func TestRetarget_RefusesOtherShape(t *testing.T) {
	other := videoShape
	other.SliceSizes = [4]uint32{2560}
	store := newFlowStore(map[string]*liveFlow{
		"a": newLiveFlow(1, videoShape),
		"b": newLiveFlow(2, other),
	})
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	r := startVideo(t, flow)

	flow.(Retargetable).Retarget("b")
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 20; i++ {
		if g := nextGrain(t, r); g.V210[0] != 1 {
			t.Fatalf("read flow %d of another shape", g.V210[0])
		}
	}
	if got := flow.(Retargetable).FlowID(); got != "a" {
		t.Errorf("FlowID = %q, want a", got)
	}
	if !store.handle("b").isClosed() {
		t.Error("handle on the refused flow was not closed")
	}
}

// An input booked without a flow waits for its first connection instead of
// ending its reader loop.
func TestRetargetableReader_WaitsForFirstFlow(t *testing.T) {
	store := newFlowStore(map[string]*liveFlow{"a": newLiveFlow(1, videoShape)})
	flow := NewRetargetableReader(store.open, nil, nil)
	r := startVideo(t, flow)

	select {
	case _, ok := <-r.Video():
		if !ok {
			t.Fatal("reader loop with no flow ended")
		}
		t.Fatal("grain delivered with no flow")
	case <-time.After(100 * time.Millisecond):
	}
	flow.(Retargetable).Retarget("a")
	if g := nextGrain(t, r); g.V210[0] != 1 {
		t.Fatalf("grain from flow %d, want 1", g.V210[0])
	}
	if got := flow.ConfigInfo(); got != videoShape {
		t.Errorf("shape = %+v, want the first flow's", got)
	}
}

// Disconnecting stops reading but keeps the loop, so a later connection
// continues the same stream.
func TestRetarget_EmptyDisconnectsAndReconnects(t *testing.T) {
	store := newFlowStore(map[string]*liveFlow{"a": newLiveFlow(1, videoShape)})
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	r := startVideo(t, flow)
	before := nextGrain(t, r)

	flow.(Retargetable).Retarget("")
	time.Sleep(20 * time.Millisecond)
	for drained := false; !drained; {
		select {
		case <-r.Video():
		default:
			drained = true
		}
	}
	select {
	case _, ok := <-r.Video():
		if ok {
			t.Fatal("grain delivered after disconnect")
		}
		t.Fatal("reader loop ended on disconnect")
	case <-time.After(100 * time.Millisecond):
	}
	if !first.(*liveFlow).isClosed() {
		t.Error("handle was not closed on disconnect")
	}

	flow.(Retargetable).Retarget("a")
	if g := nextGrain(t, r); g.PTS <= before.PTS {
		t.Fatalf("PTS %d after reconnect, not past %d", g.PTS, before.PTS)
	}
}

// Of two retargets in quick succession the later one wins, even when the
// earlier flow opens after it.
func TestRetarget_LaterRetargetWins(t *testing.T) {
	store := newFlowStore(map[string]*liveFlow{
		"a": newLiveFlow(1, videoShape),
		"b": newLiveFlow(2, videoShape),
		"c": newLiveFlow(3, videoShape),
	})
	gate := store.gate("b")
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	r := startVideo(t, flow)

	flow.(Retargetable).Retarget("b")
	// Replaced while its open is under way, so the open returns a handle
	// nothing reads.
	store.heldAt(t, "b")
	flow.(Retargetable).Retarget("c")
	deadline := time.Now().Add(time.Second)
	for nextGrain(t, r).V210[0] != 3 {
		if time.Now().After(deadline) {
			t.Fatal("never moved to flow c")
		}
	}
	close(gate)
	deadline = time.Now().Add(time.Second)
	for !store.handle("b").isClosed() {
		if time.Now().After(deadline) {
			t.Fatal("handle opened for the superseded retarget was not closed")
		}
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 20; i++ {
		if g := nextGrain(t, r); g.V210[0] != 3 {
			t.Fatalf("superseded flow %d took over", g.V210[0])
		}
	}
	store.mu.Lock()
	opens := store.opens["b"]
	store.mu.Unlock()
	if opens != 1 {
		t.Errorf("flow b opened %d times, want once", opens)
	}
}

// Audio follows the same rules; its loop takes channel count and rate from
// the first flow it opens.
func TestRetargetableAudioReader_ReadsAfterFirstConnection(t *testing.T) {
	shape := FlowConfig{Format: DataFormatAudio, GrainRate: Rational{48000, 1}, ChannelCount: 2}
	a := newLiveFlow(1, shape)
	flow := NewRetargetableAudioReader(func(id string) (ContinuousReader, error) {
		if id != "a" {
			return nil, fmt.Errorf("flow %s: %w", id, ErrTimeout)
		}
		return a, nil
	}, nil, nil)
	r := NewAudioReader(ReaderConfig{BufSize: 4, TimeoutMs: 1, SamplesPerRead: 8})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		for range r.Audio() {
		}
		r.Wait()
	}()
	r.StartAudio(ctx, flow)

	flow.(Retargetable).Retarget("a")
	select {
	case g, ok := <-r.Audio():
		if !ok {
			t.Fatal("audio loop ended")
		}
		if g.Channels != 2 || g.SampleRate != 48000 {
			t.Fatalf("grain has %d channels at %d Hz, want 2 at 48000", g.Channels, g.SampleRate)
		}
	case <-time.After(time.Second):
		t.Fatal("no audio after connecting")
	}
}

// An audio read ends at its index, so the first read after the move covers
// a whole read before the head; the move waits until the node has received
// that much of the new flow, or that read would carry samples it never got.
func TestConnect_AudioWaitsForAWholeReadWritten(t *testing.T) {
	shape := FlowConfig{Format: DataFormatAudio, GrainRate: Rational{48000, 1}, ChannelCount: 2}
	b := newLiveFlow(2, shape)
	flow := NewRetargetableAudioReader(func(id string) (ContinuousReader, error) { return b, nil }, nil, nil)
	if err := flow.(Retargetable).Connect(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	// The first look saw 100+headStep; the move waited for a whole read more.
	if got := b.lastHead() - (100 + headStep); got < defaultSamplesPerRead {
		t.Errorf("moved after %d samples were written here, want at least %d", got, defaultSamplesPerRead)
	}
}

// A retarget made while an earlier one waits for its flow to be written
// ends that wait, rather than holding the flow open until it times out.
func TestRetarget_LaterRetargetEndsTheWait(t *testing.T) {
	b := newLiveFlow(2, videoShape)
	b.written = make(chan struct{})
	store := newFlowStore(map[string]*liveFlow{"a": newLiveFlow(1, videoShape), "b": b, "c": newLiveFlow(3, videoShape)})
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	r := startVideo(t, flow)

	res := make(chan error, 1)
	go func() { res <- flow.(Retargetable).Connect(context.Background(), "b") }()
	for deadline := time.Now().Add(time.Second); store.handle("b") == b; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("flow b never opened")
		}
	}
	flow.(Retargetable).Retarget("c")
	select {
	case err := <-res:
		if !errors.Is(err, errSuperseded) {
			t.Errorf("err %v, want superseded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the wait for flow b outlived the retarget to c")
	}
	if !store.handle("b").isClosed() {
		t.Error("flow b was left open")
	}
	deadline := time.Now().Add(time.Second)
	for nextGrain(t, r).V210[0] != 3 {
		if time.Now().After(deadline) {
			t.Fatal("never moved to flow c")
		}
	}
}

// A connection that cannot be made fails, so whoever asked for it can say
// so, and the input keeps the flow it reads.
func TestConnect_FailsForAFlowThatIsNotThere(t *testing.T) {
	store := newFlowStore(map[string]*liveFlow{"a": newLiveFlow(1, videoShape)})
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	r := startVideo(t, flow)
	nextGrain(t, r)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := flow.(Retargetable).Connect(ctx, "missing"); err == nil {
		t.Fatal("connecting to a flow that is not there succeeded")
	}
	if got := flow.(Retargetable).FlowID(); got != "a" {
		t.Errorf("FlowID = %q after a failed connection, want a", got)
	}
	for i := 0; i < 5; i++ {
		if g := nextGrain(t, r); g.V210[0] != 1 {
			t.Fatalf("grain from flow %d after a failed connection", g.V210[0])
		}
	}
}

func TestConnect_FailsForAFlowOfAnotherShape(t *testing.T) {
	other := videoShape
	other.GrainRate = Rational{25, 1}
	store := newFlowStore(map[string]*liveFlow{
		"a": newLiveFlow(1, videoShape),
		"b": newLiveFlow(2, other),
	})
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	startVideo(t, flow)

	err := flow.(Retargetable).Connect(context.Background(), "b")
	if !errors.Is(err, ErrShape) {
		t.Fatalf("Connect = %v, want ErrShape", err)
	}
	if !store.handle("b").isClosed() {
		t.Error("handle on the refused flow was not closed")
	}
}

// The reader's own check applies to the first flow too, which no earlier
// flow gives a shape to compare with.
func TestConnect_ExpectRefusesTheFirstFlow(t *testing.T) {
	store := newFlowStore(map[string]*liveFlow{"a": newLiveFlow(1, videoShape)})
	flow := NewRetargetableReader(store.open, func(c FlowConfig) error {
		if c.GrainRate != (Rational{25, 1}) {
			return fmt.Errorf("rate %v, the pipeline runs 25/1", c.GrainRate)
		}
		return nil
	}, nil)
	startVideo(t, flow)
	if err := flow.(Retargetable).Connect(context.Background(), "a"); !errors.Is(err, ErrShape) {
		t.Fatalf("Connect = %v, want ErrShape", err)
	}
	if got := flow.(Retargetable).FlowID(); got != "" {
		t.Errorf("FlowID = %q, want none", got)
	}
}

// Connecting an input to the flow it already reads changes nothing: the
// flow is not reopened, which would send the reader back to the head.
func TestConnect_SameFlowIsANoOp(t *testing.T) {
	store := newFlowStore(map[string]*liveFlow{"a": newLiveFlow(1, videoShape)})
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	startVideo(t, flow)
	if err := flow.(Retargetable).Connect(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	opens := store.opens["a"]
	store.mu.Unlock()
	if opens != 1 {
		t.Errorf("flow opened %d times, want once", opens)
	}
	if first.(*liveFlow).isClosed() {
		t.Error("the handle being read was replaced")
	}
}

// A connection that failed leaves nothing under way, so connecting again to
// the flow the input still reads is a no-op rather than a reopen, which
// would replace the handle being read.
func TestConnect_FailedConnectionLeavesNothingUnderWay(t *testing.T) {
	store := newFlowStore(map[string]*liveFlow{"a": newLiveFlow(1, videoShape)})
	first, _ := store.open("a")
	flow := NewReopeningReader("a", first, store.open, nil)
	startVideo(t, flow)
	if err := flow.(Retargetable).Connect(context.Background(), "missing"); err == nil {
		t.Fatal("connection to a missing flow succeeded")
	}
	if err := flow.(Retargetable).Connect(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	opens := store.opens["a"]
	store.mu.Unlock()
	if opens != 1 {
		t.Errorf("flow a opened %d times, want once", opens)
	}
	if first.(*liveFlow).isClosed() {
		t.Error("the handle being read was replaced")
	}
}

// A retarget a later connection replaced stops opening its flow, also while
// that flow is missing. Otherwise an input disconnected or moved away from a
// flow nobody writes keeps opening it for as long as the process runs, and
// every such open is one more turn at libmxl's instance lock.
func TestRetarget_SupersededRetargetStopsOpening(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after func(Retargetable) error
	}{
		{"disconnected", func(r Retargetable) error { r.Retarget(""); return nil }},
		{"connected elsewhere", func(r Retargetable) error {
			return r.Connect(context.Background(), "a")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFlowStore(map[string]*liveFlow{"a": newLiveFlow(1, videoShape)})
			opened := func() int {
				store.mu.Lock()
				defer store.mu.Unlock()
				return store.opens["missing"]
			}
			flow := NewRetargetableReader(store.open, nil, nil)
			t.Cleanup(func() { _ = flow.Close() })
			startVideo(t, flow)

			flow.(Retargetable).Retarget("missing")
			deadline := time.Now().Add(time.Second)
			for opened() == 0 {
				if time.Now().After(deadline) {
					t.Fatal("the missing flow was never opened")
				}
				time.Sleep(time.Millisecond)
			}
			if err := tc.after(flow.(Retargetable)); err != nil {
				t.Fatal(err)
			}
			before := opened()
			time.Sleep(7 * reopenInterval / 2)
			// One open may already be under way when the retarget is
			// replaced; a loop still retrying makes three more.
			if n := opened() - before; n > 1 {
				t.Errorf("superseded retarget opened the missing flow %d more times", n)
			}
		})
	}
}
