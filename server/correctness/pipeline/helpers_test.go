//go:build correctness

package pipeline

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

const (
	testWidth  = 320
	testHeight = 240
	testFPS    = 30
	ptsDelta   = 3000 // 90kHz / 30fps
)

// Source colors -- distinct in YUV space, easily identified after lossy compression.
var (
	sourceA = sourceColor{Y: 80, Cb: 90, Cr: 240}   // reddish
	sourceB = sourceColor{Y: 160, Cb: 50, Cr: 60}   // greenish
	sourceC = sourceColor{Y: 200, Cb: 200, Cr: 100} // bluish
	sourceD = sourceColor{Y: 120, Cb: 128, Cr: 128} // neutral gray
)

type sourceColor struct {
	Y, Cb, Cr byte
}

// pipelineHarness wires a real Switcher -> pipeline -> encoder -> muxer chain.
// Frames are fed via IngestRawVideo, processed through the CPU pipeline
// (upstream key -> dve -> compositor -> encode), and captured as MPEG-TS.
type pipelineHarness struct {
	sw           *switcher.Switcher
	programRelay *distribution.Relay
	muxer        *output.TSMuxer
	mixer        *audio.Mixer
	compositor   *graphics.Compositor
	dveComp      *dve.Compositor
	keyProc      *graphics.KeyProcessor
	keyBridge    *graphics.KeyProcessorBridge

	capture *captureViewer

	tsMu   sync.Mutex
	tsData []byte

	sources map[string]sourceColor

	// cutAt is the capture count at the last cut (sinceCut).
	cutAt int

	// Set when withReferenceClock option is used.
	aacBuffer   *clock.AACFrameBuffer
	useRefClock bool
}

type harnessOption func(*harnessConfig)

type harnessConfig struct {
	withMixer      bool
	withCompositor bool
	withLayout     bool
	withKeying     bool
	useRefClock    bool
}

func withMixer() harnessOption {
	return func(c *harnessConfig) { c.withMixer = true }
}

func withCompositor() harnessOption {
	return func(c *harnessConfig) { c.withCompositor = true }
}

func withLayout() harnessOption {
	return func(c *harnessConfig) { c.withLayout = true }
}

func withKeying() harnessOption {
	return func(c *harnessConfig) { c.withKeying = true }
}

func withReferenceClock() harnessOption {
	return func(c *harnessConfig) { c.useRefClock = true }
}

// newPipelineHarness creates a fully wired pipeline test harness.
func newPipelineHarness(t *testing.T, opts ...harnessOption) *pipelineHarness {
	t.Helper()

	var cfg harnessConfig
	for _, o := range opts {
		o(&cfg)
	}

	programRelay := distribution.NewRelay()

	// Create TS muxer to capture output.
	muxer := output.NewTSMuxer()
	h := &pipelineHarness{
		programRelay: programRelay,
		muxer:        muxer,
		sources:      make(map[string]sourceColor),
	}
	muxer.SetOutput(func(data []byte) {
		h.tsMu.Lock()
		h.tsData = append(h.tsData, data...)
		h.tsMu.Unlock()
	})

	// Create a viewer that captures frames AND feeds the muxer.
	capture := newCaptureViewer("pipeline-capture")
	h.capture = capture
	muxViewer := &muxCaptureViewer{
		capture: capture,
		muxer:   muxer,
	}
	programRelay.AddViewer(muxViewer)

	// Create switcher with real pipeline codecs (libx264).
	sw := switcher.NewTestSwitcher(t, programRelay)
	h.sw = sw

	// Wire real encoder factory.
	sw.SetPipelineCodecs(func(w, ht, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return codec.NewFFmpegEncoder("libx264", w, ht, bitrate, fpsNum, fpsDen, 2, nil)
	})

	// Reference clock path: program at testFPS and the sync loop's audio,
	// set up before mixer/pipeline.
	if cfg.useRefClock {
		setRefClockRate(t, sw, testFPS, 1)
		h.useRefClock = true
		h.aacBuffer = newSyncAACBuffer(t, sw)
	}

	// Optional: audio mixer. The sync loop drives it via ProduceSamples and
	// encodes its output to AAC, as production wires it.
	if cfg.withMixer {
		mixer := audio.NewMixer(audio.MixerConfig{
			SampleRate: 48000,
			Channels:   2,
			DecoderFactory: func(sampleRate, channels int) (audio.Decoder, error) {
				return audio.NewFDKDecoder(sampleRate, channels)
			},
		})
		h.mixer = mixer
		sw.SetMixer(mixer)
		sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
			mixer.IngestFrame(sourceKey, frame)
		})
		if h.aacBuffer == nil {
			h.aacBuffer = newSyncAACBuffer(t, sw)
		}
		sw.SetSyncMixer(mixer)
	}

	// Optional: graphics compositor.
	if cfg.withCompositor {
		comp := graphics.NewCompositor()
		h.compositor = comp
		sw.SetCompositor(comp)
	}

	// Optional: DVE compositor (PIP layout).
	if cfg.withLayout {
		dc := dve.NewCompositor(testWidth, testHeight)
		h.dveComp = dc
		sw.SetDVECompositor(dc)
	}

	// Optional: upstream keying.
	if cfg.withKeying {
		kp := graphics.NewKeyProcessor()
		kb := graphics.NewKeyProcessorBridge(kp)
		h.keyProc = kp
		h.keyBridge = kb
		sw.SetKeyBridge(kb)
	}

	// Build the pipeline.
	require.NoError(t, sw.BuildPipeline())

	return h
}

