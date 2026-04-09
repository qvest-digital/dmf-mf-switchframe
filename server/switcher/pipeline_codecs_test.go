package switcher

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

func TestPipelineCodecs_EncodeProcessingFrame(t *testing.T) {
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}
	// Must init encoder first (needs dimensions)
	pc.encWidth = 4
	pc.encHeight = 4
	enc, err := pc.encoderFactory(4, 4, 4_000_000, 30, 1)
	require.NoError(t, err)
	pc.encoder = enc

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
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

func TestPipelineCodecs_ResolutionChange(t *testing.T) {
	encoderCreateCount := 0
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			encoderCreateCount++
			return transition.NewMockEncoder(), nil
		},
	}

	// First encode at 4x4
	pf := &ProcessingFrame{
		Data: make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 1, encoderCreateCount)
	require.Equal(t, 4, pc.encWidth)
	require.Equal(t, 4, pc.encHeight)

	// Encode at 8x8 -- encoder should be recreated
	pf2 := &ProcessingFrame{
		Data: make([]byte, ColorYUV420_8bit.FrameSize(8, 8)), Width: 8, Height: 8,
		PTS: 2000, IsKeyframe: true, Codec: "h264",
	}
	_, err = pc.encode(pf2, true)
	require.NoError(t, err)
	require.Equal(t, 2, encoderCreateCount, "encoder should be recreated on resolution change")
	require.Equal(t, 8, pc.encWidth)
	require.Equal(t, 8, pc.encHeight)
}

func TestPipelineCodecs_Close(t *testing.T) {
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	// Init encoder via an encode call
	pf := &ProcessingFrame{
		Data: make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, pc.encoder)

	pc.close()
	require.Nil(t, pc.encoder)
}

func TestPipelineCodecs_DTSEqualsPTS(t *testing.T) {
	// The pipeline encoder has max_b_frames=0, so output DTS must equal PTS.
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	pf := &ProcessingFrame{
		Data: make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), Width: 4, Height: 4,
		PTS: 9000, DTS: 3000, // Source had B-frames: DTS != PTS
		IsKeyframe: true, Codec: "h264",
	}
	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)
	require.Equal(t, frame.PTS, frame.DTS, "DTS must equal PTS (no B-frames in output)")
}

func TestPipelineCodecs_MonotonicPTS(t *testing.T) {
	// B-frame sources can produce scrambled PTS from the sourceDecoder.
	// The pipeline must enforce monotonic output PTS.
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	mkFrame := func(pts int64) *ProcessingFrame {
		return &ProcessingFrame{
			Data: make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), Width: 4, Height: 4,
			PTS: pts, DTS: pts, IsKeyframe: true, Codec: "h264",
		}
	}

	// Simulate B-frame PTS reorder: 0, 9000, 3000, 6000
	scrambled := []int64{0, 9000, 3000, 6000}
	var outputPTS []int64
	for _, pts := range scrambled {
		frame, err := pc.encode(mkFrame(pts), true)
		require.NoError(t, err)
		require.NotNil(t, frame)
		outputPTS = append(outputPTS, frame.PTS)
	}

	// Output must be monotonically non-decreasing
	for i := 1; i < len(outputPTS); i++ {
		require.Greater(t, outputPTS[i], outputPTS[i-1],
			"output PTS must be monotonically increasing: PTS[%d]=%d <= PTS[%d]=%d",
			i, outputPTS[i], i-1, outputPTS[i-1])
	}
}

func TestPipelineCodecs_ForwardPTSJumpReseeds(t *testing.T) {
	// When switching sources, the new source may have a much larger PTS origin.
	// Video PTS must reseed to the new source's timeline (matching the audio
	// mixer's behavior) to prevent permanent A/V desync.
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	mkFrame := func(pts int64) *ProcessingFrame {
		return &ProcessingFrame{
			Data: make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), Width: 4, Height: 4,
			PTS: pts, DTS: pts, IsKeyframe: true, Codec: "h264",
		}
	}

	// Default frame duration at 30000/1001 fps = 3003 ticks (90kHz)
	frameDur := int64(90000) * 1001 / 30000 // 3003

	// Establish a baseline PTS
	f1, err := pc.encode(mkFrame(1_000_000), true)
	require.NoError(t, err)
	require.NotNil(t, f1)
	require.Equal(t, int64(1_000_000), f1.PTS)

	// Normal advancement (within 3x frame duration) — passes through
	f2, err := pc.encode(mkFrame(1_000_000+frameDur), true)
	require.NoError(t, err)
	require.Equal(t, int64(1_000_000)+frameDur, f2.PTS)

	// Simulate source switch: new source has PTS 100 million ticks ahead.
	// Should reseed to the new source's PTS (not cap to one frame advance).
	f3, err := pc.encode(mkFrame(100_000_000), true)
	require.NoError(t, err)
	require.NotNil(t, f3)

	// Should reseed to 100_000_000 (the new source's PTS)
	require.Equal(t, int64(100_000_000), f3.PTS,
		"large forward PTS jump should reseed to new source PTS")

	// Subsequent frames should continue from the reseeded value
	f4, err := pc.encode(mkFrame(100_000_000+frameDur), true)
	require.NoError(t, err)
	require.Equal(t, int64(100_000_000)+frameDur, f4.PTS,
		"next frame should continue from reseeded PTS")
}

