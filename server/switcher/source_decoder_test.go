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

	sd := newSourceDecoder("cam1", factory, callback, nil, nil)
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

	sd := newSourceDecoder("cam1", factory, func(string, *ProcessingFrame) {}, nil, nil)
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

	sd := newSourceDecoder("cam1", factory, callback, nil, nil)
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
	sd.Send(frame, 0)

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

	sd := newSourceDecoder("cam1", factory, callback, nil, nil)
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
		}, 0)
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

	sd := newSourceDecoder("cam1", factory, func(string, *ProcessingFrame) {}, nil, nil)

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

	sd := newSourceDecoder("cam1", factory, callback, nil, nil)
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
		}, 0)
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

	sd := newSourceDecoder("cam1", factory, func(string, *ProcessingFrame) {}, nil, nil)
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
		}, 0)
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

	sd := newSourceDecoder("cam1", factory, callback, nil, nil)
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
		}, 0)
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

func TestSourceDecoder_DecodeTimingRecorded(t *testing.T) {
	// Verify that decode timing is recorded after processing a frame.
	factory := func() (transition.VideoDecoder, error) {
		return &slowDecoder{delay: 1 * time.Millisecond}, nil
	}

	var mu sync.Mutex
	var received []*ProcessingFrame
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		received = append(received, pf)
		mu.Unlock()
	}

	sd := newSourceDecoder("cam1", factory, callback, nil, nil)
	defer sd.Close()

	sd.Send(&media.VideoFrame{
		PTS:        90000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:        []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:        []byte{0x68, 0xce, 0x38, 0x80},
		Codec:      "h264",
		GroupID:    1,
	}, time.Now().UnixNano())

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

	lastDec, maxDec, drops := sd.PerfStats()
	if lastDec <= 0 {
		t.Errorf("lastDecodeNs should be > 0 after decode, got %d", lastDec)
	}
	if maxDec <= 0 {
		t.Errorf("maxDecodeNs should be > 0, got %d", maxDec)
	}
	if drops != 0 {
		t.Errorf("drops should be 0, got %d", drops)
	}
	// Decode with 1ms sleep should take at least 1ms
	if lastDec < int64(500*time.Microsecond) {
		t.Errorf("lastDecodeNs = %d, expected at least 500us for 1ms sleep decoder", lastDec)
	}
}

func TestSourceDecoder_DropCounting(t *testing.T) {
	// Create a decoder that blocks so we can fill the channel and trigger drops.
	blockCh := make(chan struct{})
	factory := func() (transition.VideoDecoder, error) {
		return &blockingMockDecoder{
			width: 4, height: 4,
			blockCh: blockCh,
		}, nil
	}

	callback := func(sourceKey string, pf *ProcessingFrame) {}

	sd := newSourceDecoder("cam1", factory, callback, nil, nil)
	defer sd.Close()

	// Send one frame to get the decoder loop blocked
	sd.Send(&media.VideoFrame{
		PTS:        90000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65},
		SPS:        []byte{0x67},
		PPS:        []byte{0x68},
		Codec:      "h264",
	}, 0)
	time.Sleep(5 * time.Millisecond) // let decode loop start blocking

	// Channel capacity is 2 — fill it
	for i := 0; i < 2; i++ {
		sd.Send(&media.VideoFrame{
			PTS:        int64((i + 2) * 90000),
			IsKeyframe: true,
			WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65},
			SPS:        []byte{0x67},
			PPS:        []byte{0x68},
			Codec:      "h264",
		}, 0)
	}
	time.Sleep(5 * time.Millisecond) // let channel fill

	// Next send should trigger a drop (channel is full, decoder is blocked)
	sd.Send(&media.VideoFrame{
		PTS:        5 * 90000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65},
		SPS:        []byte{0x67},
		PPS:        []byte{0x68},
		Codec:      "h264",
	}, 0)

	_, _, drops := sd.PerfStats()
	if drops < 1 {
		t.Errorf("expected at least 1 drop, got %d", drops)
	}

	// Unblock decoder to allow clean shutdown
	close(blockCh)
}

