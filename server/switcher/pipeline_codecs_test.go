package switcher

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

func TestPipelineCodecs_DecodeToProcessingFrame(t *testing.T) {
	pc := &pipelineCodecs{
		decoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	}

	frame := &media.VideoFrame{
		PTS:        1000,
		DTS:        900,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		Codec:      "h264",
		GroupID:    5,
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0x42, 0x00},
	}

	pf, err := pc.decode(frame, nil)
	require.NoError(t, err)
	require.NotNil(t, pf)
	require.Equal(t, 4, pf.Width)
	require.Equal(t, 4, pf.Height)
	require.Equal(t, int64(1000), pf.PTS)
	require.Equal(t, int64(900), pf.DTS)
	require.True(t, pf.IsKeyframe)
	require.Equal(t, "h264", pf.Codec)
	require.Equal(t, uint32(5), pf.GroupID)
	require.Equal(t, 4*4*3/2, len(pf.YUV))
}

func TestPipelineCodecs_DecodeNeedsKeyframe(t *testing.T) {
	pc := &pipelineCodecs{
		decoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	}

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: false,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x41, 0x9a, 0x80, 0x40},
	}

	pf, err := pc.decode(frame, nil)
	require.Error(t, err, "should fail without keyframe to init decoder")
	require.Nil(t, pf)
}

func TestPipelineCodecs_EncodeProcessingFrame(t *testing.T) {
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}
	// Must init encoder first (needs dimensions)
	pc.encWidth = 4
	pc.encHeight = 4
	enc, err := pc.encoderFactory(4, 4, 4_000_000, 30)
	require.NoError(t, err)
	pc.encoder = enc

	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    5,
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)
	require.Equal(t, int64(1000), frame.PTS)
	require.True(t, frame.IsKeyframe)
	require.NotEmpty(t, frame.WireData)
}

func TestPipelineCodecs_DecodeShortBuffer(t *testing.T) {
	// Mock decoder that returns a buffer shorter than w*h*3/2.
	shortDecoder := &shortBufferDecoder{width: 4, height: 4}
	pc := &pipelineCodecs{
		decoderFactory: func() (transition.VideoDecoder, error) {
			return shortDecoder, nil
		},
	}

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0x42, 0x00},
	}

	pf, err := pc.decode(frame, nil)
	require.Error(t, err, "should return error for short buffer, not panic")
	require.Nil(t, pf)
	require.Contains(t, err.Error(), "decoder buffer too small")
}

func TestPipelineCodecs_ResolutionChange(t *testing.T) {
	encoderCreateCount := 0
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			encoderCreateCount++
			return transition.NewMockEncoder(), nil
		},
	}

	// First encode at 4x4
	pf := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 1, encoderCreateCount)
	require.Equal(t, 4, pc.encWidth)
	require.Equal(t, 4, pc.encHeight)

	// Encode at 8x8 — encoder should be recreated
	pf2 := &ProcessingFrame{
		YUV: make([]byte, 8*8*3/2), Width: 8, Height: 8,
		PTS: 2000, IsKeyframe: true, Codec: "h264",
	}
	_, err = pc.encode(pf2, true)
	require.NoError(t, err)
	require.Equal(t, 2, encoderCreateCount, "encoder should be recreated on resolution change")
	require.Equal(t, 8, pc.encWidth)
	require.Equal(t, 8, pc.encHeight)
}

// shortBufferDecoder returns a YUV buffer that is too small for the stated dimensions.
type shortBufferDecoder struct {
	width, height int
}

func (d *shortBufferDecoder) Decode(data []byte) ([]byte, int, int, error) {
	// Return a buffer that is half the expected size
	expected := d.width * d.height * 3 / 2
	return make([]byte, expected/2), d.width, d.height, nil
}

func (d *shortBufferDecoder) Close() {}