// newPipelineHarnessNoEncode creates a ref clock harness WITHOUT an H.264
// encoder. The sync loop broadcasts raw YUV frames directly, avoiding
// encoder warmup overhead and overload-triggered frame skips. Used for
// testing PTS accuracy in isolation from encoder behavior.
func newPipelineHarnessNoEncode(t *testing.T) *pipelineHarness {
	t.Helper()

	programRelay := distribution.NewRelay()

	h := &pipelineHarness{
		programRelay: programRelay,
		muxer:        output.NewTSMuxer(),
		sources:      make(map[string]sourceColor),
	}

	// Capture viewer only (no muxer, since raw YUV isn't muxable).
	capture := newCaptureViewer("pipeline-capture")
	h.capture = capture
	programRelay.AddViewer(capture)

	// Create switcher (no encoder factory = no pipeline).
	sw := switcher.NewTestSwitcher(t, programRelay)
	h.sw = sw

	setRefClockRate(t, sw, testFPS, 1)
	h.useRefClock = true
	h.aacBuffer = newSyncAACBuffer(t, sw)

	// Create the mixer the sync loop drives.
	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	h.mixer = mixer
	sw.SetMixer(mixer)
	sw.SetSyncMixer(mixer)

	// No BuildPipeline — the sync loop will broadcast raw YUV frames.

	return h
}

// setRefClockRate runs the program, and so the switcher's realtime
// reference clock, at fpsNum/fpsDen.
func setRefClockRate(t *testing.T, sw *switcher.Switcher, fpsNum, fpsDen int) {
	t.Helper()
	require.NoError(t, sw.SetPipelineFormat(switcher.PipelineFormat{
		Name: "test", Width: testWidth, Height: testHeight, FPSNum: fpsNum, FPSDen: fpsDen,
	}))
}

// newSyncAACBuffer gives the sync loop an AAC frame buffer with a real FDK
// encoder, its PTS based as production bases it.
func newSyncAACBuffer(t *testing.T, sw *switcher.Switcher) *clock.AACFrameBuffer {
	t.Helper()
	aacEnc, err := audio.NewFDKEncoder(48000, 2)
	require.NoError(t, err)
	t.Cleanup(func() { aacEnc.Close() })
	aacBuf := clock.NewAACFrameBuffer(2, 48000, func(pcm []float32) ([]byte, error) {
		return aacEnc.Encode(pcm)
	})
	aacBuf.SetBasePTS(clock.PTSBase)
	sw.SetAACBuffer(aacBuf)
	return aacBuf
}