func TestPipelineCodecs_OutputPTSPassesThroughUnmasked(t *testing.T) {
	// The pipeline encoder no longer masks PTS to 33 bits. The muxer
	// handles PTS rebasing and wrapping. This ensures video and audio
	// PTS stay in the same domain (both use wall-clock values that may
	// exceed 33 bits after hours of operation).
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	mkFrame := func(pts int64) *ProcessingFrame {
		return &ProcessingFrame{
			Data: make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), Width: 4, Height: 4,
			PTS: pts, DTS: pts, IsKeyframe: true, Codec: "h264",
		}
	}

	// Use PTS values that exceed 33 bits (simulating hours of uptime).
	largePTS := int64(29_000_000_000) // ~89 hours at 90kHz
	f1, err := pc.encode(mkFrame(largePTS), true)
	require.NoError(t, err)
	require.NotNil(t, f1)

	// Output PTS should pass through unmasked.
	require.Equal(t, largePTS, f1.PTS,
		"encode should not mask PTS to 33 bits — muxer handles wrapping")

	// Subsequent frames should also pass through.
	f2, err := pc.encode(mkFrame(largePTS+3750), true)
	require.NoError(t, err)
	require.NotNil(t, f2)
	require.Equal(t, largePTS+3750, f2.PTS,
		"subsequent frame PTS should pass through unmasked")
}

func TestPipelineCodecs_DefaultBitrateForResolution(t *testing.T) {
	require.Equal(t, 10_000_000, defaultBitrateForResolution(1920, 1080), "1080p should default to 10 Mbps")
	require.Equal(t, 6_000_000, defaultBitrateForResolution(1280, 720), "720p should default to 6 Mbps")
	require.Equal(t, 2_000_000, defaultBitrateForResolution(640, 480), "480p should default to 2 Mbps")
	require.Equal(t, 20_000_000, defaultBitrateForResolution(3840, 2160), "4K should default to 20 Mbps")
}

func TestPipelineCodecs_BitrateChangeRecreatesEncoder(t *testing.T) {
	var lastBitrate int
	encoderCreateCount := 0
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			encoderCreateCount++
			lastBitrate = bitrate
			return transition.NewMockEncoder(), nil
		},
	}

	pf := &ProcessingFrame{
		Data: make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}

	// First encode uses resolution-based default (no source stats yet)
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 1, encoderCreateCount)
	initialBitrate := lastBitrate

	// Simulate source stats arriving with a bitrate ABOVE the resolution
	// default (>20% higher). Only bitrates above the floor trigger recreation.
	newBitrate := initialBitrate * 3 // 3x the resolution default
	pc.updateSourceStats(float64(newBitrate)/(30*8), 30)

	// Next encode should recreate the encoder with the higher bitrate
	pf.PTS = 2000
	_, err = pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 2, encoderCreateCount, "encoder should be recreated when source bitrate exceeds floor by >20%%")
	require.Equal(t, newBitrate, lastBitrate, "new encoder should use source bitrate (above floor)")
}

func TestPipelineCodecs_LowSourceBitrateUsesFloor(t *testing.T) {
	// A low-bitrate source (e.g., demo clips at 1.6 Mbps) should NOT pull
	// the encoder below the resolution-based default. Re-encoding at the
	// source bitrate produces visible generation loss.
	var lastBitrate int
	encoderCreateCount := 0
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			encoderCreateCount++
			lastBitrate = bitrate
			return transition.NewMockEncoder(), nil
		},
	}

	// Use 1280x720 to get a meaningful resolution default (4 Mbps)
	pf := &ProcessingFrame{
		Data: make([]byte, ColorYUV420_8bit.FrameSize(1280, 720)), Width: 1280, Height: 720,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}

	// First encode uses resolution default
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 1, encoderCreateCount)
	require.Equal(t, 6_000_000, lastBitrate, "initial bitrate should be resolution default (6 Mbps for 720p)")

	// Simulate low-bitrate source stats (1.6 Mbps, like demo clips)
	pc.updateSourceStats(float64(1_600_000)/(30*8), 30)

	// Encoder should NOT be recreated — source bitrate below floor doesn't change effective bitrate
	pf.PTS = 2000
	_, err = pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 1, encoderCreateCount, "encoder should NOT be recreated when source is below resolution floor")
	require.Equal(t, 6_000_000, lastBitrate, "bitrate should stay at resolution floor")
}

