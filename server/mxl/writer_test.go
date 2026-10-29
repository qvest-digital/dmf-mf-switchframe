package mxl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/internal/video"
)

// --- Mock discrete writer (video) ---

type mockDiscreteWriter struct {
	mu     sync.Mutex
	grains []writtenGrain
	closed bool
}

type writtenGrain struct {
	index uint64
	data  []byte
}

func (m *mockDiscreteWriter) WriteGrain(index uint64, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.grains = append(m.grains, writtenGrain{index: index, data: cp})
	return nil
}

func (m *mockDiscreteWriter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockDiscreteWriter) getGrains() []writtenGrain {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]writtenGrain, len(m.grains))
	copy(cp, m.grains)
	return cp
}

// --- Mock continuous writer (audio) ---

type mockContinuousWriter struct {
	mu      sync.Mutex
	samples []writtenSamples
	closed  bool
}

type writtenSamples struct {
	index    uint64
	channels [][]float32
}

func (m *mockContinuousWriter) WriteSamples(index uint64, channels [][]float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([][]float32, len(channels))
	for i, ch := range channels {
		cp[i] = make([]float32, len(ch))
		copy(cp[i], ch)
	}
	m.samples = append(m.samples, writtenSamples{index: index, channels: cp})
	return nil
}

func (m *mockContinuousWriter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockContinuousWriter) getSamples() []writtenSamples {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]writtenSamples, len(m.samples))
	copy(cp, m.samples)
	return cp
}

// --- Tests ---

func TestWriter_WritesVideoGrain(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	w.SetVideoWriter(mock, Rational{30, 1})

	// Start the ticker so grains actually get written.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Create a minimal YUV420p frame (12x2 = 12*2 + 6*1 + 6*1 = 36 bytes).
	width, height := 12, 2
	yuvSize := width*height + width/2*height/2 + width/2*height/2
	yuv := make([]byte, yuvSize)
	// Set Y=16 (limited range black), Cb=Cr=128.
	for i := 0; i < width*height; i++ {
		yuv[i] = 16
	}
	for i := width * height; i < yuvSize; i++ {
		yuv[i] = 128
	}

	w.WriteVideo(yuv, width, height, 0)

	// Wait for the ticker to fire and write the grain.
	deadline := time.After(200 * time.Millisecond)
	for {
		grains := mock.getGrains()
		if len(grains) >= 1 {
			if len(grains[0].data) == 0 {
				t.Fatal("expected non-empty V210 data")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for grain write, got %d grains", len(mock.getGrains()))
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestWriter_WritesAudioSamples(t *testing.T) {
	mock := &mockContinuousWriter{}
	w := NewWriter(WriterConfig{SampleRate: 48000, Channels: 2})
	w.SetAudioWriter(mock, Rational{48000, 1})

	// Interleaved stereo PCM: L0, R0, L1, R1.
	pcm := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6}
	w.WriteAudio(pcm, 0, 48000, 2)

	samples := mock.getSamples()
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample batch, got %d", len(samples))
	}

	// Should be de-interleaved: 2 channels, 3 samples each.
	if len(samples[0].channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(samples[0].channels))
	}
	if len(samples[0].channels[0]) != 3 {
		t.Fatalf("expected 3 samples per channel, got %d", len(samples[0].channels[0]))
	}

	// Verify de-interleaving.
	wantL := []float32{0.1, 0.3, 0.5}
	wantR := []float32{0.2, 0.4, 0.6}
	for i := range wantL {
		if samples[0].channels[0][i] != wantL[i] {
			t.Fatalf("L[%d] = %f, want %f", i, samples[0].channels[0][i], wantL[i])
		}
		if samples[0].channels[1][i] != wantR[i] {
			t.Fatalf("R[%d] = %f, want %f", i, samples[0].channels[1][i], wantR[i])
		}
	}
}

func TestWriter_StopsOnContextCancel(t *testing.T) {
	vMock := &mockDiscreteWriter{}
	aMock := &mockContinuousWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2, SampleRate: 48000, Channels: 2})
	w.SetVideoWriter(vMock, Rational{30, 1})
	w.SetAudioWriter(aMock, Rational{48000, 1})

	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)

	// Write a video frame and wait for the ticker to pick it up.
	yuv := make([]byte, video.YUV420FrameSize(12, 2))
	w.WriteVideo(yuv, 12, 2, 0)

	deadline := time.After(200 * time.Millisecond)
	for len(vMock.getGrains()) < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for grain before cancel")
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	time.Sleep(50 * time.Millisecond) // Allow goroutine to close.

	countBefore := len(vMock.getGrains())

	// Writes after close should be silently dropped (no new grains from ticker).
	w.WriteVideo(yuv, 12, 2, 1)
	time.Sleep(100 * time.Millisecond)

	if len(vMock.getGrains()) != countBefore {
		t.Fatal("expected no new grains after close")
	}
}

