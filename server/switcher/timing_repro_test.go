package switcher

import (
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/clock"
)

// One frame path: the reference clock ticks on the program grain grid and
// each tick shows, per source, the newest frame whose media time is at or
// before the tick's read point, ReaderDelayGrains behind.

const testFrameDur = 3003 // 29.97 fps, NewTestSwitcher's format

// tickSwitcher returns a switcher with no clock running; tests drive ticks.
func tickSwitcher(t *testing.T) *Switcher {
	t.Helper()
	s := newTestSwitcherDirect(distribution.NewRelay())
	t.Cleanup(s.Close)
	return s
}

// tickAt builds the tick of program grain n.
func tickAt(s *Switcher, n uint64) clock.Tick {
	pf := s.pipelineFormat.Load()
	r := clock.Rate{Num: int64(pf.FPSNum), Den: int64(pf.FPSDen)}
	return clock.Tick{
		Index:         n,
		VideoPTS:      clock.IndexPTS(r, n),
		ReadPTS:       clock.IndexPTS(r, n-clock.ReaderDelayGrains),
		FrameDuration: pf.FrameDuration(),
	}
}

func grainNow(s *Switcher) uint64 {
	pf := s.pipelineFormat.Load()
	return clock.TimestampToIndex(clock.Rate{Num: int64(pf.FPSNum), Den: int64(pf.FPSDen)}, clock.TAINow())
}

func grainPTS(s *Switcher, i uint64) int64 {
	pf := s.pipelineFormat.Load()
	return clock.IndexPTS(clock.Rate{Num: int64(pf.FPSNum), Den: int64(pf.FPSDen)}, i)
}

// mxlFrame makes a 320x240 frame of grain i with luma y.
func mxlFrame(s *Switcher, i uint64, y byte) *ProcessingFrame {
	pool := s.framePool.Load()
	buf := pool.Acquire()[:320*240*3/2]
	copy(buf, makeTestYUV(320, 240, y))
	pf := &ProcessingFrame{Data: buf, Width: 320, Height: 240, PTS: grainPTS(s, i),
		Format: ColorYUV420_8bit, pool: pool}
	pf.SetRefs(1)
	return pf
}

// recordNode records the PTS and luma of every frame the pipeline runs.
type recordNode struct {
	mu   sync.Mutex
	pts  []int64
	luma []byte
}

func (n *recordNode) Name() string                          { return "record" }
func (n *recordNode) Configure(format PipelineFormat) error { return nil }
func (n *recordNode) Active() bool                          { return true }
func (n *recordNode) Err() error                            { return nil }
func (n *recordNode) Latency() time.Duration                { return 0 }
func (n *recordNode) Close() error                          { return nil }
func (n *recordNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	n.mu.Lock()
	n.pts = append(n.pts, src.PTS)
	n.luma = append(n.luma, src.Data[0])
	n.mu.Unlock()
	return src
}

func (n *recordNode) lastLuma(t *testing.T) byte {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.luma) == 0 {
		t.Fatal("no program frame")
	}
	return n.luma[len(n.luma)-1]
}

func recordPipeline(t *testing.T, s *Switcher) *recordNode {
	t.Helper()
	node := &recordNode{}
	p := &Pipeline{}
	if err := p.Build(*s.pipelineFormat.Load(), s.framePool.Load(), []PipelineNode{node}); err != nil {
		t.Fatal(err)
	}
	s.pipeline.Store(p)
	return node
}

func onProgram(t *testing.T, s *Switcher, key string) {
	t.Helper()
	s.RegisterMXLSource(key)
	if err := s.Cut(t.Context(), key); err != nil {
		t.Fatal(err)
	}
}