func TestPipelineCodecs_SmallBitrateChangeKeepsEncoder(t *testing.T) {
	encoderCreateCount := 0
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			encoderCreateCount++
			return transition.NewMockEncoder(), nil
		},
	}

	// Use 1280x720 (floor = 6 Mbps) so we can test above-floor changes
	pf := &ProcessingFrame{
		Data: make([]byte, ColorYUV420_8bit.FrameSize(1280, 720)), Width: 1280, Height: 720,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}

	// First encode creates encoder at floor (4 Mbps)
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 1, encoderCreateCount)

	// Simulate source bitrate 10% above floor (6.6 Mbps) — within 20% threshold
	pc.updateSourceStats(float64(6_600_000)/(30*8), 30)

	// Next encode should NOT recreate the encoder
	pf.PTS = 2000
	_, err = pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 1, encoderCreateCount, "encoder should NOT be recreated for small bitrate change above floor")
}

func TestPipelineCodecs_BitrateOverride(t *testing.T) {
	// When encoderOpts.Bitrate > 0, the encoder factory must receive that
	// bitrate instead of the resolution default. When Bitrate == 0, the
	// resolution default is used.

	t.Run("explicit bitrate overrides resolution default", func(t *testing.T) {
		var capturedBitrate int
		pc := &pipelineCodecs{
			encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
				capturedBitrate = bitrate
				return transition.NewMockEncoder(), nil
			},
			encoderOpts: &codec.EncoderOptions{Bitrate: 8_000_000},
		}

		// Use 1280x720 (resolution default = 6 Mbps) so we can confirm override.
		pf := &ProcessingFrame{
			Data: make([]byte, ColorYUV420_8bit.FrameSize(1280, 720)), Width: 1280, Height: 720,
			PTS: 1000, IsKeyframe: true, Codec: "h264",
		}
		_, err := pc.encode(pf, true)
		require.NoError(t, err)
		require.Equal(t, 8_000_000, capturedBitrate,
			"encoder factory must receive user-configured bitrate, not resolution default")
	})

	t.Run("zero bitrate uses resolution default", func(t *testing.T) {
		var capturedBitrate int
		pc := &pipelineCodecs{
			encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
				capturedBitrate = bitrate
				return transition.NewMockEncoder(), nil
			},
			encoderOpts: &codec.EncoderOptions{Bitrate: 0},
		}

		pf := &ProcessingFrame{
			Data: make([]byte, ColorYUV420_8bit.FrameSize(1280, 720)), Width: 1280, Height: 720,
			PTS: 1000, IsKeyframe: true, Codec: "h264",
		}
		_, err := pc.encode(pf, true)
		require.NoError(t, err)
		require.Equal(t, 6_000_000, capturedBitrate,
			"encoder factory must receive resolution default when bitrate is 0 (auto)")
	})

	t.Run("nil encoderOpts uses resolution default", func(t *testing.T) {
		var capturedBitrate int
		pc := &pipelineCodecs{
			encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
				capturedBitrate = bitrate
				return transition.NewMockEncoder(), nil
			},
			encoderOpts: nil,
		}

		pf := &ProcessingFrame{
			Data: make([]byte, ColorYUV420_8bit.FrameSize(1920, 1080)), Width: 1920, Height: 1080,
			PTS: 1000, IsKeyframe: true, Codec: "h264",
		}
		_, err := pc.encode(pf, true)
		require.NoError(t, err)
		require.Equal(t, 10_000_000, capturedBitrate,
			"encoder factory must receive resolution default when encoderOpts is nil")
	})

	t.Run("explicit bitrate skips source-driven invalidation", func(t *testing.T) {
		encoderCreateCount := 0
		pc := &pipelineCodecs{
			encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
				encoderCreateCount++
				return transition.NewMockEncoder(), nil
			},
			encoderOpts: &codec.EncoderOptions{Bitrate: 8_000_000},
		}

		pf := &ProcessingFrame{
			Data: make([]byte, ColorYUV420_8bit.FrameSize(1280, 720)), Width: 1280, Height: 720,
			PTS: 1000, IsKeyframe: true, Codec: "h264",
		}
		_, err := pc.encode(pf, true)
		require.NoError(t, err)
		require.Equal(t, 1, encoderCreateCount)

		// Simulate a high-bitrate source that would normally trigger invalidation.
		pc.updateSourceStats(float64(20_000_000)/(30*8), 30)

		pf.PTS = 2000
		_, err = pc.encode(pf, true)
		require.NoError(t, err)
		require.Equal(t, 1, encoderCreateCount,
			"encoder must NOT be recreated when user has set explicit bitrate")
	})
}