// addSource registers a source with the switcher and records its color.
func (h *pipelineHarness) addSource(key string, color sourceColor) {
	relay := distribution.NewRelay()
	h.sw.RegisterSource(key, relay)
	h.sources[key] = color
}

// feedFrames feeds `count` solid-color YUV frames via IngestRawVideo.
// Frames are stamped with their arrival time, so they are paced at the
// program frame rate, as a live source delivers them.
func (h *pipelineHarness) feedFrames(sourceKey string, count int, startPTS int64) {
	color := h.sources[sourceKey]
	yuv := testutil.GenerateSolidYUV420(testWidth, testHeight, color.Y, color.Cb, color.Cr)

	for i := 0; i < count; i++ {
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(yuv),
			Width:      testWidth,
			Height:     testHeight,
			PTS:        startPTS + int64(i)*ptsDelta,
			IsKeyframe: i == 0,
		}
		pf.SetRefs(1) // managed, as IngestRawVideo requires of producers
		h.sw.IngestRawVideo(sourceKey, pf)
		h.pace(i, count)
	}
}

// feedFramesWithColor feeds `count` solid-color YUV frames with explicit
// color, paced as feedFrames paces them.
func (h *pipelineHarness) feedFramesWithColor(sourceKey string, count int, startPTS int64, y, cb, cr byte) {
	yuv := testutil.GenerateSolidYUV420(testWidth, testHeight, y, cb, cr)

	for i := 0; i < count; i++ {
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(yuv),
			Width:      testWidth,
			Height:     testHeight,
			PTS:        startPTS + int64(i)*ptsDelta,
			IsKeyframe: i == 0,
		}
		pf.SetRefs(1) // managed, as IngestRawVideo requires of producers
		h.sw.IngestRawVideo(sourceKey, pf)
		h.pace(i, count)
	}
}

// pace waits one program frame period after a frame is fed. The
// synchronizer buffers a few frames per source and a tick shows a frame
// two periods after its arrival, so frames fed faster than the program
// rate are dropped unseen.
func (h *pipelineHarness) pace(int, int) {
	time.Sleep(h.sw.PipelineFormat().FrameDuration())
}

// cut performs a cut to the given source.
func (h *pipelineHarness) cut(t *testing.T, sourceKey string) {
	t.Helper()
	h.cutAt = h.capture.videoCount()
	require.NoError(t, h.sw.Cut(t.Context(), sourceKey))
}

// cutLeadIn bounds where the black lead-in of a cut can start: the frames
// of the old source still in the encoder come out first.
const cutLeadIn = 10

// sinceCut returns the decoded frames broadcast since the last cut. After
// the old source's frames still in flight, the program is black until the
// new source's first frame is due, two frame periods after it arrived:
// that run of black frames is left out.
func (h *pipelineHarness) sinceCut(decoded []decodedFrame) []decodedFrame {
	i := 0
	for i < len(decoded) && decoded[i].Idx < h.cutAt {
		i++
	}
	out := append([]decodedFrame(nil), decoded[i:]...)
	b := 0
	for b < len(out) && b < cutLeadIn && !isBroadcastBlack(out[b]) {
		b++
	}
	e := b
	for e < len(out) && isBroadcastBlack(out[e]) {
		e++
	}
	return append(out[:b], out[e:]...)
}

// waitForPipeline waits for pipeline processing to complete.
func (h *pipelineHarness) waitForPipeline(d time.Duration) {
	time.Sleep(d)
}

// writeTS writes accumulated TS bytes to a temp file and returns the path.
func (h *pipelineHarness) writeTS(t *testing.T) string {
	t.Helper()

	h.tsMu.Lock()
	tsBytes := make([]byte, len(h.tsData))
	copy(tsBytes, h.tsData)
	h.tsMu.Unlock()

	if len(tsBytes) == 0 {
		t.Fatal("No TS data produced -- pipeline did not generate output")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline_test.ts")
	require.NoError(t, os.WriteFile(path, tsBytes, 0o644))
	return path
}

// tsLen returns current TS data length.
func (h *pipelineHarness) tsLen() int {
	h.tsMu.Lock()
	defer h.tsMu.Unlock()
	return len(h.tsData)
}

// stopClock stops the switcher's reference clock and sync loop, so the
// captured output stops growing before it is inspected.
func (h *pipelineHarness) stopClock() {
	h.sw.Close()
}

// waitForVideoFrames waits until the capture viewer has at least n video frames.
// Uses require.Eventually with a reasonable timeout.
func (h *pipelineHarness) waitForVideoFrames(t *testing.T, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return h.capture.videoCount() >= n
	}, 5*time.Second, 5*time.Millisecond,
		"expected at least %d video frames, got %d", n, h.capture.videoCount())
}