func TestPipelineCodecs_Close(t *testing.T) {
	pc := &pipelineCodecs{
		decoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
		encoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	// Init decoder via a decode call
	frame := &media.VideoFrame{
		PTS: 1000, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS:      []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:      []byte{0x68, 0x42, 0x00},
	}
	_, err := pc.decode(frame, nil)
	require.NoError(t, err)
	require.NotNil(t, pc.decoder)

	pc.close()
	require.Nil(t, pc.decoder)
	require.Nil(t, pc.encoder)
}

// flushTrackingDecoder wraps a decoder and counts Flush() and Close() calls.
type flushTrackingDecoder struct {
	inner      transition.VideoDecoder
	flushCount atomic.Int32
	closeCount atomic.Int32
}

func (d *flushTrackingDecoder) Decode(data []byte) ([]byte, int, int, error) {
	return d.inner.Decode(data)
}

func (d *flushTrackingDecoder) Close() {
	d.closeCount.Add(1)
	d.inner.Close()
}

func (d *flushTrackingDecoder) Flush() {
	d.flushCount.Add(1)
}

func makeGOP(n int) []*media.VideoFrame {
	frames := make([]*media.VideoFrame, n)
	frames[0] = &media.VideoFrame{
		PTS: 100, IsKeyframe: true,
		WireData: []byte{0x01},
		SPS:      []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:      []byte{0x68, 0x42, 0x00},
	}
	for i := 1; i < n; i++ {
		frames[i] = &media.VideoFrame{
			PTS:      int64(100 + i*33333),
			WireData: []byte{0x02},
		}
	}
	return frames
}

func TestReplayGOP_PoolReuse(t *testing.T) {
	// First call creates from factory (factoryCount=1).
	// Second call reuses pool (factoryCount still 1).
	var factoryCount atomic.Int32
	factory := func() (transition.VideoDecoder, error) {
		factoryCount.Add(1)
		return transition.NewMockDecoder(4, 4), nil
	}

	pc := &pipelineCodecs{
		decoder:        transition.NewMockDecoder(4, 4),
		decoderFactory: factory,
	}

	frames := makeGOP(3)

	// First call — no pool decoder, creates via factory.
	pc.replayGOP(frames)
	require.Equal(t, int32(1), factoryCount.Load(), "first replayGOP should create from factory")
	require.NotNil(t, pc.replayDecoder, "old pipeline decoder should be recycled into pool")

	// Second call — takes from pool, no factory call.
	pc.replayGOP(frames)
	require.Equal(t, int32(1), factoryCount.Load(), "second replayGOP should reuse pool, not factory")
	require.NotNil(t, pc.replayDecoder, "pool should be replenished after second call")

	// Verify instrumentation.
	require.Equal(t, int64(2), pc.replayGOPCount.Load())
	require.Equal(t, int64(1), pc.replayGOPPoolHits.Load(), "second call should be a pool hit")
}

func TestReplayGOP_PoolEliminatesColdStartGap(t *testing.T) {
	// Use a slow factory (500ms). Pre-warm the replayDecoder.
	// replayGOP time should be ~N×decode_time, NOT 500ms+.
	slowFactory := func() (transition.VideoDecoder, error) {
		time.Sleep(500 * time.Millisecond)
		return transition.NewMockDecoder(4, 4), nil
	}

	frames := makeGOP(5)

	// Cold path: no pre-warmed decoder.
	pcCold := &pipelineCodecs{
		decoder:        transition.NewMockDecoder(4, 4),
		decoderFactory: slowFactory,
	}
	coldStart := time.Now()
	pcCold.replayGOP(frames)
	coldDur := time.Since(coldStart)

	// Warm path: pre-warmed replayDecoder.
	pcWarm := &pipelineCodecs{
		decoder:        transition.NewMockDecoder(4, 4),
		decoderFactory: slowFactory,
		replayDecoder:  transition.NewMockDecoder(4, 4),
	}
	warmStart := time.Now()
	pcWarm.replayGOP(frames)
	warmDur := time.Since(warmStart)

	t.Logf("Cold: %v, Warm: %v", coldDur, warmDur)

	require.Greater(t, coldDur, 400*time.Millisecond,
		"cold path should include factory creation time")
	require.Less(t, warmDur, 100*time.Millisecond,
		"warm path should skip factory, just flush+decode")
}

func TestReplayGOP_FlushCalledOnReuse(t *testing.T) {
	// Verify Flush is called when reusing from pool, NOT on factory creation.
	poolDec := &flushTrackingDecoder{inner: transition.NewMockDecoder(4, 4)}
	pc := &pipelineCodecs{
		decoder:       transition.NewMockDecoder(4, 4),
		replayDecoder: poolDec,
		decoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	}

	frames := makeGOP(3)
	pc.replayGOP(frames)

	require.Equal(t, int32(1), poolDec.flushCount.Load(),
		"Flush should be called when reusing pooled decoder")
	require.Equal(t, int32(0), poolDec.closeCount.Load(),
		"pooled decoder should NOT be closed (recycled)")
}

func TestPipelineCodecs_CloseReleasesPool(t *testing.T) {
	dec := &flushTrackingDecoder{inner: transition.NewMockDecoder(4, 4)}
	poolDec := &flushTrackingDecoder{inner: transition.NewMockDecoder(4, 4)}

	pc := &pipelineCodecs{
		decoder:       dec,
		replayDecoder: poolDec,
	}

	pc.close()

	require.Equal(t, int32(1), dec.closeCount.Load(), "pipeline decoder should be closed")
	require.Equal(t, int32(1), poolDec.closeCount.Load(), "pool decoder should be closed")
	require.Nil(t, pc.decoder)
	require.Nil(t, pc.replayDecoder)
}

func TestReplayGOP_FailedDecodeReturnsToPool(t *testing.T) {
	// When no frames decode successfully, the replay decoder should be
	// returned to the pool (not wasted).
	poolDec := &flushTrackingDecoder{inner: &failingDecoder{}}
	pc := &pipelineCodecs{
		decoder:       transition.NewMockDecoder(4, 4),
		replayDecoder: poolDec,
		decoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	}

	frames := makeGOP(3)
	pc.replayGOP(frames)

	// Decoder should be returned to pool, not closed.
	require.Equal(t, int32(0), poolDec.closeCount.Load(),
		"failed decode should return decoder to pool, not close it")
	require.NotNil(t, pc.replayDecoder,
		"pool should still have a decoder after failed replay")
}

// failingDecoder always returns an error from Decode.
type failingDecoder struct{}

func (d *failingDecoder) Decode(data []byte) ([]byte, int, int, error) {
	return nil, 0, 0, fmt.Errorf("mock decode failure")
}

func (d *failingDecoder) Close() {}

func TestPipelineCodecs_PrewarmRaceOnClose(t *testing.T) {
	// Verify that close() waits for an in-flight prewarm goroutine and
	// that no decoders are leaked. Runs multiple iterations to exercise
	// timing variations under the race detector.
	for i := 0; i < 20; i++ {
		var callCount atomic.Int32
		pc := &pipelineCodecs{
			decoderFactory: func() (transition.VideoDecoder, error) {
				n := callCount.Add(1)
				if n > 1 {
					// Slow prewarm to increase overlap with close().
					time.Sleep(10 * time.Millisecond)
				}
				return &flushTrackingDecoder{inner: transition.NewMockDecoder(4, 4)}, nil
			},
		}

		frame := &media.VideoFrame{
			PTS: 1000, IsKeyframe: true,
			WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
			SPS:      []byte{0x67, 0x42, 0x00, 0x0a},
			PPS:      []byte{0x68, 0x42, 0x00},
		}
		_, err := pc.decode(frame, nil)
		require.NoError(t, err)

		// Call close() while prewarm goroutine is likely still running.
		pc.close()

		pc.mu.Lock()
		require.True(t, pc.closed, "closed flag must be set")
		require.Nil(t, pc.decoder, "decoder must be nil after close")
		require.Nil(t, pc.replayDecoder, "replayDecoder must be nil after close")
		pc.mu.Unlock()
	}
}

func TestPipelineCodecs_PrewarmGoroutineCleansUpAfterClose(t *testing.T) {
	// A slow prewarm factory that finishes after close() is called should
	// see the closed flag and clean up the decoder instead of storing it.
	factoryStarted := make(chan struct{})
	factoryProceed := make(chan struct{})
	var prewarmDec atomic.Pointer[flushTrackingDecoder]
	var callCount atomic.Int32

	pc := &pipelineCodecs{
		decoderFactory: func() (transition.VideoDecoder, error) {
			n := callCount.Add(1)
			dec := &flushTrackingDecoder{inner: transition.NewMockDecoder(4, 4)}
			if n > 1 {
				// This is the prewarm call — synchronize with the test.
				prewarmDec.Store(dec)
				factoryStarted <- struct{}{}
				<-factoryProceed
			}
			return dec, nil
		},
	}

	frame := &media.VideoFrame{
		PTS: 1000, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS:      []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:      []byte{0x68, 0x42, 0x00},
	}
	_, err := pc.decode(frame, nil)
	require.NoError(t, err)

	// Wait for prewarm goroutine to enter the factory.
	<-factoryStarted

	// Release the factory — close() will wait for prewarm via WaitGroup.
	// The prewarm goroutine should see pc.closed and close the decoder.
	close(factoryProceed)
	pc.close()

	dec := prewarmDec.Load()
	require.NotNil(t, dec)
	require.Equal(t, int32(1), dec.closeCount.Load(),
		"prewarm decoder must be closed (either by prewarm seeing closed flag, or by close())")

	pc.mu.Lock()
	require.Nil(t, pc.replayDecoder, "replayDecoder must be nil after close")
	pc.mu.Unlock()
}

func BenchmarkPipelineEncode(b *testing.B) {
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	pf := &ProcessingFrame{
		YUV:        make([]byte, 320*240*3/2),
		Width:      320,
		Height:     240,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    1,
	}

	// Prime the encoder
	_, err := pc.encode(pf, true)
	require.NoError(b, err)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		pf.PTS = int64(i * 3000)
		pf.IsKeyframe = i%30 == 0
		_, _ = pc.encode(pf, i%30 == 0)
	}
}

