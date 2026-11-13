package playout

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

// slowProvider opens assets the way a demuxing provider does: Open takes
// openDelay and the first Next of every iterator takes firstDelay. Each
// frame's PTS carries its asset's id and frame index, and every frame
// carries one audio frame tagged with the same id.
type slowProvider struct {
	openDelay  time.Duration
	firstDelay time.Duration
	frames     int

	mu     sync.Mutex
	opens  map[string]int
	closes map[string]int
	fail   map[string]error
	order  []string         // URIs in the order their opens started
	sizes  map[string]int64 // asset sizes; absent is unknown
	sized  int              // assetSize calls
}

func (p *slowProvider) assetSize(uri string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sized++
	if n, ok := p.sizes[uri]; ok {
		return n
	}
	return -1
}

func newSlowProvider(openDelay, firstDelay time.Duration, frames int) *slowProvider {
	return &slowProvider{
		openDelay:  openDelay,
		firstDelay: firstDelay,
		frames:     frames,
		opens:      map[string]int{},
		closes:     map[string]int{},
		fail:       map[string]error{},
		sizes:      map[string]int64{},
	}
}

// slowAssetID maps the test asset URIs to the id frames carry.
var slowAssetID = map[string]int64{
	"local:///clips/a.ts": 1,
	"local:///clips/b.ts": 2,
	"local:///clips/c.ts": 3,
	"local:///clips/d.ts": 4,
}

// slowPTS is the PTS frame index of asset id carries, at 50 fps.
func slowPTS(id int64, index int) int64 { return id*100_000_000 + int64(index)*1800 }

func (p *slowProvider) Open(uri string) (FrameIterator, error) {
	p.mu.Lock()
	p.order = append(p.order, uri)
	p.mu.Unlock()
	time.Sleep(p.openDelay)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.opens[uri]++
	if err := p.fail[uri]; err != nil {
		return nil, err
	}
	frames := make([]DecodedFrame, p.frames)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV:    make([]byte, 64*48*3/2),
			PTS:    slowPTS(slowAssetID[uri], i),
			Width:  64,
			Height: 48,
			AudioFrames: []AudioFrameData{{
				Data: []byte{byte(slowAssetID[uri])}, SampleRate: 48000, Channels: 2,
			}},
		}
	}
	return &slowIterator{SliceIterator: NewSliceIterator(frames), p: p, uri: uri}, nil
}

func (p *slowProvider) openCount(uri string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.opens[uri]
}

func (p *slowProvider) closeCount(uri string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closes[uri]
}

type slowIterator struct {
	*SliceIterator
	p      *slowProvider
	uri    string
	warmed bool
}

func (it *slowIterator) Next() (*DecodedFrame, error) {
	if !it.warmed {
		it.warmed = true
		time.Sleep(it.p.firstDelay)
	}
	return it.SliceIterator.Next()
}

// Seek moves to frame, as FileFrameIterator does to its keyframe.
func (it *slowIterator) Seek(frame int) error {
	it.pos = frame
	return nil
}

func (it *slowIterator) Close() {
	it.p.mu.Lock()
	it.p.closes[it.uri]++
	it.p.mu.Unlock()
}

// emitted is one frame the channel put out, with the audio sent with it.
type emitted struct {
	audio []byte
}

// prerollChannel returns a channel over p with the given pre-roll, its
// output recorded into out.
func prerollChannel(p AssetProvider, prerollItems int, out *[]emitted) *Channel {
	return prerollChannelMax(p, prerollItems, 0, out)
}

// prerollChannelMax is prerollChannel with a byte budget for the items
// after the cursor's.
func prerollChannelMax(p AssetProvider, prerollItems int, maxBytes int64, out *[]emitted) *Channel {
	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"},
		AssetProvider:        p,
		Width:                64,
		Height:               48,
		FPS:                  50,
		PrerollItems:         prerollItems,
		PrerollMaxBytes:      maxBytes,
		starts:               newStartStats(),
	})
	var mu sync.Mutex
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		mu.Lock()
		*out = append(*out, emitted{})
		mu.Unlock()
	})
	ch.SetAudioOutput(func(data []byte, pts int64, sampleRate, channels int) {
		mu.Lock()
		last := &(*out)[len(*out)-1]
		last.audio = append(last.audio, data[0])
		mu.Unlock()
	})
	return ch
}

