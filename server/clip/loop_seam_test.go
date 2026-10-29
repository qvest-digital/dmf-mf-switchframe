package clip

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/codec"
)

// threadedDecoder behaves like a frame-threaded decoder: it returns a
// picture only once depth frames are in flight, each call takes cost, and
// Flush throws away what it holds, so it returns nothing again for its
// next depth calls. The picture is the frame's last byte.
type threadedDecoder struct {
	depth int
	cost  time.Duration
	queue [][]byte
	eos   bool
}

func (d *threadedDecoder) Decode(annexB []byte) ([]byte, int, int, error) {
	time.Sleep(d.cost)
	d.queue = append(d.queue, []byte{annexB[len(annexB)-1]})
	if len(d.queue) <= d.depth {
		return nil, 0, 0, errors.New("no output frame yet (buffering)")
	}
	out := d.queue[0]
	d.queue = d.queue[1:]
	return out, 16, 16, nil
}

func (d *threadedDecoder) SendEOS() error { d.eos = true; return nil }

func (d *threadedDecoder) ReceiveFrame() ([]byte, int, int, error) {
	if !d.eos || len(d.queue) == 0 {
		d.eos = false
		return nil, 0, 0, errors.New("no more frames")
	}
	out := d.queue[0]
	d.queue = d.queue[1:]
	return out, 16, 16, nil
}

func (d *threadedDecoder) Flush() { d.queue, d.eos = nil, false }
func (d *threadedDecoder) Close() {}

