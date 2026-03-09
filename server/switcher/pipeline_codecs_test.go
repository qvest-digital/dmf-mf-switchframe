package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
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
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 1, encoderCreateCount)
	require.Equal(t, 4, pc.encWidth)
	require.Equal(t, 4, pc.encHeight)

	// Encode at 8x8 -- encoder should be recreated
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

func TestPipelineCodecs_Close(t *testing.T) {
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	// Init encoder via an encode call
	pf := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4,
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
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4,
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
			YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4,
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

func TestPipelineCodecs_ForwardPTSJumpCapped(t *testing.T) {
	// When switching sources, the new source may have a much larger PTS origin.
	// A large forward PTS jump causes downstream MPEG-TS decoders to stall
	// while "buffering" the gap. The enforcer must cap forward jumps.
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	mkFrame := func(pts int64) *ProcessingFrame {
		return &ProcessingFrame{
			YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4,
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

	// Simulate source switch: new source has PTS 100 million ticks ahead
	f3, err := pc.encode(mkFrame(100_000_000), true)
	require.NoError(t, err)
	require.NotNil(t, f3)

	// Should be capped to lastOutputPTS + frameDur, NOT 100_000_000
	expectedPTS := f2.PTS + frameDur
	require.Equal(t, expectedPTS, f3.PTS,
		"large forward PTS jump should be capped to one frame duration")

	// Subsequent frames should continue from the capped value
	f4, err := pc.encode(mkFrame(100_000_000+frameDur), true)
	require.NoError(t, err)
	require.Equal(t, expectedPTS+frameDur, f4.PTS,
		"next frame should continue from capped PTS")
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
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4,
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
		YUV: make([]byte, 1280*720*3/2), Width: 1280, Height: 720,
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
		YUV: make([]byte, 1280*720*3/2), Width: 1280, Height: 720,
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
		YUV:        make([]byte, 4*4*3/2),
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

func BenchmarkPipelineEncode(b *testing.B) {
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
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
