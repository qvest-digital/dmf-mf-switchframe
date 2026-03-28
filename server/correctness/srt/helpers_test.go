//go:build correctness

package srt

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	srtgo "github.com/zsiec/srtgo"
	"github.com/zsiec/switchframe/server/srt"
)

// capturedVideoFrame holds a single decoded video frame's metadata.
type capturedVideoFrame struct {
	Key    string
	Width  int
	Height int
	PTS    int64
	Size   int // len(yuv)
}

// capturedAudioFrame holds a single decoded audio frame's metadata.
type capturedAudioFrame struct {
	Key        string
	PTS        int64
	SampleRate int
	Channels   int
	Samples    int // len(pcm)
}

// srtHarness manages an SRT listener with frame capture for correctness testing.
type srtHarness struct {
	t        *testing.T
	addr     string
	listener *srt.Listener
	store    *srt.Store
	ctx      context.Context
	cancel   context.CancelFunc
	runDone  chan error
	log      *slog.Logger

	mu     sync.Mutex
	vFrames []capturedVideoFrame
	aFrames []capturedAudioFrame
}

// newSRTHarness creates a harness with an SRT listener on a random port.
// The listener captures decoded video and audio frames from any connected source.
func newSRTHarness(t *testing.T) *srtHarness {
	t.Helper()

	addr := findFreeUDPAddr(t)
	dir := t.TempDir()
	store, err := srt.NewStore(filepath.Join(dir, "srt_sources.json"))
	if err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

	h := &srtHarness{
		t:       t,
		addr:    addr,
		store:   store,
		ctx:     ctx,
		cancel:  cancel,
		runDone: make(chan error, 1),
		log:     log,
	}

	l, err := srt.NewListener(srt.ListenerConfig{
		Addr:           addr,
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg srt.SourceConfig, conn *srtgo.Conn) {
			stats := srt.NewConnStats(cfg.Mode, cfg.StreamID, cfg.LatencyMs)
			src := srt.NewSource(cfg, conn, stats, log)
			src.OnRawVideo = func(key string, yuv []byte, width, height int, pts int64) {
				h.mu.Lock()
				h.vFrames = append(h.vFrames, capturedVideoFrame{
					Key:    key,
					Width:  width,
					Height: height,
					PTS:    pts,
					Size:   len(yuv),
				})
				h.mu.Unlock()
			}
			src.OnRawAudio = func(key string, pcm []float32, pts int64, sampleRate, channels int) {
				h.mu.Lock()
				h.aFrames = append(h.aFrames, capturedAudioFrame{
					Key:        key,
					PTS:        pts,
					SampleRate: sampleRate,
					Channels:   channels,
					Samples:    len(pcm),
				})
				h.mu.Unlock()
			}
			if err := src.Start(ctx); err != nil {
				t.Errorf("source start failed: %v", err)
			}
		},
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}

	h.listener = l
	go func() {
		h.runDone <- l.Run(ctx)
	}()

	// Give listener time to start accepting.
	time.Sleep(100 * time.Millisecond)

	return h
}

// videoFrames returns a snapshot of captured video frames.
func (h *srtHarness) videoFrames() []capturedVideoFrame {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]capturedVideoFrame, len(h.vFrames))
	copy(out, h.vFrames)
	return out
}

// audioFrames returns a snapshot of captured audio frames.
func (h *srtHarness) audioFrames() []capturedAudioFrame {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]capturedAudioFrame, len(h.aFrames))
	copy(out, h.aFrames)
	return out
}