func TestWriter_ClosesFlows(t *testing.T) {
	vMock := &mockDiscreteWriter{}
	aMock := &mockContinuousWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2, SampleRate: 48000, Channels: 2})
	w.SetVideoWriter(vMock, Rational{30, 1})
	w.SetAudioWriter(aMock, Rational{48000, 1})

	if err := w.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	if !vMock.closed {
		t.Fatal("expected video writer to be closed")
	}
	if !aMock.closed {
		t.Fatal("expected audio writer to be closed")
	}
}

func TestWriter_NilWritersNoOp(t *testing.T) {
	w := NewWriter(WriterConfig{})

	// Should not panic with nil writers.
	w.WriteVideo(make([]byte, 36), 12, 2, 0)
	w.WriteAudio([]float32{0.1, 0.2}, 0, 48000, 2)

	if err := w.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

func TestWriter_DeinterleavesMono(t *testing.T) {
	mock := &mockContinuousWriter{}
	w := NewWriter(WriterConfig{SampleRate: 48000, Channels: 1})
	w.SetAudioWriter(mock, Rational{48000, 1})

	pcm := []float32{0.1, 0.2, 0.3}
	w.WriteAudio(pcm, 0, 48000, 1)

	samples := mock.getSamples()
	if len(samples) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(samples))
	}
	if len(samples[0].channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(samples[0].channels))
	}
	want := []float32{0.1, 0.2, 0.3}
	for i, v := range want {
		if samples[0].channels[0][i] != v {
			t.Fatalf("[%d] = %f, want %f", i, samples[0].channels[0][i], v)
		}
	}
}

func TestWriter_SkipsMismatchedResolution(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 1920, Height: 1080})
	w.SetVideoWriter(mock, Rational{30, 1})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Write a frame at wrong resolution — should be silently skipped.
	yuv := make([]byte, video.YUV420FrameSize(640, 128))
	w.WriteVideo(yuv, 640, 128, 0)

	time.Sleep(100 * time.Millisecond)
	if len(mock.getGrains()) != 0 {
		t.Fatal("expected no grains for mismatched resolution")
	}
}

func TestWriterConcurrentWriteVideo(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	w.SetVideoWriter(mock, Rational{30, 1})

	var wg sync.WaitGroup
	wg.Add(2)

	width, height := 12, 2
	yuvSize := width*height + width/2*height/2 + width/2*height/2

	go func() {
		defer wg.Done()
		yuv := make([]byte, yuvSize)
		for i := 0; i < 100; i++ {
			w.WriteVideo(yuv, width, height, int64(i*3000))
		}
	}()

	go func() {
		defer wg.Done()
		time.Sleep(time.Millisecond)
		_ = w.Close()
	}()

	wg.Wait()
}

func TestWriterCloseFlagPreventsWrites(t *testing.T) {
	vMock := &mockDiscreteWriter{}
	aMock := &mockContinuousWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2, SampleRate: 48000, Channels: 2})
	w.SetVideoWriter(vMock, Rational{30, 1})
	w.SetAudioWriter(aMock, Rational{48000, 1})

	_ = w.Close()

	width, height := 12, 2
	yuvSize := width*height + width/2*height/2 + width/2*height/2
	yuv := make([]byte, yuvSize)
	w.WriteVideo(yuv, width, height, 0)
	w.WriteAudio([]float32{0.1, 0.2}, 0, 48000, 2)

	if len(vMock.getGrains()) != 0 {
		t.Fatal("expected no video grains after close")
	}
	if len(aMock.getSamples()) != 0 {
		t.Fatal("expected no audio samples after close")
	}
}

