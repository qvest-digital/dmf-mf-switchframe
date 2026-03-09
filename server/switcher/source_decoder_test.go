package switcher

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

func TestSourceDecoderCreation(t *testing.T) {
	factory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	}

	var called atomic.Int32
	callback := func(sourceKey string, pf *ProcessingFrame) {
		called.Add(1)
	}

	sd := newSourceDecoder("cam1", factory, callback, nil)
	if sd == nil {
		t.Fatal("newSourceDecoder returned nil")
	}
	if sd.sourceKey != "cam1" {
		t.Errorf("sourceKey = %q, want %q", sd.sourceKey, "cam1")
	}

	sd.Close()
}

func TestSourceDecoderFactoryError(t *testing.T) {
	factory := func() (transition.VideoDecoder, error) {
		return nil, fmt.Errorf("no codec")
	}

	sd := newSourceDecoder("cam1", factory, func(string, *ProcessingFrame) {}, nil)
	if sd != nil {
		sd.Close()
		t.Fatal("expected nil sourceDecoder when factory fails")
	}
}

func TestSourceDecoderSendAndCallback(t *testing.T) {
	factory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	}

	var mu sync.Mutex
	var received []*ProcessingFrame
	var receivedKeys []string
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		received = append(received, pf)
		receivedKeys = append(receivedKeys, sourceKey)
		mu.Unlock()
	}

	sd := newSourceDecoder("cam1", factory, callback, nil)
	defer sd.Close()

	// Send a keyframe (needed to init mock decoder)
	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:        []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:        []byte{0x68, 0xce, 0x38, 0x80},
		Codec:      "h264",
		GroupID:    1,
	}
	sd.Send(frame)

	// Wait for decode goroutine to process
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for callback")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if receivedKeys[0] != "cam1" {
		t.Errorf("callback sourceKey = %q, want %q", receivedKeys[0], "cam1")
	}
	pf := received[0]
	if pf.Width != 320 || pf.Height != 240 {
		t.Errorf("dimensions = %dx%d, want 320x240", pf.Width, pf.Height)
	}
	if pf.PTS != 90000 {
		t.Errorf("PTS = %d, want 90000", pf.PTS)
	}
	if pf.Codec != "h264" {
		t.Errorf("Codec = %q, want %q", pf.Codec, "h264")
	}
}

func TestSourceDecoderNewestWinsDrop(t *testing.T) {
	// Use a blocking decoder to fill the channel
	decCh := make(chan struct{})
	factory := func() (transition.VideoDecoder, error) {
		return &blockingMockDecoder{
			width: 320, height: 240,
			blockCh: decCh,
		}, nil
	}

	var mu sync.Mutex
	var ptsList []int64
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		ptsList = append(ptsList, pf.PTS)
		mu.Unlock()
	}

	sd := newSourceDecoder("cam1", factory, callback, nil)
	defer sd.Close()

	// Send 5 frames rapidly — channel capacity is 2, so oldest should be dropped
	for i := 0; i < 5; i++ {
		sd.Send(&media.VideoFrame{
			PTS:        int64((i + 1) * 90000),
			IsKeyframe: true,
			WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65},
			SPS:        []byte{0x67},
			PPS:        []byte{0x68},
			Codec:      "h264",
			GroupID:    uint32(i + 1),
		})
		time.Sleep(1 * time.Millisecond) // let goroutine pick up
	}

	// Unblock decoder for all pending frames
	close(decCh)

	// Wait for at least one callback
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(ptsList)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for callbacks")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Give time for remaining frames
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	// Should have fewer than 5 frames due to drop policy
	if len(ptsList) >= 5 {
		t.Errorf("expected fewer than 5 decoded frames due to drop, got %d", len(ptsList))
	}
}

func TestSourceDecoderCloseStopsGoroutine(t *testing.T) {
	factory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	}

	sd := newSourceDecoder("cam1", factory, func(string, *ProcessingFrame) {}, nil)

	// Close should return without hanging
	done := make(chan struct{})
	go func() {
		sd.Close()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return in time")
	}
}

