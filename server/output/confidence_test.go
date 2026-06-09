package output

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

func TestConfidenceMonitor_ProducesThumbnail(t *testing.T) {
	// Use a mock decoder that returns known YUV data
	decoderFactory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 180), nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	defer cm.Close()

	// Feed a mock keyframe
	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}, // fake AVC data
	}

	cm.IngestVideo(frame)
	cm.inFlight.Wait() // join the background decode/encode worker

	// Should have a JPEG thumbnail available
	jpg := cm.LatestThumbnail()
	require.NotNil(t, jpg, "should produce thumbnail from keyframe")
	require.True(t, len(jpg) > 0)
	// Verify JPEG magic bytes (SOI marker)
	require.Equal(t, byte(0xFF), jpg[0])
	require.Equal(t, byte(0xD8), jpg[1])
}

func TestConfidenceMonitor_SkipsNonKeyframes(t *testing.T) {
	decoderFactory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 180), nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	defer cm.Close()

	// Feed a non-keyframe
	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: false,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x88},
	}

	cm.IngestVideo(frame)

	// Should NOT have a thumbnail
	require.Nil(t, cm.LatestThumbnail(), "should not produce thumbnail from non-keyframe")
}

func TestConfidenceMonitor_RateLimitsTo1fps(t *testing.T) {
	decodeCount := 0
	decoderFactory := func() (transition.VideoDecoder, error) {
		return &countingDecoder{count: &decodeCount}, nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	cm.minInterval = 100 * time.Millisecond // speed up for test
	defer cm.Close()

	keyframe := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	}

	// First frame should be processed
	cm.IngestVideo(keyframe)
	cm.inFlight.Wait() // join the background decode/encode worker
	require.Equal(t, 1, decodeCount)

	// Immediate second frame should be rate-limited (skipped)
	cm.IngestVideo(keyframe)
	cm.inFlight.Wait()
	require.Equal(t, 1, decodeCount, "should rate-limit and skip second frame")

	// Wait past the interval
	time.Sleep(150 * time.Millisecond)

	// Third frame should be processed
	cm.IngestVideo(keyframe)
	cm.inFlight.Wait()
	require.Equal(t, 2, decodeCount, "should process frame after interval")
}

func TestConfidenceMonitor_NilDecoderFactory(t *testing.T) {
	cm := NewConfidenceMonitor(nil)
	defer cm.Close()

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	}

	// Should not panic
	cm.IngestVideo(frame)
	require.Nil(t, cm.LatestThumbnail())
}

func TestConfidenceMonitor_ScalesToThumbnailSize(t *testing.T) {
	// Use a larger-than-thumbnail decoder
	decoderFactory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(1920, 1080), nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	defer cm.Close()

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	}

	cm.IngestVideo(frame)
	cm.inFlight.Wait() // join the background decode/encode worker

	jpg := cm.LatestThumbnail()
	require.NotNil(t, jpg, "should produce thumbnail from large frame")
	// Verify JPEG magic bytes
	require.Equal(t, byte(0xFF), jpg[0])
	require.Equal(t, byte(0xD8), jpg[1])
}

func TestConfidenceMonitor_ConvertsAVC1ToAnnexB(t *testing.T) {
	var receivedData []byte
	decoderFactory := func() (transition.VideoDecoder, error) {
		return &recordingDecoder{received: &receivedData}, nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	defer cm.Close()

	// Feed AVC1-format wire data (4-byte length prefix)
	avc1Data := []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88}
	sps := []byte{0x67, 0x64, 0x00, 0x28}
	pps := []byte{0x68, 0xEE, 0x3C, 0x80}

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   avc1Data,
		SPS:        sps,
		PPS:        pps,
	}

	cm.IngestVideo(frame)
	cm.inFlight.Wait() // join the background decode/encode worker

	require.NotNil(t, receivedData)
	// Should start with Annex B start code (0x00 0x00 0x00 0x01), not AVC1 length prefix
	require.True(t, len(receivedData) >= 4)
	require.Equal(t, byte(0x00), receivedData[0])
	require.Equal(t, byte(0x00), receivedData[1])
	require.Equal(t, byte(0x00), receivedData[2])
	require.Equal(t, byte(0x01), receivedData[3])
}