func TestWriter_CloseWaitsForGoroutines(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	w.SetVideoWriter(mock, Rational{30, 1})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Feed a frame so the ticker has something to write.
	width, height := 12, 2
	yuvSize := width*height + width/2*height/2 + width/2*height/2
	yuv := make([]byte, yuvSize)
	w.WriteVideo(yuv, width, height, 0)

	// Wait for at least one grain to confirm the goroutine is running.
	deadline := time.After(200 * time.Millisecond)
	for len(mock.getGrains()) < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for initial grain")
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Close should block until the videoTickLoop goroutine exits.
	// If it doesn't wait, the race detector would catch concurrent access.
	done := make(chan struct{})
	go func() {
		_ = w.Close()
		close(done)
	}()

	select {
	case <-done:
		// Close returned — goroutines have exited.
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not return — possible deadlock")
	}

	// Second Close should be a no-op and return immediately.
	if err := w.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

func TestWriter_ContextCancelWaitsForGoroutines(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	w.SetVideoWriter(mock, Rational{30, 1})

	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)

	// Feed a frame so the ticker is active.
	width, height := 12, 2
	yuvSize := width*height + width/2*height/2 + width/2*height/2
	yuv := make([]byte, yuvSize)
	w.WriteVideo(yuv, width, height, 0)

	deadline := time.After(200 * time.Millisecond)
	for len(mock.getGrains()) < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for initial grain")
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Cancel context — the ctx.Done goroutine calls Close(), which waits
	// for videoTickLoop. This must not deadlock. We call Close() ourselves
	// (racing with ctx.Done goroutine) to verify it returns; either caller
	// will complete the shutdown.
	cancel()

	done := make(chan struct{})
	go func() {
		_ = w.Close()
		close(done)
	}()

	select {
	case <-done:
		// Close returned — no deadlock
	case <-time.After(2 * time.Second):
		t.Fatal("context cancel path appears to have deadlocked")
	}

	// Writer should be closed now.
	if !w.closed.Load() {
		t.Fatal("expected writer to be closed after context cancel")
	}
}

func BenchmarkMXLWriterVideoHotPath(b *testing.B) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	w.SetVideoWriter(mock, Rational{30, 1})

	width, height := 12, 2
	yuvSize := width*height + width/2*height/2 + width/2*height/2
	yuv := make([]byte, yuvSize)
	for i := 0; i < width*height; i++ {
		yuv[i] = 16
	}
	for i := width * height; i < yuvSize; i++ {
		yuv[i] = 128
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.WriteVideo(yuv, width, height, int64(i))
	}
}

func BenchmarkMXLWriterAudioHotPath(b *testing.B) {
	mock := &mockContinuousWriter{}
	w := NewWriter(WriterConfig{SampleRate: 48000, Channels: 2})
	w.SetAudioWriter(mock, Rational{48000, 1})

	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = float32(i) * 0.001
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.WriteAudio(pcm, int64(i), 48000, 2)
	}
}

func TestWriter_SteadyRateMultipleFrames(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	// Use a fast rate (100fps = 10ms ticks) so the test runs quickly.
	w.SetVideoWriter(mock, Rational{100, 1})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Write a frame.
	width, height := 12, 2
	yuvSize := width*height + width/2*height/2 + width/2*height/2
	yuv := make([]byte, yuvSize)
	for i := 0; i < width*height; i++ {
		yuv[i] = 16
	}
	for i := width * height; i < yuvSize; i++ {
		yuv[i] = 128
	}
	w.WriteVideo(yuv, width, height, 0)

	// Wait for multiple ticks — the same frame should be written repeatedly.
	time.Sleep(55 * time.Millisecond) // ~5 ticks at 100fps
	grains := mock.getGrains()
	if len(grains) < 3 {
		t.Fatalf("expected at least 3 grains from steady ticker, got %d", len(grains))
	}
}

// corruptionDetectingWriter simulates a slow cgo WriteGrain that holds the
// buffer for a configurable duration. It snapshots the first bytes of data
// on entry, sleeps, then re-reads on exit and counts mismatches. Any
// producer-side mutation of the buffer while WriteGrain is running shows up
// as a non-zero corruptions count.
type corruptionDetectingWriter struct {
	sleep       time.Duration
	snapshotN   int
	corruptions atomic.Int64
	grains      atomic.Int64
	closed      atomic.Bool
}