// buildAnnexBKeyframe constructs Annex B data containing SPS, PPS, and IDR
// NALUs with 4-byte start codes, matching what a real H.264 encoder produces.
func buildAnnexBKeyframe(spsPayload, ppsPayload []byte) []byte {
	startCode := []byte{0x00, 0x00, 0x00, 0x01}
	spsNALU := append([]byte{0x67}, spsPayload...) // type 7 (SPS)
	ppsNALU := append([]byte{0x68}, ppsPayload...) // type 8 (PPS)
	idrNALU := []byte{0x65, 0xAA, 0xBB}            // type 5 (IDR)

	var annexB []byte
	for _, nalu := range [][]byte{spsNALU, ppsNALU, idrNALU} {
		annexB = append(annexB, startCode...)
		annexB = append(annexB, nalu...)
	}
	return annexB
}

func TestPipelineCodecs_SPSPPSIndependentOfWireData(t *testing.T) {
	// Regression test: SPS and PPS must be independent copies, not sub-slices
	// of the WireData buffer. When WireData is returned to a sync.Pool,
	// sub-slices would point to recycled memory, causing corrupted parameter
	// sets for downstream consumers (output muxer, WebTransport writer).

	spsPayload := []byte{0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x6C}
	ppsPayload := []byte{0xCE, 0x3C, 0x80}

	annexBData := buildAnnexBKeyframe(spsPayload, ppsPayload)

	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: annexBData}, nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    1,
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)
	require.NotNil(t, frame.SPS, "keyframe must have SPS")
	require.NotNil(t, frame.PPS, "keyframe must have PPS")

	// Save expected SPS/PPS content
	expectedSPS := append([]byte{0x67}, spsPayload...)
	expectedPPS := append([]byte{0x68}, ppsPayload...)

	require.Equal(t, expectedSPS, frame.SPS)
	require.Equal(t, expectedPPS, frame.PPS)

	// Simulate pool recycling: overwrite the WireData buffer.
	// If SPS/PPS are sub-slices of WireData, they'll be corrupted.
	for i := range frame.WireData {
		frame.WireData[i] = 0xFF
	}

	// SPS and PPS must be unaffected by WireData mutation
	require.Equal(t, expectedSPS, frame.SPS,
		"SPS corrupted after WireData overwrite — still a sub-slice of the pooled buffer")
	require.Equal(t, expectedPPS, frame.PPS,
		"PPS corrupted after WireData overwrite — still a sub-slice of the pooled buffer")
}

// mockAnnexBEncoder returns Annex B data with SPS/PPS/IDR NALUs, matching
// what a real H.264 encoder produces. The encode() pipeline converts this
// to AVC1 format before extracting SPS/PPS.
type mockAnnexBEncoder struct {
	annexB []byte
}

func (e *mockAnnexBEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	out := make([]byte, len(e.annexB))
	copy(out, e.annexB)
	return out, forceIDR, nil
}

func (e *mockAnnexBEncoder) Close() {}

func TestPipelineCodecs_EncodeAfterCloseReturnsError(t *testing.T) {
	// Bug A3: After close(), encode() should return errPipelineClosed
	// instead of lazily creating a new encoder. Without this guard,
	// a racing goroutine can call encode() after close() has released
	// the encoder, creating a new one that is never cleaned up.
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	// Prime the encoder with an initial encode.
	pf := &ProcessingFrame{
		Data: make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, pc.encoder)

	// Close — sets closed flag and nils the encoder.
	pc.close()
	require.True(t, pc.closed)
	require.Nil(t, pc.encoder)

	// Subsequent encode must return errPipelineClosed.
	pf2 := &ProcessingFrame{
		Data: make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), Width: 4, Height: 4,
		PTS: 2000, IsKeyframe: true, Codec: "h264",
	}
	frame, err := pc.encode(pf2, true)
	require.ErrorIs(t, err, errPipelineClosed,
		"encode() after close() should return errPipelineClosed")
	require.Nil(t, frame)

	// The encoder should NOT have been re-created.
	require.Nil(t, pc.encoder, "encoder should remain nil after close()")
}

func TestPipelineCodecs_EncodeAfterCloseWithoutPriorEncode(t *testing.T) {
	// Edge case: close() called before any encode(), then encode().
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}
	pc.close()

	pf := &ProcessingFrame{
		Data: make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}
	frame, err := pc.encode(pf, true)
	require.ErrorIs(t, err, errPipelineClosed)
	require.Nil(t, frame)
}

// slowMockEncoder simulates a real encoder that takes time to encode.
// Close() records whether it was called, allowing the test to verify
// that invalidateEncoder() waits for encode() to finish before closing.
type slowMockEncoder struct {
	encodeStarted chan struct{} // closed when Encode() begins
	encodeFinish  chan struct{} // Encode() blocks until this is closed
	closedAt      int64         // unix nano when Close() was called (atomic)
}

func (e *slowMockEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	// Signal that encoding has started
	close(e.encodeStarted)
	// Block to simulate slow encoding (~33ms in production)
	<-e.encodeFinish
	return []byte{0x00, 0x00, 0x00, 0x01, 0x65}, forceIDR, nil
}