// onAir puts ch on air without its run goroutine, so the test ticks it.
func onAir(ch *Channel) {
	ch.mu.Lock()
	ch.state = StateOnAir
	ch.scheduler.Start()
	ch.updatePrerollLocked()
	ch.mu.Unlock()
}

// tickTimed runs one tick at now and returns how long it took.
func tickTimed(t *testing.T, ch *Channel, now time.Time) time.Duration {
	t.Helper()
	start := time.Now()
	require.False(t, ch.tick(now))
	return time.Since(start)
}

func waitPrerolled(t *testing.T, ch *Channel, n int) {
	t.Helper()
	waitFor(t, 5*time.Second, func() bool { return ch.preroll.ready() >= n })
}

// sourcePTS returns the source PTS of the frame the channel last took from
// the iterator of the item on air.
func sourcePTS(ch *Channel) int64 {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.clipLastFrame == nil {
		return -1
	}
	return ch.clipLastFrame.PTS
}

// A cut to a pre-rolled clip takes its first frame on the tick of the cut,
// with no tick spent opening it. Opened on the cut instead, the tick stalls
// for the open and the first decode, and the output with it.
func TestChannel_Preroll_CutTakesFirstFrameWithoutStall(t *testing.T) {
	const openDelay = 150 * time.Millisecond
	p := newSlowProvider(openDelay, 50*time.Millisecond, 5)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"))))
	waitPrerolled(t, ch, 1) // a, which the cue starts on
	onAir(ch)
	waitPrerolled(t, ch, 2) // and b after it

	now := time.Now()
	var worst time.Duration
	var seen []int64
	for i := 0; i < 9; i++ {
		if d := tickTimed(t, ch, now); d > worst {
			worst = d
		}
		seen = append(seen, sourcePTS(ch))
		now = now.Add(20 * time.Millisecond)
	}
	require.Less(t, worst, openDelay/3, "a tick stalled opening an item")
	require.Contains(t, seen, slowPTS(2, 0), "b never went on air")
	require.Equal(t, 1, p.openCount("local:///clips/a.ts"))
	require.Equal(t, 1, p.openCount("local:///clips/b.ts"))
}

// A clip that runs out hands over to the next item on the same tick: the
// output goes from the last frame of one clip to the first frame of the
// next, with the first frame's audio, rather than showing the last frame a
// second time while the scheduler advances on the tick after.
func TestChannel_Preroll_ClipEndCutsOnTheSameTick(t *testing.T) {
	p := newSlowProvider(0, 0, 3)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"))))
	waitPrerolled(t, ch, 1)
	onAir(ch)
	waitPrerolled(t, ch, 2)

	now := time.Now()
	var seen []int64
	for i := 0; i < 6; i++ {
		tickTimed(t, ch, now)
		seen = append(seen, sourcePTS(ch))
		now = now.Add(20 * time.Millisecond)
	}
	require.Equal(t, []int64{
		slowPTS(1, 0), slowPTS(1, 1), slowPTS(1, 2),
		slowPTS(2, 0), slowPTS(2, 1), slowPTS(2, 2),
	}, seen)
	require.Len(t, out, 6)
	for i, f := range out {
		want := byte(1)
		if i >= 3 {
			want = 2
		}
		require.Equal(t, []byte{want}, f.audio, "frame %d", i)
	}
}

// A clip that ends into a transition starts the transition on the same
// tick: that tick blends the incoming clip's first frame and carries its
// audio, rather than showing the outgoing clip's last frame once more.
func TestChannel_Preroll_ClipEndStartsTransitionOnTheSameTick(t *testing.T) {
	p := newSlowProvider(0, 0, 3)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	b := clipItem("b")
	b.Transition = &TransitionConfig{Type: "dissolve", DurationMs: 100}
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), b)))
	waitPrerolled(t, ch, 1)
	onAir(ch)
	waitPrerolled(t, ch, 2)

	now := time.Now()
	for i := 0; i < 4; i++ {
		tickTimed(t, ch, now)
		now = now.Add(20 * time.Millisecond)
	}
	require.Equal(t, SchedTransitioning, ch.scheduler.State())
	ch.mu.Lock()
	in := ch.inClipLastFrame
	ch.mu.Unlock()
	require.NotNil(t, in, "the tick after the last frame did not start the transition")
	require.Equal(t, slowPTS(2, 0), in.PTS)
	require.Len(t, out, 4)
	require.Equal(t, []byte{2}, out[3].audio)
}