func (m *corruptionDetectingWriter) WriteGrain(index uint64, data []byte) error {
	n := m.snapshotN
	if n <= 0 || n > len(data) {
		n = len(data)
	}
	entry := make([]byte, n)
	copy(entry, data[:n])
	time.Sleep(m.sleep)
	exit := make([]byte, n)
	copy(exit, data[:n])
	if !bytes.Equal(entry, exit) {
		m.corruptions.Add(1)
	}
	m.grains.Add(1)
	return nil
}

func (m *corruptionDetectingWriter) Close() error {
	m.closed.Store(true)
	return nil
}

// TestMXLWriter_TripleBufferOverrunCorrupts drives WriteVideo faster than the
// ticker can consume while WriteGrain is deliberately slow. The current
// 3-slot ring (without in-flight coordination) lets the producer wrap back
// into the slot the ticker is mid-cgo-memcpy over, tearing the V210 content.
// With the 4-slot-plus-inFlight fix, the producer must drop rather than
// overwrite, so every grain's entry snapshot must equal its exit snapshot.
func TestMXLWriter_TripleBufferOverrunCorrupts(t *testing.T) {
	const (
		width    = 48
		height   = 16
		tickFPS  = 50                    // 20 ms tick period
		grainDur = 20 * time.Millisecond // WriteGrain held for a full tick
		burstDur = 5 * time.Millisecond  // producer writes every 5 ms
		cycles   = 40                    // ~200 ms total
	)
	mock := &corruptionDetectingWriter{sleep: grainDur, snapshotN: 128}
	w := NewWriter(WriterConfig{Width: width, Height: height})
	w.SetVideoWriter(mock, Rational{tickFPS, 1})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	yuvSize := video.YUV420FrameSize(width, height)
	// Drive WriteVideo at burstDur intervals for `cycles` iterations. Each
	// iteration uses a distinct fill byte so the V210 output differs — any
	// mid-WriteGrain overwrite on the same slot becomes observable.
	for i := 0; i < cycles*4; i++ {
		yuv := make([]byte, yuvSize)
		fill := byte((i * 7) & 0xff)
		for j := range yuv {
			yuv[j] = fill
		}
		w.WriteVideo(yuv, width, height, int64(i)*3000)
		time.Sleep(burstDur)
	}

	// Let the ticker drain a final cycle so any lingering WriteGrain
	// can complete and report corruption.
	time.Sleep(grainDur * 2)

	if c := mock.corruptions.Load(); c > 0 {
		t.Fatalf("%d of %d grains observed buffer mutation during WriteGrain — ring slot was overwritten mid-cgo-copy", c, mock.grains.Load())
	}
	if mock.grains.Load() < 3 {
		t.Fatalf("expected ticker to have produced at least 3 grains in %d iterations, got %d", cycles*4, mock.grains.Load())
	}
	// Sanity: under a 20 ms hold and 5 ms producer cadence, the in-flight
	// gate should have dropped frames rather than overwriting a held slot.
	if w.VideoDrops() == 0 {
		t.Fatalf("expected VideoDrops() > 0 under sustained over-production (20 ms hold + 5 ms producer cadence); got 0")
	}
}

// frameYUV returns a 12x2 YUV420p frame whose luma is value, so the V210 it
// becomes identifies the frame.
func frameYUV(value byte) []byte {
	const width, height = 12, 2
	yuv := make([]byte, width*height+2*(width/2)*(height/2))
	for i := 0; i < width*height; i++ {
		yuv[i] = value
	}
	for i := width * height; i < len(yuv); i++ {
		yuv[i] = 128
	}
	return yuv
}