// waitForVideoFrames polls until at least n video frames are captured, or times out.
func (h *srtHarness) waitForVideoFrames(n int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		if len(h.videoFrames()) >= n {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// waitForAudioFrames polls until at least n audio frames are captured, or times out.
func (h *srtHarness) waitForAudioFrames(n int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		if len(h.audioFrames()) >= n {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// close shuts down the listener and waits for cleanup.
func (h *srtHarness) close() {
	h.cancel()
	select {
	case <-h.runDone:
	case <-time.After(5 * time.Second):
		h.t.Error("timeout waiting for listener Run to return")
	}
}

// pushTSFile dials the SRT listener and sends a TS file in 1316-byte chunks
// with wall-clock pacing that approximates real-time delivery. The write loop
// runs in a goroutine. The connection is NOT closed when writing finishes --
// the caller is responsible for closing it (this keeps the SRT receiver buffer
// alive long enough for the decoder to drain it).
func (h *srtHarness) pushTSFile(streamID string, tsPath string) *srtgo.Conn {
	h.t.Helper()

	cfg := srtgo.DefaultConfig()
	cfg.StreamID = streamID

	var conn *srtgo.Conn
	var err error
	for i := 0; i < 20; i++ {
		conn, err = srtgo.Dial(h.addr, cfg)
		if err == nil {
			break
		}
		time.Sleep(time.Duration(10*(i+1)) * time.Millisecond)
	}
	if err != nil {
		h.t.Fatalf("dial SRT failed after retries: %v", err)
	}

	data, err := os.ReadFile(tsPath)
	if err != nil {
		h.t.Fatalf("read TS file: %v", err)
	}

	// Calculate pacing: spread the data over roughly the file's duration.
	// For a 3s file with N chunks, sleep duration between chunks should be
	// approximately 3s / N. This ensures the decoder sees data at real-time
	// rate rather than getting a burst and then EOF.
	nChunks := (len(data) + 1315) / 1316
	// Assume ~2 Mbps bitrate for a 320x240@30fps file. Pace at the file's
	// bitrate by calculating bytes-per-second and sleeping accordingly.
	// We use a minimum of 1ms per chunk and a maximum that spreads the data
	// over 80% of the estimated file duration (allow some headroom).
	pacingPerChunk := time.Millisecond // minimum
	if nChunks > 0 {
		// Estimate file duration from size assuming ~400 kbps for 320x240.
		// Better: use the known duration from test parameters. We'll compute
		// it from bitrate as a rough heuristic.
		fileBits := int64(len(data)) * 8
		bitrateGuess := int64(400_000) // 400 kbps for 320x240@30fps ultrafast
		durationUs := fileBits * 1_000_000 / bitrateGuess
		totalDuration := time.Duration(durationUs) * time.Microsecond
		pacingPerChunk = totalDuration / time.Duration(nChunks)
		if pacingPerChunk < time.Millisecond {
			pacingPerChunk = time.Millisecond
		}
		if pacingPerChunk > 100*time.Millisecond {
			pacingPerChunk = 100 * time.Millisecond
		}
	}

	go func() {
		// NOTE: connection is NOT closed here. Caller must close.
		for i := 0; i < len(data); i += 1316 {
			select {
			case <-h.ctx.Done():
				return
			default:
			}
			end := i + 1316
			if end > len(data) {
				end = len(data)
			}
			_, werr := conn.Write(data[i:end])
			if werr != nil {
				return
			}
			time.Sleep(pacingPerChunk)
		}
	}()

	return conn
}

// pushTSFileSync dials and sends a TS file synchronously, returning after all
// data is written or an error occurs. Does NOT close the connection.
func (h *srtHarness) pushTSFileSync(streamID string, tsPath string) *srtgo.Conn {
	h.t.Helper()

	cfg := srtgo.DefaultConfig()
	cfg.StreamID = streamID

	var conn *srtgo.Conn
	var err error
	for i := 0; i < 20; i++ {
		conn, err = srtgo.Dial(h.addr, cfg)
		if err == nil {
			break
		}
		time.Sleep(time.Duration(10*(i+1)) * time.Millisecond)
	}
	if err != nil {
		h.t.Fatalf("dial SRT failed after retries: %v", err)
	}

	data, err := os.ReadFile(tsPath)
	if err != nil {
		h.t.Fatalf("read TS file: %v", err)
	}

	for i := 0; i < len(data); i += 1316 {
		select {
		case <-h.ctx.Done():
			return conn
		default:
		}
		end := i + 1316
		if end > len(data) {
			end = len(data)
		}
		_, werr := conn.Write(data[i:end])
		if werr != nil {
			h.t.Logf("write error during push: %v", werr)
			return conn
		}
		time.Sleep(time.Millisecond) // rough pacing
	}

	return conn
}

// findFreeUDPAddr finds an available UDP port on localhost and returns "127.0.0.1:<port>".
func findFreeUDPAddr(t *testing.T) string {
	t.Helper()
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.LocalAddr().String()
	_ = l.Close()
	return addr
}

// generateTestTS creates a test MPEG-TS file with video and audio using ffmpeg.
// Returns the path to the generated file.
func generateTestTS(t *testing.T, durationSec float64, width, height, fps int) string {
	t.Helper()

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.ts")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", formatTestSrc(durationSec, width, height, fps),
		"-f", "lavfi",
		"-i", formatSineSrc(durationSec),
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-pix_fmt", "yuv420p",
		"-g", "30",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "mpegts",
		"-y",
		path,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg failed: %s\n%s", err, string(out))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("generated TS file is empty")
	}

	t.Logf("generated test TS: %s (%d bytes, %.1fs, %dx%d@%dfps)",
		path, info.Size(), durationSec, width, height, fps)

	return path
}

func formatTestSrc(durationSec float64, width, height, fps int) string {
	return "testsrc=duration=" + ftoa(durationSec) +
		":size=" + itoa(width) + "x" + itoa(height) +
		":rate=" + itoa(fps)
}

func formatSineSrc(durationSec float64) string {
	return "sine=frequency=1000:duration=" + ftoa(durationSec) + ":sample_rate=48000"
}

func ftoa(f float64) string {
	if f == float64(int(f)) {
		return itoa(int(f))
	}
	// Simple float formatting for common test values
	whole := int(f)
	frac := int((f - float64(whole)) * 10)
	return itoa(whole) + "." + itoa(frac)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 8)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