// The incoming clip of a transition is pre-rolled too: the transition's
// first tick blends its first frame without opening it.
func TestChannel_Preroll_TransitionIncomingReady(t *testing.T) {
	const openDelay = 150 * time.Millisecond
	p := newSlowProvider(openDelay, 0, 50)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	b := clipItem("b")
	b.Transition = &TransitionConfig{Type: "dissolve", DurationMs: 100}
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), b)))
	waitPrerolled(t, ch, 1)
	onAir(ch)
	waitPrerolled(t, ch, 2)

	now := time.Now()
	tickTimed(t, ch, now)
	require.NoError(t, ch.Next())
	now = now.Add(20 * time.Millisecond)
	d := tickTimed(t, ch, now)
	require.Equal(t, SchedTransitioning, ch.scheduler.State())
	require.Less(t, d, openDelay/3, "the transition's first tick opened the incoming clip")
	require.Equal(t, []byte{2}, out[len(out)-1].audio, "the incoming clip's audio starts with the transition")
}

// Pre-roll holds the item on air plus PrerollItems clips after it, and
// lets go of what the cursor moves past.
func TestChannel_Preroll_LookaheadBoundedAndReleased(t *testing.T) {
	p := newSlowProvider(0, 0, 100)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	pl := testPlaylist(clipItem("a"), slateItem("s", 1000), clipItem("b"), clipItem("c"), clipItem("d"))
	require.NoError(t, ch.LoadPlaylist(pl))
	waitPrerolled(t, ch, 1) // a cued channel prepares only the item it starts on
	require.Equal(t, 0, p.openCount("local:///clips/b.ts"))
	onAir(ch)
	waitPrerolled(t, ch, 2) // and on air b too; the slate between them needs nothing
	require.Equal(t, 0, p.openCount("local:///clips/c.ts"))

	now := time.Now()
	tickTimed(t, ch, now)
	// a is on air and b prepared: nothing further.
	require.Equal(t, 1, ch.preroll.ready())

	// Jumping to c skips b, which is released, and prepares d.
	require.NoError(t, ch.JumpTo("c"))
	waitFor(t, 5*time.Second, func() bool { return p.closeCount("local:///clips/b.ts") == 1 })
	waitPrerolled(t, ch, 2) // c, which airs next, and d after it
	require.Equal(t, 1, p.openCount("local:///clips/b.ts"))

	now = now.Add(20 * time.Millisecond)
	tickTimed(t, ch, now)
	require.Equal(t, slowPTS(3, 0), sourcePTS(ch))
	require.Equal(t, 1, p.openCount("local:///clips/c.ts"), "c opened again instead of taken")

	// Stopping releases everything prepared.
	ch.Stop()
	require.Equal(t, 0, ch.preroll.ready())
	waitFor(t, 5*time.Second, func() bool { return p.closeCount("local:///clips/d.ts") == 1 })
}

// Pre-roll off opens each clip on the tick that cuts to it, and prepares
// nothing.
func TestChannel_Preroll_Off(t *testing.T) {
	p := newSlowProvider(0, 0, 3)
	var out []emitted
	ch := prerollChannel(p, 0, &out)
	require.Nil(t, ch.preroll)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"))))
	require.Equal(t, 0, p.openCount("local:///clips/a.ts"))
	onAir(ch)
	now := time.Now()
	for i := 0; i < 4; i++ {
		tickTimed(t, ch, now)
		now = now.Add(20 * time.Millisecond)
	}
	require.Equal(t, slowPTS(2, 0), sourcePTS(ch))
	require.Equal(t, 2.0, testutil.ToFloat64(ch.config.starts.starts.WithLabelValues("1", prerollMissed)))
}