func TestSourceDecoderDecodeError(t *testing.T) {
	// failingMockDecoder fails first 2 calls, then succeeds.
	// The decode loop runs in a single goroutine, so calls is safe.
	fmd := &failingMockDecoder{failCount: 2}
	factory := func() (transition.VideoDecoder, error) {
		return fmd, nil
	}

	var mu sync.Mutex
	var received int
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		received++
		mu.Unlock()
	}

	sd := newSourceDecoder("cam1", factory, callback, nil)
	defer sd.Close()

	// Send 3 frames sequentially — first 2 should fail, third should succeed.
	// Give time between sends so the decode goroutine processes each one.
	for i := 0; i < 3; i++ {
		sd.Send(&media.VideoFrame{
			PTS:        int64(i * 90000),
			IsKeyframe: true,
			WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65},
			SPS:        []byte{0x67},
			PPS:        []byte{0x68},
			Codec:      "h264",
			GroupID:    uint32(i),
		})
		time.Sleep(10 * time.Millisecond) // let decode goroutine process
	}

	// Wait for processing
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := received
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for successful decode")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestSourceDecoderStats(t *testing.T) {
	factory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	}

	sd := newSourceDecoder("cam1", factory, func(string, *ProcessingFrame) {}, nil)
	defer sd.Close()

	// Send a few frames to build stats
	for i := 0; i < 5; i++ {
		sd.Send(&media.VideoFrame{
			PTS:        int64(i * 3000), // ~30fps at 90kHz
			IsKeyframe: i == 0,
			WireData:   make([]byte, 10000), // 10KB per frame
			SPS:        []byte{0x67},
			PPS:        []byte{0x68},
			Codec:      "h264",
			GroupID:    uint32(i / 2),
		})
	}

	time.Sleep(100 * time.Millisecond)

	avgSize, avgFPS := sd.Stats()
	if avgSize <= 0 {
		t.Errorf("avgFrameSize should be positive, got %f", avgSize)
	}
	// FPS may or may not be computed depending on timing
	_ = avgFPS
}

func TestSourceDecoderBufferReuse(t *testing.T) {
	// Verify the decoder reuses its annexB/prepend buffers across frames
	// by checking that multiple frames produce correct output without
	// inter-frame corruption.
	factory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	}

	var mu sync.Mutex
	var received []*ProcessingFrame
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		received = append(received, pf)
		mu.Unlock()
	}

	sd := newSourceDecoder("cam1", factory, callback, nil)
	defer sd.Close()

	// Send 3 keyframes — each reuses the annexB/prepend buffers.
	// If buffer reuse corrupts data, the decoder will fail or produce
	// wrong dimensions.
	for i := 0; i < 3; i++ {
		sd.Send(&media.VideoFrame{
			PTS:        int64((i + 1) * 90000),
			DTS:        int64((i + 1) * 90000),
			IsKeyframe: true,
			WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
			SPS:        []byte{0x67, 0x42, 0x00, 0x1e},
			PPS:        []byte{0x68, 0xce, 0x38, 0x80},
			Codec:      "h264",
			GroupID:    uint32(i + 1),
		})
		time.Sleep(20 * time.Millisecond)
	}

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 3 {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			t.Fatalf("timeout: got %d frames, want 3", len(received))
			mu.Unlock()
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for i, pf := range received {
		if pf.Width != 320 || pf.Height != 240 {
			t.Errorf("frame %d: dimensions = %dx%d, want 320x240", i, pf.Width, pf.Height)
		}
		if pf.PTS != int64((i+1)*90000) {
			t.Errorf("frame %d: PTS = %d, want %d", i, pf.PTS, int64((i+1)*90000))
		}
	}
}

// --- Test helpers ---

// blockingMockDecoder blocks until blockCh is closed, then returns success.
type blockingMockDecoder struct {
	width, height int
	blockCh       chan struct{}
}

func (d *blockingMockDecoder) Decode(data []byte) ([]byte, int, int, error) {
	<-d.blockCh
	return make([]byte, d.width*d.height*3/2), d.width, d.height, nil
}

func (d *blockingMockDecoder) Close() {}

// failingMockDecoder fails the first failCount calls, then succeeds.
type failingMockDecoder struct {
	failCount int
	calls     int
}

func (d *failingMockDecoder) Decode(data []byte) ([]byte, int, int, error) {
	d.calls++
	if d.calls <= d.failCount {
		return nil, 0, 0, fmt.Errorf("decode error")
	}
	return make([]byte, 320*240*3/2), 320, 240, nil
}

func (d *failingMockDecoder) Close() {}
