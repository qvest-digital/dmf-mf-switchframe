//go:build correctness

package mux

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
)

const (
	// PIDs used by TSMuxer (must match output/muxer.go constants).
	videoPID uint16 = 0x100
	audioPID uint16 = 0x101
	pmtPID   uint16 = 0x1000
)

// muxFrames encodes numFrames of video (and optionally audio) through the
// TSMuxer and returns the raw concatenated TS bytes. The encoder uses
// libx264 at 320x240. Audio frames are interleaved with video when
// withAudio is true — a synthetic ADTS-wrapped silent AAC frame is
// generated for each video frame.
func muxFrames(t *testing.T, numFrames int, withAudio bool) []byte {
	t.Helper()

	const (
		w       = 320
		h       = 240
		fps     = 30
		bitrate = 500_000
		gopSecs = 2
	)

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	ptsDelta := int64(90000 / fps) // 3000 ticks per frame at 30fps
	startPTS := int64(90000)       // start at 1 second

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	for i := 0; i < numFrames; i++ {
		pts := startPTS + int64(i)*ptsDelta
		forceIDR := (i % (fps * gopSecs)) == 0

		// Vary Y content each frame so P-frames encode with non-trivial data.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if len(encoded) == 0 {
			continue // encoder warmup
		}

		avc1 := codec.AnnexBToAVC1(encoded)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
		}

		if isKeyframe {
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7:
					frame.SPS = append([]byte(nil), nalu...)
				case 8:
					frame.PPS = append([]byte(nil), nalu...)
				}
			}
		}

		require.NoError(t, muxer.WriteVideo(frame), "mux video frame %d", i)

		// Optionally write a silent AAC audio frame alongside each video frame.
		if withAudio {
			// Build a minimal silent AAC frame. A real encoder would produce
			// proper AAC — we just need syntactically valid ADTS so the muxer
			// can write it. Use a 2-byte silence payload; BuildADTS wraps it.
			silentPayload := []byte{0x01, 0x08} // minimal raw AAC-LC silent frame stub
			adtsFrame := codec.BuildADTS(48000, 2, len(silentPayload))
			adtsFrame = append(adtsFrame, silentPayload...)

			aFrame := &media.AudioFrame{
				PTS:        pts,
				Data:       adtsFrame,
				SampleRate: 48000,
				Channels:   2,
			}
			require.NoError(t, muxer.WriteAudio(aFrame), "mux audio frame %d", i)
		}
	}

	return tsData
}

// writeTSToFile writes TS data to a temp file and returns the path.
func writeTSToFile(t *testing.T, tsData []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))
	return path
}

// ---------- Tests ----------

// TestPacketStructure verifies every TS packet is exactly 188 bytes with
// sync byte 0x47.
func TestPacketStructure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	tsData := muxFrames(t, 60, false)
	require.NotEmpty(t, tsData, "muxer should produce output")

	testutil.AssertValidTSStructure(t, tsData)

	// Additionally verify total length is a multiple of 188.
	require.Equal(t, 0, len(tsData)%188,
		"total TS data length %d is not a multiple of 188", len(tsData))
}

// TestPATPresent verifies that PID 0x0000 (PAT) appears within the first
// 7 packets of the output.
func TestPATPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	tsData := muxFrames(t, 60, false)
	require.NotEmpty(t, tsData)

	testutil.AssertPATPresent(t, tsData, 7)
}

// TestPMTPresent verifies that PID 0x1000 (PMT) appears within the first
// 7 packets of the output.
func TestPMTPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	tsData := muxFrames(t, 60, false)
	require.NotEmpty(t, tsData)

	testutil.AssertPMTPresent(t, tsData, pmtPID, 7)
}