// An asset that is not in the cache yet is prepared again once it may be,
// and airs pre-rolled.
func TestChannel_Preroll_RetriesAssetNotCachedYet(t *testing.T) {
	p := newSlowProvider(0, 0, 100)
	p.fail["local:///clips/b.ts"] = ErrAssetNotReady
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"))))
	waitPrerolled(t, ch, 1)
	onAir(ch)
	waitFor(t, 5*time.Second, func() bool { return p.openCount("local:///clips/b.ts") == 1 })

	p.mu.Lock()
	delete(p.fail, "local:///clips/b.ts")
	p.mu.Unlock()
	waitFor(t, 5*time.Second, func() bool {
		ch.updatePreroll()
		return ch.preroll.ready() == 2
	})
}

// A clip whose asset cannot be opened is skipped on the tick it fails, to
// the next item, rather than putting out a frame of idle in between.
func TestChannel_Preroll_FailedItemSkippedOnSameTick(t *testing.T) {
	p := newSlowProvider(0, 0, 3)
	p.fail["local:///clips/b.ts"] = errors.New("demux: broken file")
	var out []emitted
	ch := prerollChannel(p, 2, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"), clipItem("c"))))
	waitPrerolled(t, ch, 1)
	onAir(ch)
	waitPrerolled(t, ch, 2) // a and c

	now := time.Now()
	var seen []int64
	for i := 0; i < 5; i++ {
		tickTimed(t, ch, now)
		seen = append(seen, sourcePTS(ch))
		now = now.Add(20 * time.Millisecond)
	}
	require.Equal(t, []int64{
		slowPTS(1, 0), slowPTS(1, 1), slowPTS(1, 2),
		slowPTS(3, 0), slowPTS(3, 1),
	}, seen)
	// The failed preparation stands for the cut: the asset is not opened
	// a second time on the tick.
	require.Equal(t, 1, p.openCount("local:///clips/b.ts"))
}

// The start metrics count the cut by its pre-roll and time it.
func TestChannel_Preroll_StartMetrics(t *testing.T) {
	p := newSlowProvider(0, 0, 3)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"))))
	waitPrerolled(t, ch, 1)
	onAir(ch)
	waitPrerolled(t, ch, 2)
	now := time.Now()
	for i := 0; i < 4; i++ {
		tickTimed(t, ch, now)
		now = now.Add(20 * time.Millisecond)
	}
	s := ch.config.starts
	require.Equal(t, 2.0, testutil.ToFloat64(s.starts.WithLabelValues("1", prerollReady)))
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(s.delay)
	n, err := testutil.GatherAndCount(reg, "switchframe_playout_item_start_delay_seconds")
	require.NoError(t, err)
	require.Equal(t, 2, n, "a video and an audio series")
}

// A looping clip with an in-point starts each pass at a seek to it rather
// than decoding from the top of the file.
func TestTrimIterator_RewindSeeksToInPoint(t *testing.T) {
	inner := &seekCountingIterator{SliceIterator: NewSliceIterator(fakeFrames(90))}
	trim := NewTrimIterator(inner, 1000, 0) // 1 s in, 30 fps: frame 30
	require.NoError(t, trim.Seek(0))
	f, err := trim.Next()
	require.NoError(t, err)
	require.Equal(t, int64(30*3000), f.PTS)
	for {
		f, err := trim.Next()
		require.NoError(t, err)
		if f == nil {
			break
		}
	}
	inner.nexts = 0
	require.NoError(t, trim.Rewind())
	f, err = trim.Next()
	require.NoError(t, err)
	require.Equal(t, int64(30*3000), f.PTS)
	require.Equal(t, 1, inner.nexts, "the pass decoded its way to the in-point")
}

type seekCountingIterator struct {
	*SliceIterator
	nexts int
}

func (s *seekCountingIterator) Next() (*DecodedFrame, error) {
	s.nexts++
	return s.SliceIterator.Next()
}

func (s *seekCountingIterator) Seek(frame int) error {
	s.pos = frame
	return nil
}

// ClipsAhead returns the clip at the cursor and the clips after it up to
// the look-ahead, past items that are not clips, without the open ones.
func TestScheduler_ClipsAhead(t *testing.T) {
	s := NewScheduler()
	require.NoError(t, s.LoadPlaylist(testPlaylist(
		clipItem("a"), slateItem("s", 1000), liveItem("l", "cam"), clipItem("b"), clipItem("c"))))
	ids := func(cur, next []PlaylistItem) [2][]string {
		var out [2][]string
		for i, items := range [][]PlaylistItem{cur, next} {
			for _, it := range items {
				out[i] = append(out[i], it.ID)
			}
		}
		return out
	}
	require.Equal(t, [2][]string{{"a"}, {"b"}}, ids(s.ClipsAhead(1)))
	require.Equal(t, [2][]string{{"a"}, {"b", "c"}}, ids(s.ClipsAhead(2)))
	require.Equal(t, [2][]string{nil, {"b"}}, ids(s.ClipsAhead(1, "a")))
	require.Equal(t, [2][]string{{"a"}, nil}, ids(s.ClipsAhead(0)))
	require.Equal(t, [2][]string{{"a"}, {"c"}}, ids(s.ClipsAhead(2, "b")), "an open clip counts")

	// Off air the cursor is the cued item.
	require.NoError(t, s.Cue("b"))
	require.Equal(t, [2][]string{{"b"}, {"c"}}, ids(s.ClipsAhead(1)))
	s.Start()
	require.Equal(t, [2][]string{nil, {"c"}}, ids(s.ClipsAhead(1, "b")))

	// A playlist that ran out needs nothing.
	s.SetCursor(4, ModeAutomated, 0)
	require.NoError(t, s.Next())
	s.Tick(time.Now())
	require.Equal(t, [2][]string{nil, nil}, ids(s.ClipsAhead(1)))
}

// countingDecoder is a decoder that puts out a picture for every frame it
// is fed and counts them.
type countingDecoder struct {
	mockVideoDecoder
	fed *int
}

func (d *countingDecoder) Decode(h264 []byte) ([]byte, int, int, error) {
	*d.fed++
	return d.mockVideoDecoder.Decode(h264)
}

type fileProvider struct{ factory DecoderFactory }

func (p fileProvider) Open(path string) (FrameIterator, error) {
	return NewFileFrameIteratorWithDecoder(path, p.factory)
}

// A clip with an in-point opens at a seek to it: the decoder is fed the
// GOP the in-point falls in, not every frame of the file before it.
func TestOpenClip_InPointSeeksInsteadOfDecodingUpToIt(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg CLI not available")
	}
	path := filepath.Join(t.TempDir(), "in.ts")
	out, err := exec.Command(ffmpeg, "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=320x240:rate=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "30",
		"-f", "mpegts", path).CombinedOutput()
	if err != nil {
		t.Skipf("ffmpeg: %v\n%s", err, out)
	}

	fed := 0
	p := fileProvider{factory: func() (VideoDecoder, error) {
		return &countingDecoder{mockVideoDecoder: mockVideoDecoder{width: 320, height: 240}, fed: &fed}, nil
	}}
	item := PlaylistItem{ID: "x", Type: ItemClip, AssetURI: path, InPointMs: 3000}
	src, _, _, err := openClip(p, &item)
	require.NoError(t, err)
	defer src.Close()
	f, err := src.Next()
	require.NoError(t, err)
	require.NotNil(t, f)
	trim := src.(*TrimIterator)
	require.GreaterOrEqual(t, f.PTS-trim.basePTS, int64(3000*90), "first frame before the in-point")
	require.Less(t, f.PTS-trim.basePTS, int64(3000*90+3*3000), "first frame well past the in-point")
	require.LessOrEqual(t, fed, 32, "decoded every frame up to the in-point (90)")
}

