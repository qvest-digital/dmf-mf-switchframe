package switcher

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

func TestEncodeNode_AlwaysActive(t *testing.T) {
	n := &encodeNode{}
	require.True(t, n.Active(), "encode node is always active")
	require.Equal(t, "h264-encode", n.Name())
	require.True(t, n.Latency() > 0)
	require.NoError(t, n.Close())
}

func TestEncodeNode_ProcessEncodes(t *testing.T) {
	mockEnc := transition.NewMockEncoder()
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return mockEnc, nil
		},
	}

	var encoded atomic.Pointer[media.VideoFrame]
	var forceIDR atomic.Bool

	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {
			encoded.Store(frame)
		},
	}
	n.start()
	defer func() { _ = n.Close() }()

	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
	}
	pf.SetRefs(1)

	out := n.Process(nil, pf)
	require.Same(t, pf, out, "encodeNode always returns src")

	// Wait for async encode to complete
	require.Eventually(t, func() bool {
		return encoded.Load() != nil
	}, time.Second, time.Millisecond, "onEncoded should have been called")
	require.Equal(t, int64(1000), encoded.Load().PTS)
}

func TestEncodeNode_ForceIDR(t *testing.T) {
	mockEnc := transition.NewMockEncoder()
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return mockEnc, nil
		},
	}

	var forceIDR atomic.Bool
	forceIDR.Store(true)

	var encoded atomic.Pointer[media.VideoFrame]
	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {
			encoded.Store(frame)
		},
	}
	n.start()
	defer func() { _ = n.Close() }()

	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2),
		Width:      4,
		Height:     4,
		PTS:        2000,
		IsKeyframe: false,
		Codec:      "h264",
	}
	pf.SetRefs(1)

	n.Process(nil, pf)
	require.Eventually(t, func() bool {
		return encoded.Load() != nil
	}, time.Second, time.Millisecond)
	// After CompareAndSwap, forceIDR should be false
	require.False(t, forceIDR.Load(), "forceIDR should be consumed")
}

func TestEncodeNode_EncodeError(t *testing.T) {
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &failingMockEncoder{}, nil
		},
	}

	var forceIDR atomic.Bool
	var callCount atomic.Int64
	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {
			callCount.Add(1)
		},
	}
	n.start()
	defer func() { _ = n.Close() }()

	pf := &ProcessingFrame{
		YUV:    make([]byte, 4*4*3/2),
		Width:  4,
		Height: 4,
		PTS:    3000,
	}
	pf.SetRefs(1)

	out := n.Process(nil, pf)
	require.Same(t, pf, out, "should return src even on error")

	// Wait for async encode to attempt and report error
	require.Eventually(t, func() bool {
		return n.Err() != nil
	}, time.Second, time.Millisecond, "Err() should report the encode error")
	require.Equal(t, int64(0), callCount.Load(), "onEncoded should not be called on error")
}

func TestEncodeNode_ProcessReturnsImmediately(t *testing.T) {
	// Encoder that takes 200ms per frame — Process() should still return instantly.
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &delayedMockEncoder{delay: 200 * time.Millisecond}, nil
		},
	}

	var forceIDR atomic.Bool
	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {
		},
	}
	n.start()
	defer func() { _ = n.Close() }()

	pf := &ProcessingFrame{
		YUV:    make([]byte, 4*4*3/2),
		Width:  4,
		Height: 4,
		PTS:    1000,
		Codec:  "h264",
	}
	pf.SetRefs(1)

	start := time.Now()
	n.Process(nil, pf)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 50*time.Millisecond,
		"Process() should return immediately, not block for encode")
}

func TestEncodeNode_DropCounterOnBackpressure(t *testing.T) {
	// Block the encoder goroutine so the channel fills up.
	started := make(chan struct{})
	blockCh := make(chan struct{})
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &blockingMockEncoder{blockCh: blockCh, started: started}, nil
		},
	}

	var forceIDR atomic.Bool
	var dropCount atomic.Int64
	n := &encodeNode{
		codecs:          codecs,
		forceIDR:        &forceIDR,
		encodeDropCount: &dropCount,
		onEncoded:       func(frame *media.VideoFrame) {},
	}
	n.start()
	defer func() {
		close(blockCh) // unblock encoder so Close() can drain
		_ = n.Close()
	}()

	// First frame: goes to the encoder goroutine (blocks on blockCh).
	pf1 := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4, PTS: 1000, Codec: "h264",
	}
	pf1.SetRefs(1)
	n.Process(nil, pf1)

	// Wait for goroutine to pick up pf1 and block on encode (deterministic).
	<-started

	// Second frame: fills the channel buffer (size 1).
	pf2 := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4, PTS: 2000, Codec: "h264",
	}
	pf2.SetRefs(1)
	n.Process(nil, pf2)

	// Third frame: channel full, should be dropped.
	pf3 := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4, PTS: 3000, Codec: "h264",
	}
	pf3.SetRefs(1)
	n.Process(nil, pf3)

	require.Equal(t, int64(1), dropCount.Load(), "one frame should be dropped due to backpressure")
}