// slowDecoder introduces a configurable delay on Decode() for timing tests.
type slowDecoder struct {
	delay time.Duration
}

func (d *slowDecoder) Decode(data []byte) ([]byte, int, int, error) {
	if d.delay > 0 {
		time.Sleep(d.delay)
	}
	w, h := 4, 4
	yuv := make([]byte, w*h*3/2)
	return yuv, w, h, nil
}

func (d *slowDecoder) Close() {}

func TestSourceDecoder_TimestampsStamped(t *testing.T) {
	// Verify that decodeLoop stamps DecodeStartNano, DecodeEndNano, and
	// propagates ArrivalNano into the ProcessingFrame.
	factory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	}

	var mu sync.Mutex
	var captured *ProcessingFrame
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		if captured == nil {
			captured = pf
		}
		mu.Unlock()
	}

	sd := newSourceDecoder("cam1", factory, callback, nil, nil)
	defer sd.Close()

	arrivalNano := time.Now().UnixNano()

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
	sd.Send(frame, arrivalNano)

	// Wait for callback
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		got := captured
		mu.Unlock()
		if got != nil {
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

	// ArrivalNano must match what we passed in
	if captured.ArrivalNano != arrivalNano {
		t.Errorf("ArrivalNano = %d, want %d", captured.ArrivalNano, arrivalNano)
	}

	// DecodeStartNano must be stamped (> 0) and >= arrivalNano
	if captured.DecodeStartNano <= 0 {
		t.Errorf("DecodeStartNano should be > 0, got %d", captured.DecodeStartNano)
	}
	if captured.DecodeStartNano < arrivalNano {
		t.Errorf("DecodeStartNano (%d) should be >= arrivalNano (%d)", captured.DecodeStartNano, arrivalNano)
	}

	// DecodeEndNano must be stamped (> 0) and >= DecodeStartNano
	if captured.DecodeEndNano <= 0 {
		t.Errorf("DecodeEndNano should be > 0, got %d", captured.DecodeEndNano)
	}
	if captured.DecodeEndNano < captured.DecodeStartNano {
		t.Errorf("DecodeEndNano (%d) should be >= DecodeStartNano (%d)", captured.DecodeEndNano, captured.DecodeStartNano)
	}
}

func TestSourceDecoderPipelineFormat(t *testing.T) {
	// Verify that newSourceDecoder stores the pipelineFormat pointer and that
	// the decoder can load the current format atomically.
	factory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	}

	callback := func(string, *ProcessingFrame) {}

	var pf atomic.Pointer[PipelineFormat]
	format := &PipelineFormat{Width: 1920, Height: 1080, FPSNum: 30000, FPSDen: 1001, Name: "1080p29.97"}
	pf.Store(format)

	sd := newSourceDecoder("cam1", factory, callback, nil, &pf)
	if sd == nil {
		t.Fatal("newSourceDecoder returned nil")
	}
	defer sd.Close()

	// sourceDecoder should be able to read the pipeline format
	if sd.pipelineFormat == nil {
		t.Fatal("pipelineFormat pointer is nil")
	}
	loaded := sd.pipelineFormat.Load()
	if loaded == nil {
		t.Fatal("pipelineFormat.Load() returned nil")
	}
	if loaded.Width != 1920 || loaded.Height != 1080 {
		t.Errorf("pipelineFormat dimensions = %dx%d, want 1920x1080", loaded.Width, loaded.Height)
	}
	if loaded.Name != "1080p29.97" {
		t.Errorf("pipelineFormat name = %q, want %q", loaded.Name, "1080p29.97")
	}

	// Verify that updating the atomic pointer is visible to the decoder
	newFormat := &PipelineFormat{Width: 1280, Height: 720, FPSNum: 60, FPSDen: 1, Name: "720p60"}
	pf.Store(newFormat)
	loaded2 := sd.pipelineFormat.Load()
	if loaded2.Width != 1280 || loaded2.Height != 720 {
		t.Errorf("updated pipelineFormat dimensions = %dx%d, want 1280x720", loaded2.Width, loaded2.Height)
	}
}