// A looping clip keeps its cadence across the wrap: every picture once per
// pass, one output period apart, audio included. Restarting the decoder at
// the wrap held the last picture while a frame-threaded decoder refilled,
// and left the slots of that time without audio.
func TestPlayerLoopWrapKeepsCadence(t *testing.T) {
	const pictures = 20
	clip := make([]BufferedFrame, pictures)
	audio := make([]BufferedAudioFrame, pictures)
	for i := range clip {
		// Length-prefixed NAL {0x41, i}: the decoder sees i as its last byte.
		clip[i] = BufferedFrame{wireData: []byte{0, 0, 0, 2, 0x41, byte(i)}, pts: int64(i) * 1800, isKeyframe: i == 0}
		audio[i] = BufferedAudioFrame{data: []byte{byte(i)}, sampleRate: 48000, channels: 2}
	}
	dec := &threadedDecoder{depth: 4, cost: 6 * time.Millisecond}

	var mu sync.Mutex
	var shown []byte
	var at []time.Time
	audioOut := 0
	p := NewPlayer(PlayerConfig{
		Clip:         clip,
		AudioClip:    audio,
		Speed:        1,
		Loop:         true,
		OutputFPSNum: 50,
		OutputFPSDen: 1,
		Decoder:      dec,
		DecodeFrame:  dec.Decode,
		RawVideoOutput: func(yuv []byte, _, _ int, _ int64, _ bool) {
			mu.Lock()
			shown = append(shown, yuv[0])
			at = append(at, time.Now())
			mu.Unlock()
		},
		AudioOutput: func([]byte, int64, int, int) {
			mu.Lock()
			audioOut++
			mu.Unlock()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	// Two wraps: 2.5 passes of 20 pictures at 50 fps.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(shown) >= 2*pictures+pictures/2
	}, 5*time.Second, 5*time.Millisecond)
	cancel()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()
	for i, v := range shown[:2*pictures] {
		require.Equal(t, byte(i%pictures), v, "picture %d of %v", i, shown)
	}
	// Output i is due i periods after output 0, which anchors the pacing,
	// and comes out a decode call after that. Measured against that grid,
	// no output is ahead of its slot or held far past it, and a wrap costs
	// no time: each pass sits where the first one does. A pass's median
	// offset ignores the odd output a busy host delays; a decoder refill at
	// the wrap moves every output after it.
	period := 20 * time.Millisecond
	offset := make([]time.Duration, len(at))
	for i := range at {
		offset[i] = at[i].Sub(at[0]) - time.Duration(i)*period
		assert.Greater(t, offset[i], -period/2, "output %d (picture %d) ahead of its slot", i, shown[i])
		assert.Less(t, offset[i], 3*period, "output %d (picture %d) held past its slot", i, shown[i])
	}
	first := medianDuration(offset[:pictures])
	for pass := 1; pass*pictures < len(offset); pass++ {
		later := medianDuration(offset[pass*pictures : min((pass+1)*pictures, len(offset))])
		assert.Less(t, later-first, period/2, "pass %d behind the first by %v", pass, later-first)
		assert.Greater(t, later-first, -period/2, "pass %d ahead of the first by %v", pass, first-later)
	}
	assert.GreaterOrEqual(t, audioOut, 2*pictures, "audio frames emitted over two passes")
}

func medianDuration(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[len(s)/2]
}

// Resuming after a pause picks up at the output rate: the slots that fell
// due during the pause are not caught up, which for a looping clip carrying
// its pacing across wraps would replay passes at full speed.
func TestPlayerResumeAfterPauseKeepsRate(t *testing.T) {
	const pictures = 20
	clip := make([]BufferedFrame, pictures)
	for i := range clip {
		clip[i] = BufferedFrame{wireData: []byte{0, 0, 0, 2, 0x41, byte(i)}, pts: int64(i) * 1800, isKeyframe: i == 0}
	}
	dec := &threadedDecoder{depth: 4}
	var mu sync.Mutex
	var at []time.Time
	p := NewPlayer(PlayerConfig{
		Clip: clip, Speed: 1, Loop: true, OutputFPSNum: 50, OutputFPSDen: 1,
		Decoder: dec, DecodeFrame: dec.Decode,
		RawVideoOutput: func([]byte, int, int, int64, bool) { mu.Lock(); at = append(at, time.Now()); mu.Unlock() },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	p.Pause()
	time.Sleep(time.Second)
	resumed := time.Now()
	p.Resume()
	time.Sleep(150 * time.Millisecond)
	cancel()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()
	after := 0
	for _, t0 := range at {
		if t0.After(resumed) {
			after++
		}
	}
	// 150 ms is 7.5 periods at 50 fps.
	assert.LessOrEqual(t, after, 10, "outputs in the 150 ms after resuming")
	assert.GreaterOrEqual(t, after, 5, "outputs in the 150 ms after resuming")
}

// keyedDecoder is a threadedDecoder that, like FFmpeg's, fails the frames
// fed before its first keyframe and puts out nothing for them: they
// reference pictures it never had. Flush drops its references again.
type keyedDecoder struct {
	threadedDecoder
	keyed bool
}

func (d *keyedDecoder) Decode(annexB []byte) ([]byte, int, int, error) {
	// The NAL header is the second-to-last byte, after any SPS/PPS.
	if annexB[len(annexB)-2]&0x1f == 5 {
		d.keyed = true
	}
	if !d.keyed {
		return nil, 0, 0, errors.New("no reference picture")
	}
	return d.threadedDecoder.Decode(annexB)
}

func (d *keyedDecoder) Flush() { d.threadedDecoder.Flush(); d.keyed = false }

// loopRecorder collects what a looping manager player outputs: the
// pictures in order, the audio, and how much audio had gone out when
// each picture did.
type loopRecorder struct {
	mu          sync.Mutex
	pics        [][]byte
	audio       [][]byte
	audioAtPics []int
}

func (r *loopRecorder) wire(m *Manager) {
	m.SetRawVideoOutput(func(_ string, yuv []byte, _, _ int, _ int64, _ bool) {
		r.mu.Lock()
		r.pics = append(r.pics, slices.Clone(yuv))
		r.audioAtPics = append(r.audioAtPics, len(r.audio))
		r.mu.Unlock()
	})
	m.SetAudioOutput(func(_ string, data []byte, _ int64, _, _ int) {
		r.mu.Lock()
		r.audio = append(r.audio, slices.Clone(data))
		r.mu.Unlock()
	})
}

func (r *loopRecorder) waitPics(t *testing.T, n int, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.pics) >= n
	}, timeout, 5*time.Millisecond)
}