func TestEncodeNode_ForceIDRReArmedOnDrop(t *testing.T) {
	// When a frame with forceIDR is dropped (channel full), the IDR request
	// must be re-armed so the next frame carries it.
	started := make(chan struct{})
	blockCh := make(chan struct{})
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &blockingMockEncoder{blockCh: blockCh, started: started}, nil
		},
	}

	var forceIDR atomic.Bool
	var dropCount atomic.Int64
	n := &encodeNode{
		codecs:          codecs,
		forceIDR:        &forceIDR,
		encodeDropCount: &dropCount,
		onEncoded:       func(frame *media.VideoFrame) {},
	}
	n.start()
	defer func() {
		close(blockCh)
		_ = n.Close()
	}()

	// First frame: picked up by goroutine and blocks.
	pf1 := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4, PTS: 1000, Codec: "h264",
	}
	pf1.SetRefs(1)
	n.Process(nil, pf1)
	<-started

	// Second frame: fills channel.
	pf2 := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4, PTS: 2000, Codec: "h264",
	}
	pf2.SetRefs(1)
	n.Process(nil, pf2)

	// Set forceIDR, then send a third frame that will be dropped.
	forceIDR.Store(true)
	pf3 := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4, PTS: 3000, Codec: "h264",
	}
	pf3.SetRefs(1)
	n.Process(nil, pf3)

	require.Equal(t, int64(1), dropCount.Load(), "frame should be dropped")
	require.True(t, forceIDR.Load(), "forceIDR should be re-armed after drop")
}

func TestEncodeNode_CloseWaitsForPending(t *testing.T) {
	var encodeDone atomic.Bool
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &callbackMockEncoder{
				onEncode: func() {
					time.Sleep(50 * time.Millisecond)
					encodeDone.Store(true)
				},
			}, nil
		},
	}

	var forceIDR atomic.Bool
	n := &encodeNode{
		codecs:    codecs,
		forceIDR:  &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {},
	}
	n.start()

	pf := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4, PTS: 1000, Codec: "h264",
	}
	pf.SetRefs(1)
	n.Process(nil, pf)

	// Close() should wait for the pending encode to finish.
	n.Close()
	require.True(t, encodeDone.Load(), "Close() should have waited for pending encode")
}

func TestEncodeNode_CloseIdempotent(t *testing.T) {
	// Double-close must not panic.
	n := &encodeNode{}
	n.start()
	require.NoError(t, n.Close())
	require.NoError(t, n.Close()) // second close is a no-op
}

func TestEncodeNode_RefReleaseLifecycle(t *testing.T) {
	// Verify that the frame's ref is properly managed:
	// - Process() adds a ref for the async goroutine
	// - The goroutine releases its ref after encode
	// - The caller can independently release its own ref
	mockEnc := transition.NewMockEncoder()
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return mockEnc, nil
		},
	}

	var wg sync.WaitGroup
	wg.Add(1)
	var forceIDR atomic.Bool
	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {
			wg.Done()
		},
	}
	n.start()
	defer n.Close()

	pool := NewFramePool(1, 4, 4)
	buf := pool.Acquire()
	pf := &ProcessingFrame{
		YUV:    buf[:4*4*3/2],
		Width:  4,
		Height: 4,
		PTS:    1000,
		Codec:  "h264",
		pool:   pool,
	}
	pf.SetRefs(1)

	n.Process(nil, pf)

	// At this point: refs should be 2 (pipeline + async goroutine)
	require.Equal(t, int32(2), pf.Refs())

	// Wait for async encode to complete
	wg.Wait()

	// After encode goroutine finishes: refs should be 1 (pipeline only)
	require.Eventually(t, func() bool {
		return pf.Refs() == 1
	}, time.Second, time.Millisecond)

	// Pipeline releases its ref
	pf.ReleaseYUV()
	require.Nil(t, pf.YUV, "buffer should be returned to pool")
}