// recordingDecoder captures the data passed to Decode for assertion.
type recordingDecoder struct {
	received *[]byte
}

func (d *recordingDecoder) Decode(data []byte) ([]byte, int, int, error) {
	cp := make([]byte, len(data))
	copy(cp, data)
	*d.received = cp
	w, h := 320, 180
	return make([]byte, video.YUV420FrameSize(w, h)), w, h, nil
}

func (d *recordingDecoder) Close() {}

// countingDecoder tracks decode calls and returns a fixed-size YUV buffer.
type countingDecoder struct {
	count *int
}

func (d *countingDecoder) Decode(data []byte) ([]byte, int, int, error) {
	*d.count++
	w, h := 320, 180
	return make([]byte, video.YUV420FrameSize(w, h)), w, h, nil
}

func (d *countingDecoder) Close() {}

// slowDecoder simulates a decoder that takes time to process, exposing the
// window where Close() could race with an in-flight Decode.
type slowDecoder struct {
	delay time.Duration
}

func (d *slowDecoder) Decode(data []byte) ([]byte, int, int, error) {
	time.Sleep(d.delay)
	w, h := 320, 180
	return make([]byte, video.YUV420FrameSize(w, h)), w, h, nil
}

func (d *slowDecoder) Close() {}

func TestConfidenceMonitorConcurrentClose(t *testing.T) {
	decoderFactory := func() (transition.VideoDecoder, error) {
		return &slowDecoder{delay: 50 * time.Millisecond}, nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	cm.minInterval = 0 // allow every frame through for stress

	keyframe := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	}

	// Launch several IngestVideo goroutines concurrently with Close.
	var wg sync.WaitGroup
	const goroutines = 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cm.IngestVideo(keyframe)
		}()
	}

	// Give goroutines a moment to start and acquire the decoder.
	time.Sleep(10 * time.Millisecond)

	// Close concurrently — must not race with in-flight Decode calls.
	wg.Add(1)
	go func() {
		defer wg.Done()
		cm.Close()
	}()

	wg.Wait()
}

func TestConfidenceMonitor_IngestVideoCloseRace(t *testing.T) {
	// This test specifically targets the race where IngestVideo checks
	// decoderFactory outside the lock while Close sets it to nil under
	// the lock. With the fix, both accesses are under the lock.
	// Run with -race to verify.
	for i := 0; i < 50; i++ {
		decoderFactory := func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(320, 180), nil
		}

		cm := NewConfidenceMonitor(decoderFactory)
		cm.minInterval = 0

		keyframe := &media.VideoFrame{
			PTS:        1000,
			IsKeyframe: true,
			WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
		}

		var wg sync.WaitGroup

		// Start IngestVideo and Close simultaneously to maximize
		// the chance of hitting the race window.
		wg.Add(2)
		go func() {
			defer wg.Done()
			cm.IngestVideo(keyframe)
		}()
		go func() {
			defer wg.Done()
			cm.Close()
		}()

		wg.Wait()
	}
}

func TestConfidenceMonitor_IngestAfterClose(t *testing.T) {
	decoderFactory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 180), nil
	}

	cm := NewConfidenceMonitor(decoderFactory)
	cm.Close()

	// IngestVideo after Close should be a no-op (not panic).
	keyframe := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	}
	cm.IngestVideo(keyframe)
	require.Nil(t, cm.LatestThumbnail())
}