// TestPATPMTRepeatInterval muxes 10 seconds of video and checks that
// PAT/PMT packets appear at regular intervals — at least once per GOP.
func TestPATPMTRepeatInterval(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const numFrames = 300 // 10s at 30fps
	tsData := muxFrames(t, numFrames, false)
	require.NotEmpty(t, tsData)

	packets := testutil.ParseTSPackets(t, tsData)

	// Collect packet indices where PAT appears.
	var patIndices []int
	for i, pkt := range packets {
		if pkt.PID == 0x0000 {
			patIndices = append(patIndices, i)
		}
	}
	require.GreaterOrEqual(t, len(patIndices), 2,
		"expected at least 2 PAT packets in 10s of video")

	// Check the maximum gap between consecutive PATs.
	// With go-astits, PAT/PMT are written by WriteTables before each
	// keyframe at minimum (every GOP = 2s). Verify no gap exceeds a
	// reasonable number of packets.
	maxGap := 0
	for i := 1; i < len(patIndices); i++ {
		gap := patIndices[i] - patIndices[i-1]
		if gap > maxGap {
			maxGap = gap
		}
	}
	// At 500kbps + overhead, 2s of data is ~125KB = ~665 packets.
	// Allow up to 2000 packets between PAT entries (very generous).
	require.LessOrEqual(t, maxGap, 2000,
		"PAT repeat gap of %d packets is too large (expected at most every ~2s)", maxGap)
}

// TestPCRAccuracy verifies PCR tracks PTS within ±500us (±45 ticks at 90kHz)
// over 10 seconds of content.
func TestPCRAccuracy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const numFrames = 300
	tsData := muxFrames(t, numFrames, false)
	require.NotEmpty(t, tsData)

	packets := testutil.ParseTSPackets(t, tsData)
	pcrs := testutil.ExtractPCRValues(t, tsData)
	require.NotEmpty(t, pcrs, "should have PCR values in the stream")

	// Extract video PTS from PES headers on the video PID.
	// The muxer sets PCR.Base = muxerPTS, so PCR should exactly match
	// some video PTS.
	var videoPTSValues []int64
	for _, pkt := range packets {
		if pkt.PID != videoPID || !pkt.HasPayload || len(pkt.Payload) < 14 {
			continue
		}
		// PES header starts with 0x00 0x00 0x01
		if pkt.Payload[0] != 0x00 || pkt.Payload[1] != 0x00 || pkt.Payload[2] != 0x01 {
			continue
		}
		ptsDTSFlags := (pkt.Payload[7] >> 6) & 0x03
		if ptsDTSFlags == 0 {
			continue
		}
		// Parse 33-bit PTS from 5 bytes starting at offset 9.
		pts := int64(pkt.Payload[9]&0x0E) << 29
		pts |= int64(pkt.Payload[10]) << 22
		pts |= int64(pkt.Payload[11]&0xFE) << 14
		pts |= int64(pkt.Payload[12]) << 7
		pts |= int64(pkt.Payload[13]) >> 1
		videoPTSValues = append(videoPTSValues, pts)
	}
	require.NotEmpty(t, videoPTSValues, "should have video PTS values")

	// For each PCR, find the nearest video PTS and verify it's within ±45 ticks.
	const toleranceTicks = 45 // ±500µs at 90kHz
	for _, pcr := range pcrs {
		minDiff := int64(1 << 32)
		for _, pts := range videoPTSValues {
			diff := pcr - pts
			if diff < 0 {
				diff = -diff
			}
			if diff < minDiff {
				minDiff = diff
			}
		}
		require.LessOrEqual(t, minDiff, int64(toleranceTicks),
			"PCR %d is more than ±%d ticks from any video PTS (nearest diff=%d)",
			pcr, toleranceTicks, minDiff)
	}
}

// TestPCRInterval verifies PCR is present at least every 100ms (9000 ticks).
func TestPCRInterval(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const numFrames = 300 // 10s
	tsData := muxFrames(t, numFrames, false)
	require.NotEmpty(t, tsData)

	pcrs := testutil.ExtractPCRValues(t, tsData)
	require.GreaterOrEqual(t, len(pcrs), 2, "need at least 2 PCR values")

	// Check gap between consecutive PCR values.
	const maxGapTicks = 9000 // 100ms at 90kHz
	for i := 1; i < len(pcrs); i++ {
		gap := pcrs[i] - pcrs[i-1]
		require.LessOrEqual(t, gap, int64(maxGapTicks),
			"PCR gap between index %d and %d is %d ticks (%.1fms), exceeds 100ms limit",
			i-1, i, gap, float64(gap)/90.0)
	}
}

// TestPCRContinuity verifies PCR values are monotonically increasing.
func TestPCRContinuity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const numFrames = 300
	tsData := muxFrames(t, numFrames, false)
	require.NotEmpty(t, tsData)

	pcrs := testutil.ExtractPCRValues(t, tsData)
	require.GreaterOrEqual(t, len(pcrs), 2, "need at least 2 PCR values")

	testutil.AssertMonotonic(t, pcrs, "PCR")
}