// waitForAudioFrames waits until the capture viewer has at least n audio frames.
func (h *pipelineHarness) waitForAudioFrames(t *testing.T, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return h.capture.audioCount() >= n
	}, 5*time.Second, 5*time.Millisecond,
		"expected at least %d audio frames, got %d", n, h.capture.audioCount())
}

// close tears down all resources.
func (h *pipelineHarness) close() {
	if h.mixer != nil {
		_ = h.mixer.Close()
	}
	h.sw.Close()
}

// captureViewer captures frames broadcast to the program relay.
type captureViewer struct {
	id     string
	mu     sync.Mutex
	videos []*media.VideoFrame
	audios []*media.AudioFrame
}

func newCaptureViewer(id string) *captureViewer {
	return &captureViewer{id: id}
}

func (c *captureViewer) ID() string                       { return c.id }
func (c *captureViewer) SendCaptions(_ *ccx.CaptionFrame) {}
func (c *captureViewer) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{ID: c.id}
}

func (c *captureViewer) SendVideo(frame *media.VideoFrame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *frame
	if len(frame.WireData) > 0 {
		cp.WireData = make([]byte, len(frame.WireData))
		copy(cp.WireData, frame.WireData)
	}
	if len(frame.SPS) > 0 {
		cp.SPS = make([]byte, len(frame.SPS))
		copy(cp.SPS, frame.SPS)
	}
	if len(frame.PPS) > 0 {
		cp.PPS = make([]byte, len(frame.PPS))
		copy(cp.PPS, frame.PPS)
	}
	c.videos = append(c.videos, &cp)
}

func (c *captureViewer) SendAudio(frame *media.AudioFrame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *frame
	if len(frame.Data) > 0 {
		cp.Data = make([]byte, len(frame.Data))
		copy(cp.Data, frame.Data)
	}
	c.audios = append(c.audios, &cp)
}

func (c *captureViewer) videoCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.videos)
}

func (c *captureViewer) audioCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.audios)
}

func (c *captureViewer) videosCopy() []*media.VideoFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*media.VideoFrame, len(c.videos))
	copy(out, c.videos)
	return out
}

func (c *captureViewer) audiosCopy() []*media.AudioFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*media.AudioFrame, len(c.audios))
	copy(out, c.audios)
	return out
}

// muxCaptureViewer wraps a captureViewer and feeds frames to a TSMuxer.
type muxCaptureViewer struct {
	capture *captureViewer
	muxer   *output.TSMuxer
}

func (m *muxCaptureViewer) ID() string { return m.capture.id }
func (m *muxCaptureViewer) SendVideo(frame *media.VideoFrame) {
	m.capture.SendVideo(frame)
	_ = m.muxer.WriteVideo(frame)
}
func (m *muxCaptureViewer) SendAudio(frame *media.AudioFrame) {
	m.capture.SendAudio(frame)
	_ = m.muxer.WriteAudio(frame)
}
func (m *muxCaptureViewer) SendCaptions(_ *ccx.CaptionFrame) {}
func (m *muxCaptureViewer) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{ID: m.capture.id}
}

// copyBytes returns an independent copy of a byte slice.
func copyBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// decodedFrame holds a decoded YUV frame with metadata.
type decodedFrame struct {
	YUV    []byte
	Width  int
	Height int
	PTS    int64
	Idx    int // index of the captured frame it was decoded from
}