func (e *slowMockEncoder) Close() {
	atomic.StoreInt64(&e.closedAt, time.Now().UnixNano())
}

func TestPipelineCodecs_InvalidateEncoderRace(t *testing.T) {
	// Verifies that invalidateEncoder() cannot close the encoder while
	// encode() is using it. The fix holds the mutex for the entire encode,
	// so invalidateEncoder() blocks until encode() completes.
	//
	// encode() holds the mutex for the entire encode call, preventing concurrent Close().

	slowEnc := &slowMockEncoder{
		encodeStarted: make(chan struct{}),
		encodeFinish:  make(chan struct{}),
	}

	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return slowEnc, nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
	}

	// Start encode in a goroutine — it will block inside Encode()
	encodeDone := make(chan error, 1)
	go func() {
		_, err := pc.encode(pf, true)
		encodeDone <- err
	}()

	// Wait for Encode() to start (encoder is actively in use)
	<-slowEnc.encodeStarted

	// Start invalidateEncoder in another goroutine — with the fix, it
	// blocks on pc.mu until encode() finishes and releases the lock.
	invalidateDone := make(chan struct{})
	go func() {
		pc.invalidateEncoder()
		close(invalidateDone)
	}()

	// Give invalidateEncoder time to block on the mutex. If the old
	// (broken) code were in place, it would acquire the lock immediately
	// and close the encoder while Encode() is still running.
	time.Sleep(10 * time.Millisecond)

	// The encoder should NOT have been closed yet — encode() still holds the lock
	if atomic.LoadInt64(&slowEnc.closedAt) != 0 {
		t.Fatal("encoder was closed while Encode() was still in progress — race condition!")
	}

	// Now let encode() finish
	close(slowEnc.encodeFinish)

	// Both goroutines should complete
	err := <-encodeDone
	require.NoError(t, err)
	<-invalidateDone

	// After both complete, the encoder should have been closed by invalidateEncoder
	require.NotZero(t, atomic.LoadInt64(&slowEnc.closedAt),
		"invalidateEncoder should have closed the encoder after encode() finished")
}

func BenchmarkPipelineEncode(b *testing.B) {
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(320, 240)),
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

func TestPipelineCodecs_NilYUV(t *testing.T) {
	encoderCreated := false
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			encoderCreated = true
			return transition.NewMockEncoder(), nil
		},
	}

	// nil YUV should return (nil, nil) — not an error, not an encoder creation.
	pf := &ProcessingFrame{
		Data: nil, Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}
	frame, err := pc.encode(pf, true)
	require.NoError(t, err, "nil YUV should not produce an error")
	require.Nil(t, frame, "nil YUV should produce nil output")
	require.False(t, encoderCreated, "nil YUV should not create encoder")
}

func TestPipelineCodecs_EmptyYUV(t *testing.T) {
	encoderCreated := false
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			encoderCreated = true
			return transition.NewMockEncoder(), nil
		},
	}

	// Zero-length non-nil YUV should be treated like nil — not an error.
	pf := &ProcessingFrame{
		Data: []byte{}, Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}
	frame, err := pc.encode(pf, true)
	require.NoError(t, err, "empty YUV should not produce an error")
	require.Nil(t, frame, "empty YUV should produce nil output")
	require.False(t, encoderCreated, "empty YUV should not create encoder")
}

// --- HEVC codec awareness tests ---

// buildHEVCAnnexBKeyframe constructs Annex B data containing VPS, SPS, PPS,
// and IDR NALUs with 4-byte start codes, matching what a real HEVC encoder produces.
// HEVC NAL header is 2 bytes: forbidden(1) | type(6) | layerID(6) | tid(3).
// First byte: (type << 1) & 0x7E, second byte: 0x01 (TID=1).
func buildHEVCAnnexBKeyframe(vpsPayload, spsPayload, ppsPayload []byte) []byte {
	startCode := []byte{0x00, 0x00, 0x00, 0x01}

	// VPS: type 32 → first byte = 32<<1 = 0x40
	vpsNALU := append([]byte{0x40, 0x01}, vpsPayload...)
	// SPS: type 33 → first byte = 33<<1 = 0x42
	spsNALU := append([]byte{0x42, 0x01}, spsPayload...)
	// PPS: type 34 → first byte = 34<<1 = 0x44
	ppsNALU := append([]byte{0x44, 0x01}, ppsPayload...)
	// IDR_W_RADL: type 19 → first byte = 19<<1 = 0x26
	idrNALU := []byte{0x26, 0x01, 0xAA, 0xBB}

	var annexB []byte
	for _, nalu := range [][]byte{vpsNALU, spsNALU, ppsNALU, idrNALU} {
		annexB = append(annexB, startCode...)
		annexB = append(annexB, nalu...)
	}
	return annexB
}