func TestPipelineCodecs_DecodeWithPrecomputedAnnexB(t *testing.T) {
	pc := &pipelineCodecs{
		decoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	}

	frame := &media.VideoFrame{
		PTS:        1000,
		DTS:        900,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		Codec:      "h264",
		GroupID:    5,
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0x42, 0x00},
	}

	// Decode without pre-computed AnnexB (nil)
	pfWithout, err := pc.decode(frame, nil)
	require.NoError(t, err)
	require.NotNil(t, pfWithout)

	// Reset decoder for a fair comparison
	pc.decoder.Close()
	pc.decoder = nil

	// Pre-compute AnnexB
	annexB := codec.AVC1ToAnnexB(frame.WireData)
	annexB = codec.PrependSPSPPS(frame.SPS, frame.PPS, annexB)

	// Decode with pre-computed AnnexB
	pfWith, err := pc.decode(frame, annexB)
	require.NoError(t, err)
	require.NotNil(t, pfWith)

	// Both paths should produce identical ProcessingFrame metadata
	require.Equal(t, pfWithout.Width, pfWith.Width)
	require.Equal(t, pfWithout.Height, pfWith.Height)
	require.Equal(t, pfWithout.PTS, pfWith.PTS)
	require.Equal(t, pfWithout.DTS, pfWith.DTS)
	require.Equal(t, pfWithout.IsKeyframe, pfWith.IsKeyframe)
	require.Equal(t, pfWithout.Codec, pfWith.Codec)
	require.Equal(t, pfWithout.GroupID, pfWith.GroupID)
	require.Equal(t, len(pfWithout.YUV), len(pfWith.YUV))
}