func TestSourceDecoderPipelineFormatScaleBuf(t *testing.T) {
	// Verify that scaleBuf field is initialized as nil (lazy allocation)
	factory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	}

	var pf atomic.Pointer[PipelineFormat]
	format := &PipelineFormat{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, Name: "1080p30"}
	pf.Store(format)

	sd := newSourceDecoder("cam1", factory, func(string, *ProcessingFrame) {}, nil, &pf)
	if sd == nil {
		t.Fatal("newSourceDecoder returned nil")
	}
	defer sd.Close()

	if sd.scaleBuf != nil {
		t.Error("scaleBuf should be nil initially (lazy allocation)")
	}
}

func TestSourceDecoder_ScalesToPipelineFormat(t *testing.T) {
	// Decoder produces 320x240, but pipeline format is 640x480.
	// The callback should receive a frame scaled to 640x480.
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

	var pf atomic.Pointer[PipelineFormat]
	format := &PipelineFormat{Width: 640, Height: 480, FPSNum: 30, FPSDen: 1, Name: "480p30"}
	pf.Store(format)

	sd := newSourceDecoder("cam1", factory, callback, nil, &pf)
	if sd == nil {
		t.Fatal("newSourceDecoder returned nil")
	}
	defer sd.Close()

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
	sd.Send(frame, 0)

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
	got := received[0]
	if got.Width != 640 || got.Height != 480 {
		t.Errorf("dimensions = %dx%d, want 640x480", got.Width, got.Height)
	}
	expectedSize := 640 * 480 * 3 / 2
	if len(got.YUV) != expectedSize {
		t.Errorf("YUV size = %d, want %d", len(got.YUV), expectedSize)
	}
}

func TestSourceDecoder_SkipsScaleWhenResolutionMatches(t *testing.T) {
	// Decoder produces 320x240, pipeline format is also 320x240.
	// No scaling should occur — frame passes through at 320x240.
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

	var pf atomic.Pointer[PipelineFormat]
	format := &PipelineFormat{Width: 320, Height: 240, FPSNum: 30, FPSDen: 1, Name: "240p30"}
	pf.Store(format)

	sd := newSourceDecoder("cam1", factory, callback, nil, &pf)
	if sd == nil {
		t.Fatal("newSourceDecoder returned nil")
	}
	defer sd.Close()

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
	sd.Send(frame, 0)

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
	got := received[0]
	if got.Width != 320 || got.Height != 240 {
		t.Errorf("dimensions = %dx%d, want 320x240", got.Width, got.Height)
	}
	expectedSize := 320 * 240 * 3 / 2
	if len(got.YUV) != expectedSize {
		t.Errorf("YUV size = %d, want %d", len(got.YUV), expectedSize)
	}
}