func TestPipelineCodecs_HEVC_CodecString(t *testing.T) {
	// When pipeline codec is HEVC, output frame.Codec must be "h265".
	pc := &pipelineCodecs{
		pipelineCodec: CodecHEVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264", // source codec doesn't matter — pipeline codec overrides
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)
	require.Equal(t, "h265", frame.Codec, "HEVC pipeline must set Codec to h265")
}

func TestPipelineCodecs_AVC_CodecString(t *testing.T) {
	// When pipeline codec is AVC (default), output frame.Codec must be "h264".
	pc := &pipelineCodecs{
		pipelineCodec: CodecAVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)
	require.Equal(t, "h264", frame.Codec, "AVC pipeline must set Codec to h264")
}

func TestPipelineCodecs_HEVC_ExtractsVPSSPSPPS(t *testing.T) {
	// HEVC keyframes must extract VPS, SPS, and PPS from the encoded bitstream.
	vpsPayload := []byte{0x01, 0x02, 0x03}
	spsPayload := []byte{0x04, 0x05, 0x06, 0x07}
	ppsPayload := []byte{0x08, 0x09}

	annexBData := buildHEVCAnnexBKeyframe(vpsPayload, spsPayload, ppsPayload)

	pc := &pipelineCodecs{
		pipelineCodec: CodecHEVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: annexBData}, nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h265",
		GroupID:    1,
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)

	// Expected NALUs include the 2-byte HEVC NAL header.
	expectedVPS := append([]byte{0x40, 0x01}, vpsPayload...)
	expectedSPS := append([]byte{0x42, 0x01}, spsPayload...)
	expectedPPS := append([]byte{0x44, 0x01}, ppsPayload...)

	require.Equal(t, expectedVPS, frame.VPS, "HEVC keyframe must have VPS")
	require.Equal(t, expectedSPS, frame.SPS, "HEVC keyframe must have SPS")
	require.Equal(t, expectedPPS, frame.PPS, "HEVC keyframe must have PPS")
}

func TestPipelineCodecs_AVC_NoVPS(t *testing.T) {
	// AVC keyframes must have SPS and PPS but NOT VPS.
	spsPayload := []byte{0x42, 0xC0, 0x1E, 0xD9}
	ppsPayload := []byte{0xCE, 0x3C, 0x80}

	annexBData := buildAnnexBKeyframe(spsPayload, ppsPayload)

	pc := &pipelineCodecs{
		pipelineCodec: CodecAVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: annexBData}, nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    1,
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)

	require.Nil(t, frame.VPS, "AVC keyframe must NOT have VPS")
	require.NotNil(t, frame.SPS, "AVC keyframe must have SPS")
	require.NotNil(t, frame.PPS, "AVC keyframe must have PPS")
}

func TestPipelineCodecs_HEVC_VideoInfoCallback(t *testing.T) {
	// HEVC pipeline must invoke onVideoInfoChange with VPS.
	vpsPayload := []byte{0x01, 0x02, 0x03}
	spsPayload := []byte{0x04, 0x05, 0x06, 0x07}
	ppsPayload := []byte{0x08, 0x09}

	annexBData := buildHEVCAnnexBKeyframe(vpsPayload, spsPayload, ppsPayload)

	var gotVPS, gotSPS, gotPPS []byte
	var gotW, gotH int
	callbackCalled := false

	pc := &pipelineCodecs{
		pipelineCodec: CodecHEVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: annexBData}, nil
		},
		onVideoInfoChange: func(vps, sps, pps []byte, width, height int) {
			callbackCalled = true
			gotVPS = vps
			gotSPS = sps
			gotPPS = pps
			gotW = width
			gotH = height
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h265",
		GroupID:    1,
	}

	_, err := pc.encode(pf, true)
	require.NoError(t, err)

	require.True(t, callbackCalled, "onVideoInfoChange must be called for HEVC keyframe")
	require.NotNil(t, gotVPS, "HEVC callback must receive VPS")
	require.NotNil(t, gotSPS, "HEVC callback must receive SPS")
	require.NotNil(t, gotPPS, "HEVC callback must receive PPS")
	require.Equal(t, 4, gotW)
	require.Equal(t, 4, gotH)
}

func TestPipelineCodecs_AVC_VideoInfoCallbackNilVPS(t *testing.T) {
	// AVC pipeline must invoke onVideoInfoChange with nil VPS.
	spsPayload := []byte{0x42, 0xC0, 0x1E, 0xD9}
	ppsPayload := []byte{0xCE, 0x3C, 0x80}

	annexBData := buildAnnexBKeyframe(spsPayload, ppsPayload)

	var gotVPS []byte
	callbackCalled := false

	pc := &pipelineCodecs{
		pipelineCodec: CodecAVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: annexBData}, nil
		},
		onVideoInfoChange: func(vps, sps, pps []byte, width, height int) {
			callbackCalled = true
			gotVPS = vps
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    1,
	}

	_, err := pc.encode(pf, true)
	require.NoError(t, err)

	require.True(t, callbackCalled, "onVideoInfoChange must be called for AVC keyframe")
	require.Nil(t, gotVPS, "AVC callback must receive nil VPS")
}