// concurrencyDecoder records the maximum number of Decode calls that are ever
// in flight simultaneously. Native (FFmpeg/HW) decoders are not safe for
// concurrent Decode on one instance, so the monitor must serialize them.
type concurrencyDecoder struct {
	delay   time.Duration
	cur     atomic.Int32
	maxSeen atomic.Int32
}

func (d *concurrencyDecoder) Decode(data []byte) ([]byte, int, int, error) {
	n := d.cur.Add(1)
	for {
		m := d.maxSeen.Load()
		if n <= m || d.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(d.delay)
	d.cur.Add(-1)
	w, h := 320, 180
	return make([]byte, video.YUV420FrameSize(w, h)), w, h, nil
}

func (d *concurrencyDecoder) Close() {}

// TestConfidenceMonitor_DecodeNotConcurrent verifies that a decode which
// outlasts minInterval cannot be entered a second time concurrently. Without
// the in-flight gate, a slow decode would let a later IngestVideo pass the
// time.Since(lastUpdate) < minInterval check (lastUpdate is stamped at the
// START of decode) and call Decode again on the shared decoder.
func TestConfidenceMonitor_DecodeNotConcurrent(t *testing.T) {
	dec := &concurrencyDecoder{delay: 60 * time.Millisecond}
	cm := NewConfidenceMonitor(func() (transition.VideoDecoder, error) {
		return dec, nil
	})
	// minInterval shorter than the decode delay: rate limiting alone would NOT
	// prevent overlap, so only the in-flight gate can keep Decode serialized.
	cm.minInterval = 10 * time.Millisecond

	keyframe := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Stagger so later calls land while the first decode is still
			// running (delay 60ms >> minInterval 10ms).
			cm.IngestVideo(keyframe)
		}()
		time.Sleep(15 * time.Millisecond)
	}
	wg.Wait()
	cm.inFlight.Wait() // join any still-running background decode

	require.Equal(t, int32(1), dec.maxSeen.Load(),
		"Decode must never run concurrently on the shared decoder")
}

// blockingDecoder blocks inside Decode until released, exposing whether the
// caller of IngestVideo is blocked for the duration of the decode.
type blockingDecoder struct {
	started chan struct{}
	release chan struct{}
}

func (d *blockingDecoder) Decode(data []byte) ([]byte, int, int, error) {
	close(d.started)
	<-d.release
	w, h := 320, 180
	return make([]byte, video.YUV420FrameSize(w, h)), w, h, nil
}

func (d *blockingDecoder) Close() {}

// TestConfidenceMonitor_IngestVideoDoesNotBlockOnDecode verifies that the
// expensive decode → scale → JPEG encode pipeline runs on a background
// goroutine. IngestVideo is invoked synchronously from the program video
// broadcast path (Manager.DirectWriteVideo ← switcher sync loop), so a
// 10-30ms 1080p keyframe decode + JPEG encode inline would exceed the
// 16.7-33ms frame budget and trip the sync loop's overload logic.
func TestConfidenceMonitor_IngestVideoDoesNotBlockOnDecode(t *testing.T) {
	dec := &blockingDecoder{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	cm := NewConfidenceMonitor(func() (transition.VideoDecoder, error) {
		return dec, nil
	})
	defer cm.Close()

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	}

	returned := make(chan struct{})
	go func() {
		cm.IngestVideo(frame)
		close(returned)
	}()

	// Wait until the decoder is blocked mid-Decode.
	select {
	case <-dec.started:
	case <-time.After(2 * time.Second):
		t.Fatal("Decode was never invoked")
	}

	// IngestVideo must have returned (or return promptly) while the decode
	// is still in flight.
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		close(dec.release)
		t.Fatal("IngestVideo blocked the broadcast path for the duration of the decode")
	}
	close(dec.release)

	// The thumbnail must still be produced once the decode completes.
	require.Eventually(t, func() bool {
		return cm.LatestThumbnail() != nil
	}, 2*time.Second, 10*time.Millisecond,
		"thumbnail should be produced asynchronously")
}