// maxDecodeFailureRatio bounds how many of the frames we actually attempt to
// decode are allowed to fail (decoder rejects them or returns nil). Silently
// dropping decode failures would let an encoder that emits corrupt/undecodable
// frames pass tests that only check the count of survivors, hiding exactly the
// encode bugs this harness exists to catch. We tolerate a small fraction (one
// transient hiccup in a tiny batch) but fail the test if corruption is
// widespread. Only enforced once there are enough attempts to be meaningful.
const (
	maxDecodeFailureRatio   = 0.10
	minDecodeAttemptsToGate = 10
)

// decodeCapturedFrames decodes captured video frames from the capture viewer
// by feeding them through an FFmpeg decoder.
//
// Frames with no WireData carry nothing to decode and are skipped without
// penalty. Frames that DO carry data but the decoder rejects are counted as
// decode failures: if their ratio among attempted decodes exceeds
// maxDecodeFailureRatio the test fails, so corrupt/undecodable output cannot
// silently vanish from the returned slice.
func decodeCapturedFrames(t *testing.T, videos []*media.VideoFrame) []decodedFrame {
	t.Helper()

	dec, err := codec.NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	var decoded []decodedFrame
	attempts := 0 // frames that carried data we tried to decode
	failures := 0 // attempted decodes the decoder rejected
	for idx, vf := range videos {
		if len(vf.WireData) == 0 {
			continue
		}
		attempts++
		annexB := codec.AVC1ToAnnexB(vf.WireData)
		yuv, w, h, decErr := dec.Decode(annexB)
		if decErr != nil || yuv == nil {
			failures++
			continue
		}
		decoded = append(decoded, decodedFrame{
			YUV:    append([]byte(nil), yuv...),
			Width:  w,
			Height: h,
			PTS:    vf.PTS,
			Idx:    idx,
		})
	}

	if failures > 0 {
		t.Logf("MEASURE: decode failures = %d / %d attempted frames (%.1f%%)",
			failures, attempts, 100*float64(failures)/float64(attempts))
	}
	if attempts >= minDecodeAttemptsToGate {
		ratio := float64(failures) / float64(attempts)
		require.LessOrEqualf(t, ratio, maxDecodeFailureRatio,
			"decoder rejected %d of %d frames (%.1f%% > %.0f%%) — encoder may be emitting corrupt/undecodable output",
			failures, attempts, 100*ratio, 100*maxDecodeFailureRatio)
	}

	// The program is black from a cut until the source's first frame is
	// due, two frame periods after it arrived. Leave that lead-in out.
	lead := 0
	for lead < len(decoded) && isBroadcastBlack(decoded[lead]) {
		lead++
	}
	if lead > 0 {
		t.Logf("MEASURE: %d leading black frames before the first source frame", lead)
	}
	return decoded[lead:]
}

// isBroadcastBlack reports whether a decoded YUV420 frame is broadcast
// black (Y=16, Cb=Cr=128) within compression error.
func isBroadcastBlack(df decodedFrame) bool {
	n := df.Width * df.Height
	if len(df.YUV) < n*3/2 {
		return false
	}
	avg := func(p []byte) float64 {
		var sum uint64
		for _, v := range p {
			sum += uint64(v)
		}
		return float64(sum) / float64(len(p))
	}
	near := func(v, want, tol float64) bool { return math.Abs(v-want) <= tol }
	return near(avg(df.YUV[:n]), 16, 2) && near(avg(df.YUV[n:n+n/4]), 128, 4) &&
		near(avg(df.YUV[n+n/4:n*3/2]), 128, 4)
}

// averageY computes the average Y value from a YUV420 frame's Y plane.
func averageY(yuv []byte, w, h int) float64 {
	if len(yuv) < w*h {
		return 0
	}
	var sum uint64
	for i := 0; i < w*h; i++ {
		sum += uint64(yuv[i])
	}
	return float64(sum) / float64(w*h)
}

// createWhiteOverlayPNG creates a white opaque RGBA PNG for graphics tests.
func createWhiteOverlayPNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			offset := (y*w + x) * 4
			img.Pix[offset] = 255   // R
			img.Pix[offset+1] = 255 // G
			img.Pix[offset+2] = 255 // B
			img.Pix[offset+3] = 255 // A
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