// The watchdog's recovery loads the playlist, cues the item on air and
// starts the channel. The first tick puts that item out within about one
// open of it: a preparation of the first item, started by the load and
// no longer wanted, neither delays it nor goes before it.
func TestChannel_Preroll_FirstTickAfterLoadCueStart(t *testing.T) {
	const openDelay = 300 * time.Millisecond
	p := newSlowProvider(openDelay, 0, 50)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"), clipItem("c"), clipItem("d"))))
	ch.CueAt(2)
	onAir(ch)

	d := tickTimed(t, ch, time.Now())
	require.Less(t, d, openDelay*3/2, "first tick after a cue")
	require.Equal(t, slowPTS(3, 0), sourcePTS(ch))
	require.Equal(t, 1, p.openCount("local:///clips/c.ts"))
}

// A jump puts the item jumped to out on the next tick within about one
// open, and it is prepared before the item after it.
func TestChannel_Preroll_JumpPreparesTargetFirst(t *testing.T) {
	const openDelay = 300 * time.Millisecond
	p := newSlowProvider(openDelay, 0, 50)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"), clipItem("c"), clipItem("d"))))
	waitPrerolled(t, ch, 1)
	onAir(ch)
	now := time.Now()
	tickTimed(t, ch, now)
	waitPrerolled(t, ch, 1) // b

	require.NoError(t, ch.JumpTo("c"))
	waitFor(t, time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.order) == 3
	})
	now = now.Add(20 * time.Millisecond)
	d := tickTimed(t, ch, now)
	require.Less(t, d, openDelay*3/2, "first tick after a jump")
	require.Equal(t, slowPTS(3, 0), sourcePTS(ch))
	waitFor(t, 5*time.Second, func() bool { return p.openCount("local:///clips/d.ts") == 1 })
	p.mu.Lock()
	order := slices.Clone(p.order)
	p.mu.Unlock()
	require.Equal(t, []string{"local:///clips/a.ts", "local:///clips/b.ts", "local:///clips/c.ts", "local:///clips/d.ts"}, order)
}