func TestPipelineCodecs_HEVC_ParamSetsIndependentOfWireData(t *testing.T) {
	// Regression test: HEVC VPS/SPS/PPS must be independent copies, not
	// sub-slices of the WireData buffer.
	vpsPayload := []byte{0x01, 0x02, 0x03}
	spsPayload := []byte{0x04, 0x05, 0x06, 0x07}
	ppsPayload := []byte{0x08, 0x09}

	annexBData := buildHEVCAnnexBKeyframe(vpsPayload, spsPayload, ppsPayload)

	pc := &pipelineCodecs{
		pipelineCodec: CodecHEVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: annexBData}, nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h265",
		GroupID:    1,
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)

	// Save expected values
	expectedVPS := append([]byte{0x40, 0x01}, vpsPayload...)
	expectedSPS := append([]byte{0x42, 0x01}, spsPayload...)
	expectedPPS := append([]byte{0x44, 0x01}, ppsPayload...)

	// Overwrite WireData to simulate pool recycling
	for i := range frame.WireData {
		frame.WireData[i] = 0xFF
	}

	require.Equal(t, expectedVPS, frame.VPS,
		"VPS corrupted after WireData overwrite — still a sub-slice of the pooled buffer")
	require.Equal(t, expectedSPS, frame.SPS,
		"SPS corrupted after WireData overwrite — still a sub-slice of the pooled buffer")
	require.Equal(t, expectedPPS, frame.PPS,
		"PPS corrupted after WireData overwrite — still a sub-slice of the pooled buffer")
}

func TestPipelineCodecs_DefaultCodecIsAVC(t *testing.T) {
	// When pipelineCodec is zero-value (CodecAVC), output should be H.264.
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)
	require.Equal(t, "h264", frame.Codec, "default pipeline codec must be h264")
}

func TestPipelineCodecs_HEVC_CallbackDeduplication(t *testing.T) {
	// Same VPS/SPS/PPS should not trigger the callback twice.
	vpsPayload := []byte{0x01, 0x02, 0x03}
	spsPayload := []byte{0x04, 0x05, 0x06, 0x07}
	ppsPayload := []byte{0x08, 0x09}

	annexBData := buildHEVCAnnexBKeyframe(vpsPayload, spsPayload, ppsPayload)

	callbackCount := 0
	pc := &pipelineCodecs{
		pipelineCodec: CodecHEVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: annexBData}, nil
		},
		onVideoInfoChange: func(vps, sps, pps []byte, width, height int) {
			callbackCount++
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h265",
		GroupID:    1,
	}

	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 1, callbackCount, "first keyframe should trigger callback")

	// Second keyframe with same params
	pf.PTS = 2000
	_, err = pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 1, callbackCount, "duplicate params should NOT trigger callback again")
}

func TestSetPipelineCodecs_DefaultIsAVC(t *testing.T) {
	// SetPipelineCodecs without explicit codec parameter defaults to AVC.
	sw := New(nil)
	defer sw.Close()

	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})

	require.Equal(t, CodecAVC, sw.pipeCodecs.pipelineCodec, "default pipeline codec should be AVC")
}

func TestSetPipelineCodecs_WithHEVC(t *testing.T) {
	// SetPipelineCodecs with explicit HEVC codec.
	sw := New(nil)
	defer sw.Close()

	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	}, CodecHEVC)

	require.Equal(t, CodecHEVC, sw.pipeCodecs.pipelineCodec, "pipeline codec should be HEVC")
}

// --- Encoder Identification SEI Tests ---

func TestPipelineCodecs_IdentSEI_H264_Keyframe(t *testing.T) {
	// The ident SEI should be injected into H.264 keyframe wire data.
	spsPayload := []byte{0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x6C}
	ppsPayload := []byte{0xCE, 0x3C, 0x80}
	annexBData := buildAnnexBKeyframe(spsPayload, ppsPayload)

	pc := &pipelineCodecs{
		pipelineCodec: CodecAVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: annexBData}, nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    1,
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)
	require.True(t, frame.IsKeyframe)

	// The Switchframe UUID must be present in the wire data.
	// Wire data is AVC1 format (length-prefixed NALUs), so we convert
	// to Annex B to search for the UUID.
	annexB := codec.WireToAnnexB(frame.WireData)
	require.True(t, bytes.Contains(annexB, codec.SwitchframeUUID[:]),
		"keyframe wire data must contain Switchframe ident UUID")

	// The ident string must be present.
	require.True(t, bytes.Contains(annexB, []byte("Switchframe/1.0")),
		"keyframe wire data must contain Switchframe ident string")
}