// A grain becomes due ReaderDelayGrains ticks after its own: a tick never
// shows a grain newer than its read point, so a mirror landing a grain on
// its TAI does not make the choice flip between two grains from tick to
// tick.
func TestTickShowsGrainAtReadPoint(t *testing.T) {
	s := tickSwitcher(t)
	node := recordPipeline(t, s)
	onProgram(t, s, "mxl:cam")

	g := grainNow(s)
	s.IngestRawVideo("mxl:cam", mxlFrame(s, g, 50))
	s.IngestRawVideo("mxl:cam", mxlFrame(s, g+1, 60))

	s.processVideoTick(tickAt(s, g+1)) // read point g-1: nothing due yet
	if node.lastLuma(t) != 16 {
		t.Fatalf("tick g+1 showed luma %d, want black (16): grain g is not due", node.lastLuma(t))
	}
	s.processVideoTick(tickAt(s, g+2)) // read point g
	if l := node.lastLuma(t); l != 50 {
		t.Fatalf("tick g+2 showed luma %d, want grain g (50)", l)
	}
	s.processVideoTick(tickAt(s, g+3)) // read point g+1
	if l := node.lastLuma(t); l != 60 {
		t.Fatalf("tick g+3 showed luma %d, want grain g+1 (60)", l)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	for i, p := range node.pts {
		if want := grainPTS(s, g+1+uint64(i)); p != want {
			t.Fatalf("frame %d PTS %d, want the tick's %d", i, p, want)
		}
	}
}

// Ten grains delivered at once after a 200 ms gap, as an MXL reader that
// fell behind delivers them, still give one program frame per tick on the
// frame grid, each showing the grain due at that tick.
func TestBurstyInputStaysOnGrid(t *testing.T) {
	s := tickSwitcher(t)
	node := recordPipeline(t, s)
	onProgram(t, s, "mxl:cam")

	g := grainNow(s)
	n := g + clock.ReaderDelayGrains
	for i := uint64(0); i < 5; i++ { // paced
		s.IngestRawVideo("mxl:cam", mxlFrame(s, g+i, byte(20+i)))
		s.processVideoTick(tickAt(s, n+i))
	}
	for i := uint64(5); i < 11; i++ { // 200 ms gap, ticks continue
		s.processVideoTick(tickAt(s, n+i))
	}
	for i := uint64(5); i < 11; i++ { // the burst
		s.IngestRawVideo("mxl:cam", mxlFrame(s, g+i, byte(20+i)))
	}
	for i := uint64(11); i < 20; i++ {
		s.processVideoTick(tickAt(s, n+i))
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if len(node.pts) != 20 {
		t.Fatalf("%d program frames for 20 ticks", len(node.pts))
	}
	for i := 1; i < len(node.pts); i++ {
		if d := node.pts[i] - node.pts[i-1]; d < testFrameDur-1 || d > testFrameDur+1 {
			t.Fatalf("PTS step %d at frame %d, want %d", d, i, testFrameDur)
		}
	}
}

// A built-in source on program, then a live one: every program PTS step
// stays one frame period. With PTS from a frame counter nudged towards
// the wall clock this stepped 1.5 periods for twice the built-in's airtime
// after the cut, and the RTMP output ran at 1.5x real time.
func TestCutFromBuiltinToLiveStaysOnGrid(t *testing.T) {
	s := tickSwitcher(t)
	node := recordPipeline(t, s)
	s.RegisterMXLSource("mxl:cam")
	if err := s.Cut(t.Context(), BuiltinSourceKeyColorbars); err != nil {
		t.Fatal(err)
	}
	g := grainNow(s)
	n := g + clock.ReaderDelayGrains
	for i := uint64(0); i < 30; i++ {
		s.IngestRawVideo("mxl:cam", mxlFrame(s, g+i, 80))
		if i == 15 {
			if err := s.Cut(t.Context(), "mxl:cam"); err != nil {
				t.Fatal(err)
			}
		}
		s.processVideoTick(tickAt(s, n+i))
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	for i := 1; i < len(node.pts); i++ {
		if d := node.pts[i] - node.pts[i-1]; d < testFrameDur-1 || d > testFrameDur+1 {
			t.Fatalf("PTS step %d at frame %d across the cut, want %d", d, i, testFrameDur)
		}
	}
}

// Faded to black, the program stays black although the live source keeps
// delivering.
func TestFTBHoldStaysBlack(t *testing.T) {
	s := tickSwitcher(t)
	node := recordPipeline(t, s)
	onProgram(t, s, "mxl:cam")
	s.configMu.Lock()
	s.transitionState(StateFTB)
	s.updateHotState(func(hs *hotState) { hs.state = StateFTB })
	s.configMu.Unlock()

	g := grainNow(s)
	for i := uint64(0); i < 5; i++ {
		s.IngestRawVideo("mxl:cam", mxlFrame(s, g+i, 200))
		s.processVideoTick(tickAt(s, g+i+clock.ReaderDelayGrains))
		if l := node.lastLuma(t); l != 16 {
			t.Fatalf("tick %d in FTB showed luma %d, want black", i, l)
		}
	}
}

// SetSourceDelay delays a source's video by reading it further behind.
func TestSourceDelayReadsFurtherBehind(t *testing.T) {
	s := tickSwitcher(t)
	node := recordPipeline(t, s)
	onProgram(t, s, "mxl:cam")
	pf := s.pipelineFormat.Load()
	delayMs := 3 * 1001 * 1000 / pf.FPSNum // three frames
	if err := s.SetSourceDelay("mxl:cam", delayMs); err != nil {
		t.Fatal(err)
	}
	g := grainNow(s)
	s.IngestRawVideo("mxl:cam", mxlFrame(s, g, 90))
	s.processVideoTick(tickAt(s, g+clock.ReaderDelayGrains))
	if l := node.lastLuma(t); l == 90 {
		t.Fatal("delayed source shown at the undelayed read point")
	}
	s.processVideoTick(tickAt(s, g+clock.ReaderDelayGrains+3))
	if l := node.lastLuma(t); l != 90 {
		t.Fatalf("delayed source not shown three frames later (luma %d)", l)
	}
}

// The audio crossfade of a transition follows its position tick by tick.
func TestTransitionTickDrivesAudioPosition(t *testing.T) {
	s := tickSwitcher(t)
	recordPipeline(t, s)
	at := &mockAudioTransHandler{}
	s.SetAudioTransition(at)
	s.SetTransitionConfig(TransitionConfig{})
	onProgram(t, s, "mxl:a")
	s.RegisterMXLSource("mxl:b")
	if err := s.StartTransition(t.Context(), "mxl:b", "mix", 500, ""); err != nil {
		t.Fatal(err)
	}
	g := grainNow(s)
	for i := uint64(0); i < 5; i++ {
		s.processVideoTick(tickAt(s, g+i))
	}
	at.mu.Lock()
	defer at.mu.Unlock()
	if len(at.positionCalls) < 5 {
		t.Fatalf("audio transition got %d position updates over 5 ticks", len(at.positionCalls))
	}
	for i := 1; i < len(at.positionCalls); i++ {
		if at.positionCalls[i] <= at.positionCalls[i-1] {
			t.Fatalf("audio position %v does not advance", at.positionCalls)
		}
	}
}

// A realtime switcher processes each program frame once, on its tick: the
// clock is the only driver, whenever it is set.
func TestStartedSwitcherShowsEachFrameOnce(t *testing.T) {
	s := NewTestSwitcher(t, distribution.NewRelay())
	node := recordPipeline(t, s)
	onProgram(t, s, "mxl:cam")
	time.Sleep(300 * time.Millisecond)
	node.mu.Lock()
	defer node.mu.Unlock()
	if len(node.pts) < 5 {
		t.Fatalf("%d program frames in 300 ms", len(node.pts))
	}
	for i := 1; i < len(node.pts); i++ {
		d := node.pts[i] - node.pts[i-1]
		if d <= 0 || d%testFrameDur > 1 && d%testFrameDur < testFrameDur-1 {
			t.Fatalf("program PTS step %d: not a whole number of frames", d)
		}
	}
}