// A producer at the grain rate, its frames jittering around the instants
// the writer would sample on, must get exactly one grain per frame. A
// writer sampling the latest frame on a ticker of its own loses a frame
// whenever two arrive between two of its ticks and shows the one before
// twice in the next; that is how a 50p clip played at 50p reached the
// program flow with a seventh of its frames repeated.
func TestWriter_GrainRateProducerGetsOneGrainPerFrame(t *testing.T) {
	const frames = 100
	rate := Rational{50, 1}
	period := 20 * time.Millisecond

	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	w.SetVideoWriter(mock, rate)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the producer on the instant the writer starts, so a writer on
	// a ticker of its own samples right where the frames arrive.
	start := time.Now()
	w.Start(ctx)
	for k := 0; k < frames; k++ {
		jitter := time.Duration((k*7)%9-4) * time.Millisecond // -4..+4 ms
		time.Sleep(time.Until(start.Add(time.Duration(k)*period + jitter)))
		w.WriteVideo(frameYUV(byte(16+k%200)), 12, 2, 0)
	}
	time.Sleep(period / 2)
	cancel()
	_ = w.Close()

	// Each frame's grains, in index order, between the first frame and
	// the last.
	grains := mock.getGrains()
	var runs []int
	for i, g := range grains {
		if i > 0 && g.index != grains[i-1].index+1 {
			t.Fatalf("grain %d follows %d", g.index, grains[i-1].index)
		}
		if i > 0 && bytes.Equal(g.data, grains[i-1].data) {
			runs[len(runs)-1]++
			continue
		}
		runs = append(runs, 1)
	}
	// The last frame repeats until the writer stops; the first frame may
	// meet a grain that is already late.
	if len(runs) != frames {
		t.Fatalf("%d frames produced, %d reached the flow", frames, len(runs))
	}
	repeated := 0
	for _, n := range runs[1 : len(runs)-1] {
		repeated += n - 1
	}
	// A host scheduling hiccup can still cost a repeat or two.
	if repeated > 2 {
		t.Fatalf("%d grains repeat a frame; runs %v", repeated, runs)
	}
}

// With no new frame the last one is still written once per grain period,
// each as its grain starts once the stall is established, so a stalled
// program source keeps its flow fed and its head on the clock.
func TestWriter_StalledProducerRepeatsOnSchedule(t *testing.T) {
	rate := Rational{50, 1}
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	w.SetVideoWriter(mock, rate)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	w.WriteVideo(frameYUV(16), 12, 2, 0)

	time.Sleep(400 * time.Millisecond)
	// Sample the head well inside a grain period.
	time.Sleep(time.Duration(NsUntilIndex(CurrentIndex(rate)+1, rate)) + 5*time.Millisecond)
	end := CurrentIndex(rate)
	head := mock.getGrains()
	cancel()
	_ = w.Close()
	if got := head[len(head)-1].index; got != end {
		t.Fatalf("head %d mid-period of grain %d during a stall", got, end)
	}

	grains := mock.getGrains()
	if len(grains) < 18 {
		t.Fatalf("%d grains in 400ms at 50/1, want about 20", len(grains))
	}
	for i := 1; i < len(grains); i++ {
		if grains[i].index != grains[i-1].index+1 {
			t.Fatalf("grain %d follows %d", grains[i].index, grains[i-1].index)
		}
	}
	if last := grains[len(grains)-1].index; last+2 < end {
		t.Fatalf("last grain %d trails the clock at %d", last, end)
	}
}

type failingDiscreteWriter struct{ calls atomic.Int64 }

// WriteGrain takes the first grain and refuses every one after it.
func (f *failingDiscreteWriter) WriteGrain(uint64, []byte) error {
	if f.calls.Add(1) == 1 {
		return nil
	}
	return errors.New("write refused")
}
func (f *failingDiscreteWriter) Close() error { return nil }

// A writer whose every grain write fails is retried once per grain period,
// not in a loop: the frame stays unwritten, so the goroutine would
// otherwise find it new on every pass and retry at once.
func TestWriter_FailingWriteRetriesOncePerGrain(t *testing.T) {
	rate := Rational{50, 1}
	fw := &failingDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	w.SetVideoWriter(fw, rate)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	w.WriteVideo(frameYUV(16), 12, 2, 0)
	time.Sleep(50 * time.Millisecond)
	w.WriteVideo(frameYUV(17), 12, 2, 0) // new, and refused from here on
	time.Sleep(300 * time.Millisecond)
	cancel()
	_ = w.Close()

	// 350 ms is 17 grain periods at 50/1.
	if n := fw.calls.Load(); n > 25 {
		t.Fatalf("%d write attempts in 350ms at 50/1", n)
	}
}