// A clip cut mid-GOP starts with frames before its first keyframe. A
// fresh decoder fails them, and the decoder a looping player keeps into
// the next pass decodes them against the previous pass's pictures: wrong
// pictures, and a pass longer than the first. Every pass has to show the
// pictures from the keyframe on, with the audio from there in step.
func TestManagerLoopSkipsFramesBeforeFirstKeyframe(t *testing.T) {
	const n, key, passes = 20, 5, 3
	frames := make([]BufferedFrame, n)
	audio := make([]BufferedAudioFrame, n)
	for i := range frames {
		nal := byte(0x41)
		if i == key {
			nal = 0x65
		}
		frames[i] = BufferedFrame{wireData: []byte{0, 0, 0, 2, nal, byte(i)}, pts: int64(i) * 1800, isKeyframe: i == key}
		audio[i] = BufferedAudioFrame{data: []byte{byte(i)}, pts: int64(i) * 1800, sampleRate: 48000, channels: 2}
	}
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)
	mgr := NewManager(store, ManagerConfig{
		DemuxFunc: func(string) ([]BufferedFrame, []BufferedAudioFrame, error) {
			return slices.Clone(frames), slices.Clone(audio), nil
		},
		DecoderFactory: func() (VideoDecoder, error) {
			return &keyedDecoder{threadedDecoder: threadedDecoder{depth: 2}}, nil
		},
		OutputRate: func() (int, int) { return 50, 1 },
	})
	defer mgr.Close()
	var rec loopRecorder
	rec.wire(mgr)
	c := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.Play(1, 1.0, true))
	const shown = n - key
	rec.waitPics(t, passes*shown, 5*time.Second)
	require.NoError(t, mgr.Stop(1))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	var want, got []byte
	for pass := 0; pass < passes; pass++ {
		for i := key; i < n; i++ {
			want = append(want, byte(i))
		}
	}
	for _, p := range rec.pics[:passes*shown] {
		got = append(got, p[0])
	}
	require.Equal(t, want, got, "pictures over %d passes", passes)
	var sound []byte
	for _, a := range rec.audio {
		sound = append(sound, a[0])
	}
	require.GreaterOrEqual(t, len(sound), passes*shown-1)
	require.Equal(t, want[:passes*shown-1], sound[:passes*shown-1], "audio over %d passes", passes)
	for i, a := range rec.audioAtPics[:passes*shown] {
		require.Equal(t, i, a, "audio frames out when picture %d went out", i)
	}
}

// The same with the real demuxer and decoder: a 50 fps clip whose first 30
// frames are cut, so 70 frames precede its first keyframe. Every pass
// puts out the pictures of the first, and as much audio.
func TestManagerLoopCutBeforeKeyframeIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dec, err := codec.NewVideoDecoderForCodec("h264")
	if err != nil {
		t.Skipf("FFmpeg decoder not available (non-cgo build?): %v", err)
	}
	dec.Close()
	dir := t.TempDir()
	writeCutTS(t, filepath.Join(dir, "cut.ts"))

	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)
	mgr := NewManager(store, ManagerConfig{
		DecoderFactoryForCodec: func(name string) (VideoDecoder, error) {
			return codec.NewVideoDecoderForCodec(name)
		},
	})
	defer mgr.Close()
	var rec loopRecorder
	rec.wire(mgr)
	c := &Clip{Name: "cut", Filename: "cut.ts", Source: SourceUpload, Codec: "h264", Width: 320, Height: 240}
	require.NoError(t, store.Add(c))

	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.Play(1, 1.0, true))
	const shown, passes = 100, 3
	rec.waitPics(t, passes*shown+1, 15*time.Second)
	require.NoError(t, mgr.Stop(1))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	for pass := 1; pass < passes; pass++ {
		for i := 0; i < shown; i++ {
			require.True(t, bytes.Equal(rec.pics[i], rec.pics[pass*shown+i]),
				"pass %d picture %d differs from the first pass", pass, i)
		}
	}
	// 2 s of 48 kHz AAC is 93.75 frames of 1024 samples.
	first := rec.audioAtPics[shown]
	assert.InDelta(t, 94, first, 1, "audio frames in the first pass")
	for pass := 1; pass < passes; pass++ {
		got := rec.audioAtPics[(pass+1)*shown] - rec.audioAtPics[pass*shown]
		assert.Equal(t, first, got, "audio frames in pass %d", pass)
	}
}

// writeCutTS writes 4 s of 320x240 50 fps H.264 with AAC to path, one
// keyframe every 100 frames and the first 30 frames cut, so the 170 frames
// left start with 70 before their first keyframe. It skips the test
// without an ffmpeg that can.
func writeCutTS(t *testing.T, path string) {
	t.Helper()
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg CLI not available, skipping integration test")
	}
	out, err := exec.Command(ffmpegPath, "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=320x240:rate=50",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4:sample_rate=48000",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-g", "100", "-bf", "0", "-bsf:v", `noise=drop=lt(n\,30)`,
		"-c:a", "aac", "-b:a", "64k", "-ac", "2",
		"-f", "mpegts", path,
	).CombinedOutput()
	if err != nil {
		t.Skipf("ffmpeg failed to generate test TS: %v\n%s", err, out)
	}
}