// A cut to an item still being prepared waits for that preparation rather
// than opening the item again, and counts as waited.
func TestChannel_Preroll_CutWaitsForRunningPreparation(t *testing.T) {
	const openDelay = 200 * time.Millisecond
	p := newSlowProvider(openDelay, 0, 50)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"))))
	waitFor(t, time.Second, func() bool {
		ch.preroll.mu.Lock()
		defer ch.preroll.mu.Unlock()
		e := ch.preroll.entries["a"]
		return e != nil && e.started
	})
	onAir(ch)

	d := tickTimed(t, ch, time.Now())
	require.Less(t, d, openDelay*3/2)
	require.Equal(t, slowPTS(1, 0), sourcePTS(ch))
	require.Equal(t, 1, p.openCount("local:///clips/a.ts"), "a opened a second time")
	starts := ch.config.starts.starts
	require.Equal(t, 1.0, testutil.ToFloat64(starts.WithLabelValues("1", prerollWaited)))
	require.Equal(t, 0.0, testutil.ToFloat64(starts.WithLabelValues("1", prerollReady)))
}

// A preparation stands only for the media it prepared: an item whose
// in-point changed is prepared again and starts at the new in-point.
func TestChannel_Preroll_InPointChangeReprepares(t *testing.T) {
	p := newSlowProvider(0, 0, 50)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"))))
	waitPrerolled(t, ch, 1)

	a := clipItem("a")
	a.InPointMs = 40 // two frames at 50 fps
	require.NoError(t, ch.LoadPlaylist(testPlaylist(a)))
	waitFor(t, 5*time.Second, func() bool { return p.openCount("local:///clips/a.ts") == 2 })
	waitPrerolled(t, ch, 1)
	onAir(ch)
	tickTimed(t, ch, time.Now())
	require.Equal(t, slowPTS(1, 2), sourcePTS(ch))
}

