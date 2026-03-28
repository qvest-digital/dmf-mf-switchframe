//go:build correctness

package pipeline

import (
	"bytes"
	"image"
	"image/png"
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
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/layout"
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
	sourceA = sourceColor{Y: 80, Cb: 90, Cr: 240}  // reddish
	sourceB = sourceColor{Y: 160, Cb: 50, Cr: 60}   // greenish
	sourceC = sourceColor{Y: 200, Cb: 200, Cr: 100}  // bluish
	sourceD = sourceColor{Y: 120, Cb: 128, Cr: 128}  // neutral gray
)

type sourceColor struct {
	Y, Cb, Cr byte
}

// pipelineHarness wires a real Switcher -> pipeline -> encoder -> muxer chain.
// Frames are fed via IngestRawVideo, processed through the CPU pipeline
// (upstream key -> layout -> compositor -> encode), and captured as MPEG-TS.
type pipelineHarness struct {
	sw           *switcher.Switcher
	programRelay *distribution.Relay
	muxer        *output.TSMuxer
	mixer        *audio.Mixer
	compositor   *graphics.Compositor
	layoutComp   *layout.Compositor
	keyProc      *graphics.KeyProcessor
	keyBridge    *graphics.KeyProcessorBridge

	capture *captureViewer

	tsMu   sync.Mutex
	tsData []byte

	sources map[string]sourceColor
}

type harnessOption func(*harnessConfig)

type harnessConfig struct {
	withMixer      bool
	withCompositor bool
	withLayout     bool
	withKeying     bool
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
	sw := switcher.NewTestSwitcher(programRelay)
	h.sw = sw

	// Wire real encoder factory.
	sw.SetPipelineCodecs(func(w, ht, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return codec.NewFFmpegEncoder("libx264", w, ht, bitrate, fpsNum, fpsDen, 2, nil)
	})

	// Optional: audio mixer.
	if cfg.withMixer {
		mixer := audio.NewMixer(audio.MixerConfig{
			SampleRate: 48000,
			Channels:   2,
			Output: func(frame *media.AudioFrame) {
				programRelay.BroadcastAudio(frame)
			},
			DecoderFactory: func(sampleRate, channels int) (audio.Decoder, error) {
				return audio.NewFDKDecoder(sampleRate, channels)
			},
			EncoderFactory: func(sampleRate, channels int) (audio.Encoder, error) {
				return audio.NewFDKEncoder(sampleRate, channels)
			},
		})
		h.mixer = mixer
		sw.SetMixer(mixer)
		sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
			mixer.IngestFrame(sourceKey, frame)
		})

		// Mirror production wiring (app.go): seed audio PTS from video so
		// both streams share the same PTS epoch. Without this, the mixer
		// self-seeds from wall clock which diverges from synthetic video PTS.
		mixer.ExpectVideoSeed()
		sw.SetOnFirstVideoPTS(func(pts int64) {
			mixer.SeedPTSFromVideo(pts)
		})
		sw.EnableWallClockVideoPTS()
	}

	// Optional: graphics compositor.
	if cfg.withCompositor {
		comp := graphics.NewCompositor()
		h.compositor = comp
		sw.SetCompositor(comp)
	}

	// Optional: layout compositor.
	if cfg.withLayout {
		lc := layout.NewCompositor(testWidth, testHeight)
		h.layoutComp = lc
		sw.SetLayoutCompositor(lc)
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

// addSource registers a source with the switcher and records its color.
func (h *pipelineHarness) addSource(key string, color sourceColor) {
	relay := distribution.NewRelay()
	h.sw.RegisterSource(key, relay)
	h.sources[key] = color
}

// feedFrames feeds `count` solid-color YUV frames via IngestRawVideo.
// Frames are paced at ~5ms intervals to avoid overwhelming the async
// pipeline channel (depth 8, newest-wins drop policy).
func (h *pipelineHarness) feedFrames(sourceKey string, count int, startPTS int64) {
	color := h.sources[sourceKey]
	yuv := testutil.GenerateSolidYUV420(testWidth, testHeight, color.Y, color.Cb, color.Cr)

	for i := 0; i < count; i++ {
		pf := &switcher.ProcessingFrame{
			YUV:        copyBytes(yuv),
			Width:      testWidth,
			Height:     testHeight,
			PTS:        startPTS + int64(i)*ptsDelta,
			IsKeyframe: i == 0,
		}
		h.sw.IngestRawVideo(sourceKey, pf)
		if i < count-1 {
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// feedFramesWithColor feeds `count` solid-color YUV frames with explicit color.
// Paced at ~5ms intervals to avoid pipeline drops.
func (h *pipelineHarness) feedFramesWithColor(sourceKey string, count int, startPTS int64, y, cb, cr byte) {
	yuv := testutil.GenerateSolidYUV420(testWidth, testHeight, y, cb, cr)

	for i := 0; i < count; i++ {
		pf := &switcher.ProcessingFrame{
			YUV:        copyBytes(yuv),
			Width:      testWidth,
			Height:     testHeight,
			PTS:        startPTS + int64(i)*ptsDelta,
			IsKeyframe: i == 0,
		}
		h.sw.IngestRawVideo(sourceKey, pf)
		if i < count-1 {
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// cut performs a cut to the given source.
func (h *pipelineHarness) cut(t *testing.T, sourceKey string) {
	t.Helper()
	require.NoError(t, h.sw.Cut(t.Context(), sourceKey))
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
}

// decodeCapturedFrames decodes captured video frames from the capture viewer
// by feeding them through an FFmpeg decoder.
func decodeCapturedFrames(t *testing.T, videos []*media.VideoFrame) []decodedFrame {
	t.Helper()

	dec, err := codec.NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	var decoded []decodedFrame
	for _, vf := range videos {
		if len(vf.WireData) == 0 {
			continue
		}
		annexB := codec.AVC1ToAnnexB(vf.WireData)
		yuv, w, h, decErr := dec.Decode(annexB)
		if decErr != nil || yuv == nil {
			continue
		}
		decoded = append(decoded, decodedFrame{
			YUV:    append([]byte(nil), yuv...),
			Width:  w,
			Height: h,
			PTS:    vf.PTS,
		})
	}

	return decoded
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