// TestPTSDTSPresent writes the muxed output to a file and verifies via
// ffprobe that every video frame has PTS and DTS values.
func TestPTSDTSPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	tsData := muxFrames(t, 150, false)
	require.NotEmpty(t, tsData)

	path := writeTSToFile(t, tsData)
	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 100,
		"expected at least 100 video frames from ffprobe")

	for i, f := range frames {
		// PTS must be non-zero (our stream starts at PTS = muxerEpoch = 90000).
		require.NotZero(t, f.PTS, "frame %d has zero PTS", i)
		// DTS should also be present and non-negative.
		require.GreaterOrEqual(t, f.DTS, int64(0), "frame %d has negative DTS", i)
	}
}

// TestNullPacketPaddingCBR verifies CBRPacer inserts null packets to achieve
// the target bitrate. We enqueue known real TS data, let the pacer run for
// a fixed duration, and verify metrics consistency.
func TestNullPacketPaddingCBR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Mux a small amount of video to get real TS packets.
	tsData := muxFrames(t, 30, false) // 1s of video
	require.NotEmpty(t, tsData)

	// Target: 2 Mbps = 250,000 bytes/sec.
	const muxrateBps = 2_000_000

	// Use a CBRPacer with a 10ms tick.
	pacer := output.NewCBRPacer(muxrateBps, 10*time.Millisecond)

	var totalOutputBytes int64
	adapters := []output.Adapter{&countingAdapter{onWrite: func(n int) {
		totalOutputBytes += int64(n)
	}}}
	pacer.SetAdapters(&adapters)

	pacer.Start()

	// Feed real TS data.
	pacer.Enqueue(tsData)

	// Let the pacer run for 1 second.
	time.Sleep(1 * time.Second)

	pacer.Stop()

	nullPkts := pacer.NullPacketsTotal()
	padBytes := pacer.PadBytesTotal()
	realBytes := pacer.RealBytesTotal()

	// Verify internal consistency: padBytes == nullPkts * 188.
	require.Equal(t, nullPkts*188, padBytes,
		"pad bytes should equal null packets * 188")

	// Real bytes should include at least the input data.
	require.GreaterOrEqual(t, realBytes, int64(len(tsData)),
		"real bytes should be at least the input size")

	// Total output should be real + padding.
	total := realBytes + padBytes
	require.Greater(t, total, int64(0), "pacer should produce output")

	// The target for 1 second at 2 Mbps is 250,000 bytes.
	// Allow ±20% due to timing imprecision in tests.
	expectedBytes := int64(muxrateBps / 8) // 250,000
	tolerance := float64(expectedBytes) * 0.20
	require.InDelta(t, float64(expectedBytes), float64(total), tolerance,
		"total output %.0f bytes should be within 20%% of target %.0f bytes",
		float64(total), float64(expectedBytes))
}

// TestContinuityCounter verifies CC increments mod 16 per PID with no gaps.
func TestContinuityCounter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	tsData := muxFrames(t, 150, true) // with audio for more PIDs
	require.NotEmpty(t, tsData)

	testutil.AssertContinuityCounters(t, tsData)
}

// TestRandomAccessIndicator verifies RAI is set on packets that start IDR
// video access units.
func TestRandomAccessIndicator(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	tsData := muxFrames(t, 150, false)
	require.NotEmpty(t, tsData)

	packets := testutil.ParseTSPackets(t, tsData)

	// Call the assertion helper which verifies RAI consistency.
	testutil.AssertRandomAccessIndicator(t, packets)

	// Verify that at least one video packet has RAI set (the first keyframe).
	raiCount := 0
	for _, pkt := range packets {
		if pkt.PID == videoPID && pkt.AdaptationField != nil && pkt.AdaptationField.RandomAccessIndicator {
			raiCount++
		}
	}
	require.GreaterOrEqual(t, raiCount, 1,
		"expected at least 1 video packet with RAI set (IDR)")

	// With 150 frames at 30fps and 2s GOP, expect ~3 keyframes.
	require.GreaterOrEqual(t, raiCount, 2,
		"expected at least 2 RAI-marked packets for 150 frames with 2s GOP")
}