// The clips after the cursor's are prepared while their files fit the
// byte budget; the cursor's item always is.
func TestChannel_Preroll_ByteBudget(t *testing.T) {
	p := newSlowProvider(0, 0, 50)
	p.sizes["local:///clips/a.ts"] = 1000
	p.sizes["local:///clips/b.ts"] = 60
	p.sizes["local:///clips/c.ts"] = 60
	var out []emitted
	ch := prerollChannelMax(p, 2, 100, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"), clipItem("c"))))
	waitPrerolled(t, ch, 1)
	onAir(ch)
	tickTimed(t, ch, time.Now())
	waitPrerolled(t, ch, 1) // b
	ch.updatePreroll()
	require.Never(t, func() bool { return p.openCount("local:///clips/c.ts") > 0 }, 100*time.Millisecond, 10*time.Millisecond)

	// The sizes are looked up once, not on every tick.
	p.mu.Lock()
	sized := p.sized
	p.mu.Unlock()
	for i := 0; i < 10; i++ {
		ch.updatePreroll()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	require.Equal(t, sized, p.sized, "sizes looked up again")
}

// A primed iterator loops only when the iterator it wraps does.
func TestPrimeIterator_LoopsOnlyOverALoopingIterator(t *testing.T) {
	_, ok := primeIterator(&errorAfterNIterator{}, nil, nil).(LoopingIterator)
	require.False(t, ok)
	_, ok = primeIterator(NewSliceIterator(fakeFrames(2)), nil, nil).(LoopingIterator)
	require.True(t, ok)
}

// The in-point is found by PTS: where the frame rate changes inside a
// file, a frame index computed from its average rate lands elsewhere.
func TestTrimIterator_SeekFindsInPointByPTS(t *testing.T) {
	encoded := make([]encodedFrame, 60)
	for i := range encoded {
		pts := int64(i) * 30000 // slow first half
		if i >= 30 {
			pts = 29*30000 + int64(i-29)*3000
		}
		encoded[i] = encodedFrame{wireData: []byte{0, 0, 0, 1, byte(i)}, pts: pts, isKeyframe: i%10 == 0}
	}
	inner := &FileFrameIterator{
		encodedFrames:  encoded,
		decoder:        &mockVideoDecoder{width: 320, height: 240},
		decoderFactory: stubDecoderFactory,
		fps:            estimateFPSFromEncodedFrames(encoded),
		totalFrames:    len(encoded),
	}
	trim := NewTrimIterator(inner, 2000, 0) // PTS 180000: frame 6
	require.NoError(t, trim.Seek(0))
	f, err := trim.Next()
	require.NoError(t, err)
	require.Equal(t, int64(180000), f.PTS)
}

// A standby's seek to an item takes the item's prepared iterator rather
// than opening it again.
func TestChannel_Preroll_SeekToItemTakesPreparation(t *testing.T) {
	p := newSlowProvider(0, 0, 50)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"))))
	waitPrerolled(t, ch, 1)
	onAir(ch)
	now := time.Now()
	tickTimed(t, ch, now)
	waitPrerolled(t, ch, 1) // b

	require.NoError(t, ch.SeekToItem(1, 0))
	tickTimed(t, ch, now.Add(20*time.Millisecond))
	require.Equal(t, slowPTS(2, 0), sourcePTS(ch))
	require.Equal(t, 1, p.openCount("local:///clips/b.ts"), "b opened again")
}

// A standby's seek into a prepared item drops the frame the preparation
// decoded: the item goes on from the frame sought, not from its first.
func TestChannel_Preroll_SeekToItemIntoPreparation(t *testing.T) {
	p := newSlowProvider(0, 0, 50)
	var out []emitted
	ch := prerollChannel(p, 1, &out)
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"))))
	waitPrerolled(t, ch, 1)
	onAir(ch)
	now := time.Now()
	tickTimed(t, ch, now)
	waitPrerolled(t, ch, 1) // b

	require.NoError(t, ch.SeekToItem(1, 100)) // frame 5 at 50 fps
	tickTimed(t, ch, now.Add(20*time.Millisecond))
	require.Equal(t, slowPTS(2, 5), sourcePTS(ch))
	require.Equal(t, 1, p.openCount("local:///clips/b.ts"))
}

// A seek on a stopped channel opens nothing it leaves open: no run
// goroutine is there to close it.
func TestChannel_SeekToItem_StoppedChannelLeavesNothingOpen(t *testing.T) {
	p := newSlowProvider(0, 0, 50)
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: p, Width: 64, Height: 48, FPS: 50})
	ch.SetRawVideoOutput(func([]byte, int, int, int64, bool) {})
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"))))
	require.NoError(t, ch.Start())
	ch.Stop()
	require.NoError(t, ch.SeekToItem(0, 0))
	ch.Stop()
	uri := "local:///clips/a.ts"
	waitFor(t, 5*time.Second, func() bool { return p.closeCount(uri) == p.openCount(uri) })
}