func TestPipelineCodecs_IdentSEI_HEVC_Keyframe(t *testing.T) {
	// The ident SEI should be injected into HEVC keyframe wire data.
	vpsPayload := []byte{0x01, 0x02, 0x03}
	spsPayload := []byte{0x04, 0x05, 0x06, 0x07}
	ppsPayload := []byte{0x08, 0x09}
	annexBData := buildHEVCAnnexBKeyframe(vpsPayload, spsPayload, ppsPayload)

	pc := &pipelineCodecs{
		pipelineCodec: CodecHEVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: annexBData}, nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h265",
		GroupID:    1,
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)

	annexB := codec.WireToAnnexB(frame.WireData)
	require.True(t, bytes.Contains(annexB, codec.SwitchframeUUID[:]),
		"HEVC keyframe wire data must contain Switchframe ident UUID")
	require.True(t, bytes.Contains(annexB, []byte("Switchframe/1.0")),
		"HEVC keyframe wire data must contain Switchframe ident string")
}

func TestPipelineCodecs_IdentSEI_NonKeyframe_NoIdent(t *testing.T) {
	// Non-keyframes must NOT contain the ident SEI (inject only on keyframes).
	pc := &pipelineCodecs{
		pipelineCodec: CodecAVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	// First, encode a keyframe to prime the encoder and build the ident SEI.
	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    1,
	}
	_, err := pc.encode(pf, true)
	require.NoError(t, err)

	// Now encode a non-keyframe.
	pf2 := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        2000,
		IsKeyframe: false,
		Codec:      "h264",
		GroupID:    1,
	}
	frame, err := pc.encode(pf2, false)
	require.NoError(t, err)
	require.NotNil(t, frame)
	require.False(t, frame.IsKeyframe)

	annexB := codec.WireToAnnexB(frame.WireData)
	require.False(t, bytes.Contains(annexB, codec.SwitchframeUUID[:]),
		"non-keyframe wire data must NOT contain Switchframe ident UUID")
}

func TestPipelineCodecs_IdentSEI_BuiltLazily(t *testing.T) {
	// The ident SEI is built lazily on the first keyframe (once we know
	// the encoder dimensions and format).
	spsPayload := []byte{0x42, 0xC0, 0x1E}
	ppsPayload := []byte{0xCE, 0x3C}
	annexBData := buildAnnexBKeyframe(spsPayload, ppsPayload)

	pc := &pipelineCodecs{
		pipelineCodec: CodecAVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: annexBData}, nil
		},
	}

	// Before any encode, identSEI should be nil.
	require.Nil(t, pc.identSEI, "identSEI should be nil before first keyframe")

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    1,
	}

	_, err := pc.encode(pf, true)
	require.NoError(t, err)

	// After keyframe encode, identSEI should be built.
	require.NotNil(t, pc.identSEI, "identSEI should be built after first keyframe")

	// It should contain the UUID.
	require.True(t, bytes.Contains(pc.identSEI, codec.SwitchframeUUID[:]),
		"identSEI must contain the Switchframe UUID")
}

func TestPipelineCodecs_IdentSEI_InvalidatedOnResolutionChange(t *testing.T) {
	// When the resolution changes, the encoder is recreated and the ident SEI
	// must be rebuilt with the new dimensions.
	spsPayload := []byte{0x42, 0xC0, 0x1E}
	ppsPayload := []byte{0xCE, 0x3C}

	pc := &pipelineCodecs{
		pipelineCodec: CodecAVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: buildAnnexBKeyframe(spsPayload, ppsPayload)}, nil
		},
	}

	// First keyframe at 4x4.
	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    1,
	}
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	firstSEI := append([]byte(nil), pc.identSEI...)

	// Second keyframe at 8x8 — ident SEI should be rebuilt.
	pf2 := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(8, 8)),
		Width:      8,
		Height:     8,
		PTS:        2000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    2,
	}
	_, err = pc.encode(pf2, true)
	require.NoError(t, err)

	// The ident SEI should have been rebuilt (different height in the format string).
	require.NotEqual(t, firstSEI, pc.identSEI,
		"identSEI must be rebuilt when resolution changes")
}

func TestPipelineCodecs_IdentSEI_PreservesParamSets(t *testing.T) {
	// The ident SEI injection must not corrupt SPS/PPS extraction.
	spsPayload := []byte{0x42, 0xC0, 0x1E, 0xD9, 0x00}
	ppsPayload := []byte{0xCE, 0x3C, 0x80}
	annexBData := buildAnnexBKeyframe(spsPayload, ppsPayload)

	pc := &pipelineCodecs{
		pipelineCodec: CodecAVC,
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockAnnexBEncoder{annexB: annexBData}, nil
		},
	}

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    1,
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)

	expectedSPS := append([]byte{0x67}, spsPayload...)
	expectedPPS := append([]byte{0x68}, ppsPayload...)

	require.Equal(t, expectedSPS, frame.SPS,
		"SPS must not be corrupted by ident SEI injection")
	require.Equal(t, expectedPPS, frame.PPS,
		"PPS must not be corrupted by ident SEI injection")
}