func TestEncodeNode_CloseWithoutStart(t *testing.T) {
	// Close() on an unstarted node should be a no-op.
	n := &encodeNode{}
	require.NoError(t, n.Close())
}

func TestEncodeNode_ProcessWithoutStart(t *testing.T) {
	// Process() on an unstarted node should fall back to synchronous encode.
	mockEnc := transition.NewMockEncoder()
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return mockEnc, nil
		},
	}

	var encoded atomic.Pointer[media.VideoFrame]
	var forceIDR atomic.Bool
	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {
			encoded.Store(frame)
		},
	}
	// Note: start() NOT called

	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
	}

	out := n.Process(nil, pf)
	require.Same(t, pf, out)
	require.NotNil(t, encoded.Load(), "synchronous fallback should still encode")
}

func TestEncodeNode_PanicRecovery(t *testing.T) {
	// A panic in the encoder (e.g., cgo FFmpeg crash) must not kill the
	// goroutine — subsequent frames should still encode successfully.
	callCount := atomic.Int64{}
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &panickingMockEncoder{
				callCount: &callCount,
				panicOnce: true,
			}, nil
		},
	}

	var forceIDR atomic.Bool
	var encoded atomic.Pointer[media.VideoFrame]
	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {
			encoded.Store(frame)
		},
	}
	n.start()
	defer n.Close()

	// First frame: will panic inside encoder.
	pf1 := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4, PTS: 1000, Codec: "h264",
	}
	pf1.SetRefs(1)
	n.Process(nil, pf1)

	// Wait for panic to be caught.
	require.Eventually(t, func() bool {
		return n.Err() != nil
	}, time.Second, time.Millisecond, "Err() should report the panic")
	require.Contains(t, n.Err().Error(), "encode panic")

	// Second frame: goroutine should still be alive and process it.
	pf2 := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4, PTS: 2000, Codec: "h264",
	}
	pf2.SetRefs(1)
	n.Process(nil, pf2)

	require.Eventually(t, func() bool {
		return encoded.Load() != nil
	}, time.Second, time.Millisecond, "goroutine should survive panic and encode next frame")
	require.Equal(t, int64(2000), encoded.Load().PTS)
}

// panickingMockEncoder panics on the first call, then delegates to mock.
type panickingMockEncoder struct {
	callCount *atomic.Int64
	panicOnce bool
	inner     transition.VideoEncoder
	once      sync.Once
}

func (e *panickingMockEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	e.once.Do(func() { e.inner = transition.NewMockEncoder() })
	n := e.callCount.Add(1)
	if e.panicOnce && n == 1 {
		panic("simulated cgo crash")
	}
	return e.inner.Encode(yuv, pts, forceIDR)
}
func (e *panickingMockEncoder) Close() {}

// failingMockEncoder always returns an error from Encode.
type failingMockEncoder struct{}

func (e *failingMockEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return nil, false, fmt.Errorf("mock encode error")
}
func (e *failingMockEncoder) Close() {}

// delayedMockEncoder delays encode by a configurable duration.
type delayedMockEncoder struct {
	delay time.Duration
}

func (e *delayedMockEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	time.Sleep(e.delay)
	return transition.NewMockEncoder().Encode(yuv, pts, forceIDR)
}
func (e *delayedMockEncoder) Close() {}

// blockingMockEncoder blocks until blockCh is closed.
// Signals on started channel when Encode() begins (for deterministic tests).
type blockingMockEncoder struct {
	blockCh <-chan struct{}
	started chan<- struct{}
	once    sync.Once
	inner   transition.VideoEncoder
}

func (e *blockingMockEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	e.once.Do(func() {
		e.inner = transition.NewMockEncoder()
		if e.started != nil {
			close(e.started)
		}
	})
	<-e.blockCh
	return e.inner.Encode(yuv, pts, forceIDR)
}
func (e *blockingMockEncoder) Close() {}

// callbackMockEncoder calls onEncode before delegating to mock.
type callbackMockEncoder struct {
	onEncode func()
	inner    transition.VideoEncoder
	once     sync.Once
}

func (e *callbackMockEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	e.once.Do(func() {
		e.inner = transition.NewMockEncoder()
	})
	if e.onEncode != nil {
		e.onEncode()
	}
	return e.inner.Encode(yuv, pts, forceIDR)
}
func (e *callbackMockEncoder) Close() {}