func TestSourceDecoder_PipelineFormatChangeUpdatesScaling(t *testing.T) {
	// Integration test: verify that when the pipeline format changes at runtime
	// (via atomic store), the decoder picks up the new format on the very next
	// frame and outputs at the new resolution.
	//
	// Sequence:
	//   1. Pipeline format = 640x480, decoder outputs 320x240 → scaled to 640x480
	//   2. Atomic store pipeline format = 1280x720
	//   3. Next decoded frame → scaled to 1280x720
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

	var pfPtr atomic.Pointer[PipelineFormat]
	initialFormat := &PipelineFormat{Width: 640, Height: 480, FPSNum: 30, FPSDen: 1, Name: "480p30"}
	pfPtr.Store(initialFormat)

	sd := newSourceDecoder("cam1", factory, callback, nil, &pfPtr)
	if sd == nil {
		t.Fatal("newSourceDecoder returned nil")
	}
	defer sd.Close()

	makeFrame := func(pts int64, groupID uint32) *media.VideoFrame {
		return &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: true,
			WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
			SPS:        []byte{0x67, 0x42, 0x00, 0x1e},
			PPS:        []byte{0x68, 0xce, 0x38, 0x80},
			Codec:      "h264",
			GroupID:    groupID,
		}
	}

	waitForN := func(n int) {
		deadline := time.After(2 * time.Second)
		for {
			mu.Lock()
			count := len(received)
			mu.Unlock()
			if count >= n {
				return
			}
			select {
			case <-deadline:
				t.Fatalf("timeout waiting for %d frames, got %d", n, count)
			default:
				time.Sleep(5 * time.Millisecond)
			}
		}
	}

	// Step 1: Send first frame with pipeline format 640x480.
	sd.Send(makeFrame(90000, 1), 0)
	waitForN(1)

	mu.Lock()
	frame1 := received[0]
	mu.Unlock()

	if frame1.Width != 640 || frame1.Height != 480 {
		t.Errorf("frame 1: dimensions = %dx%d, want 640x480", frame1.Width, frame1.Height)
	}
	expectedSize1 := 640 * 480 * 3 / 2
	if len(frame1.YUV) != expectedSize1 {
		t.Errorf("frame 1: YUV size = %d, want %d", len(frame1.YUV), expectedSize1)
	}

	// Step 2: Change pipeline format to 1280x720.
	newFormat := &PipelineFormat{Width: 1280, Height: 720, FPSNum: 60, FPSDen: 1, Name: "720p60"}
	pfPtr.Store(newFormat)

	// Step 3: Send second frame — should be scaled to 1280x720.
	sd.Send(makeFrame(180000, 2), 0)
	waitForN(2)

	mu.Lock()
	frame2 := received[1]
	mu.Unlock()

	if frame2.Width != 1280 || frame2.Height != 720 {
		t.Errorf("frame 2: dimensions = %dx%d, want 1280x720", frame2.Width, frame2.Height)
	}
	expectedSize2 := 1280 * 720 * 3 / 2
	if len(frame2.YUV) != expectedSize2 {
		t.Errorf("frame 2: YUV size = %d, want %d", len(frame2.YUV), expectedSize2)
	}
}

func TestSourceDecoderPoolDimensionMismatch(t *testing.T) {
	// Bug 3: If decoded frame is larger than pool buffer (e.g., 4K source
	// with 1080p pool), buf[:yuvSize] panics because yuvSize > cap(buf).
	// The fix should detect undersized pool buffers and fall back to make().

	// Mock decoder returns 640x480 frames (larger than pool).
	factory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(640, 480), nil
	}

	var mu sync.Mutex
	var received []*ProcessingFrame
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		received = append(received, pf)
		mu.Unlock()
	}

	// Pool is sized for 320x240 — smaller than the 640x480 decoded frame.
	pool := NewFramePool(4, 320, 240)

	sd := newSourceDecoder("cam1", factory, callback, pool, nil)
	if sd == nil {
		t.Fatal("newSourceDecoder returned nil")
	}
	defer sd.Close()

	// Send a keyframe — decode produces 640x480, which is larger than pool buffers.
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

	// This should not panic even though pool buffers are too small.
	sd.Send(frame, 0)

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
	pf := received[0]
	if pf.Width != 640 || pf.Height != 480 {
		t.Errorf("dimensions = %dx%d, want 640x480", pf.Width, pf.Height)
	}
	expectedSize := 640 * 480 * 3 / 2
	if len(pf.YUV) != expectedSize {
		t.Errorf("YUV size = %d, want %d", len(pf.YUV), expectedSize)
	}
}