// TestAdaptationFieldStuffing verifies all stuffing bytes are 0xFF.
func TestAdaptationFieldStuffing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	tsData := muxFrames(t, 150, true)
	require.NotEmpty(t, tsData)

	packets := testutil.ParseTSPackets(t, tsData)
	testutil.AssertStuffingBytes(t, packets)
}

// TestStreamPlayable writes the muxed output to a temp file and verifies
// ffprobe reports zero errors.
func TestStreamPlayable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	tsData := muxFrames(t, 150, false)
	require.NotEmpty(t, tsData)

	path := writeTSToFile(t, tsData)
	testutil.RequireNoErrors(t, path)

	// Also verify the video stream is detected.
	result := testutil.ProbeFile(t, path)
	require.NotNil(t, result.VideoStream(), "expected video stream")
}

// --- CBR pacer helper ---

// countingAdapter is a minimal output.Adapter that counts bytes written.
type countingAdapter struct {
	onWrite func(int)
}

func (a *countingAdapter) ID() string                    { return "counting" }
func (a *countingAdapter) Start(_ context.Context) error { return nil }
func (a *countingAdapter) Write(data []byte) (int, error) {
	if a.onWrite != nil {
		a.onWrite(len(data))
	}
	return len(data), nil
}
func (a *countingAdapter) Close() error                 { return nil }
func (a *countingAdapter) Status() output.AdapterStatus { return output.AdapterStatus{} }

// ==========================================================================
// Corrupted input handling tests
// ==========================================================================

// TestMuxerRecoverAfterBadFrame feeds 5 valid frames, then one frame with
// empty/nil Data, then 5 more valid frames. The muxer must not panic.
// The valid frames after the bad one should still produce valid TS output.
func TestMuxerRecoverAfterBadFrame(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const (
		w       = 320
		h       = 240
		fps     = 30
		bitrate = 500_000
		gopSecs = 2
	)

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	ptsDelta := int64(90000 / fps)
	startPTS := int64(90000)
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	// Encode and mux a single valid frame helper.
	muxValidFrame := func(i int) {
		pts := startPTS + int64(i)*ptsDelta
		forceIDR := (i % (fps * gopSecs)) == 0

		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if len(encoded) == 0 {
			return
		}

		avc1 := codec.AnnexBToAVC1(encoded)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
		}
		if isKeyframe {
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7:
					frame.SPS = append([]byte(nil), nalu...)
				case 8:
					frame.PPS = append([]byte(nil), nalu...)
				}
			}
		}
		require.NoError(t, muxer.WriteVideo(frame), "mux frame %d", i)
	}

	// Feed 5 valid frames.
	for i := 0; i < 5; i++ {
		muxValidFrame(i)
	}

	bytesBeforeBad := len(tsData)
	require.Greater(t, bytesBeforeBad, 0, "should have TS output from first 5 frames")

	// Feed one bad frame with empty data.
	badPTS := startPTS + int64(5)*ptsDelta
	require.NotPanics(t, func() {
		badFrame := &media.VideoFrame{
			PTS:      badPTS,
			DTS:      badPTS,
			WireData: nil, // nil data
		}
		// WriteVideo should handle gracefully (may skip or error, but not panic).
		_ = muxer.WriteVideo(badFrame)
	}, "muxer should not panic on frame with nil WireData")

	require.NotPanics(t, func() {
		badFrame := &media.VideoFrame{
			PTS:      badPTS + ptsDelta,
			DTS:      badPTS + ptsDelta,
			WireData: []byte{}, // empty data
		}
		_ = muxer.WriteVideo(badFrame)
	}, "muxer should not panic on frame with empty WireData")

	// Feed 5 more valid frames.
	for i := 7; i < 12; i++ {
		muxValidFrame(i)
	}

	bytesAfterRecovery := len(tsData)
	require.Greater(t, bytesAfterRecovery, bytesBeforeBad,
		"muxer should produce more output after recovery from bad frame")

	// Validate TS structure of the output produced after the bad frame.
	// Extract just the post-bad-frame portion.
	postBadData := tsData[bytesBeforeBad:]
	if len(postBadData) > 0 && len(postBadData)%188 == 0 {
		testutil.AssertValidTSStructure(t, postBadData)
	}
}