// On a stream whose 33-bit PTS wraps, the in-point is found on the
// timeline that runs on past the wrap, and the frames after the wrap
// play.
func TestTrimIterator_InPointAcrossPTSWrap(t *testing.T) {
	const step = 3600 // 25 fps
	start := int64(1<<33) - 100*step
	encoded := make([]encodedFrame, 200)
	for i := range encoded {
		encoded[i] = encodedFrame{
			wireData:   []byte{0, 0, 0, 1, byte(i)},
			pts:        (start + int64(i)*step) % (1 << 33),
			isKeyframe: i%25 == 0,
		}
	}
	inner := newFileFrameIterator(encoded, nil, &mockVideoDecoder{width: 320, height: 240}, stubDecoderFactory)
	require.Equal(t, 125, inner.keyframeBefore((start+125*step)%(1<<33)), "keyframe after the wrap")
	require.Equal(t, 50, inner.keyframeBefore(start+50*step), "keyframe before the wrap")

	trim := NewTrimIterator(inner, 2000, 0) // frame 50, before the wrap
	require.NoError(t, trim.Seek(0))
	n := 0
	for {
		f, err := trim.Next()
		require.NoError(t, err)
		if f == nil {
			break
		}
		n++
	}
	require.Equal(t, 150, n, "frames 50 to 199, across the wrap")
}

// A standby's seek to the start of an item with an in-point lands there by
// a seek, not by decoding every frame before it.
func TestChannel_SeekToItem_InPointSeeks(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg CLI not available")
	}
	path := filepath.Join(t.TempDir(), "in.ts")
	if out, err := exec.Command(ffmpeg, "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=320x240:rate=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "30",
		"-f", "mpegts", path).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg: %v\n%s", err, out)
	}
	fed := 0
	p := fileProvider{factory: func() (VideoDecoder, error) {
		return &countingDecoder{mockVideoDecoder: mockVideoDecoder{width: 320, height: 240}, fed: &fed}, nil
	}}
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: p, Width: 320, Height: 240, FPS: 30})
	ch.SetRawVideoOutput(func([]byte, int, int, int64, bool) {})
	item := PlaylistItem{ID: "x", Type: ItemClip, AssetURI: path, InPointMs: 3000}
	require.NoError(t, ch.LoadPlaylist(testPlaylist(item)))
	onAir(ch)

	require.NoError(t, ch.SeekToItem(0, 0))
	require.False(t, ch.tick(time.Now()))
	require.LessOrEqual(t, fed, 32, "decoded every frame up to the in-point (90)")
}

// A loop whose in-point lies in the first GOP wraps on the decoder it
// has: a seek would start a fresh one to land where the pass starts anyway.
func TestTrimIterator_RewindInFirstGOPKeepsDecoder(t *testing.T) {
	encoded := make([]encodedFrame, 30)
	for i := range encoded {
		encoded[i] = encodedFrame{wireData: []byte{0, 0, 0, 1, byte(i)}, pts: int64(i) * 3000, isKeyframe: i%10 == 0}
	}
	decoders := 0
	inner := &FileFrameIterator{
		encodedFrames: encoded,
		decoder:       &mockVideoDecoder{width: 320, height: 240},
		decoderFactory: func() (VideoDecoder, error) {
			decoders++
			return stubDecoderFactory()
		},
		fps:         30,
		totalFrames: len(encoded),
	}
	trim := NewTrimIterator(inner, 100, 0) // frame 3, in the GOP of frame 0
	trim.SetLoop(true)
	require.NoError(t, trim.Seek(0))
	for {
		f, err := trim.Next()
		require.NoError(t, err)
		if f == nil {
			break
		}
	}
	before := decoders
	require.NoError(t, trim.Rewind())
	f, err := trim.Next()
	require.NoError(t, err)
	require.Equal(t, int64(3*3000), f.PTS)
	require.Equal(t, before, decoders, "the wrap started a fresh decoder")
}
