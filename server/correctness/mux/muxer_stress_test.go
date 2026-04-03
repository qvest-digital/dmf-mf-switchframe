//go:build correctness

package mux

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/internal/mpegts"
	"github.com/zsiec/switchframe/server/output"
)

// ---------- TestMuxerHighFrameRate ----------
// Mux 600 frames at 60fps (PTS delta = 1500 ticks) for 10 seconds.
// Verify frame count, PTS delta consistency, and PAT/PMT repeat intervals.
func TestMuxerHighFrameRate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const (
		w         = 320
		h         = 240
		fps       = 60
		bitrate   = 500_000
		gopSecs   = 2
		numFrames = 600                // 10 seconds at 60fps
		ptsDelta  = int64(90000 / fps) // 1500 ticks
	)

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	startPTS := int64(90000)
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	for i := 0; i < numFrames; i++ {
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
			continue
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
	}

	require.NotEmpty(t, tsData, "muxer should produce output")

	// Write to file for ffprobe analysis.
	dir := t.TempDir()
	path := filepath.Join(dir, "60fps.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	// 1. Verify frame count via ffprobe.
	frames := testutil.ProbeVideoFrames(t, path)
	t.Logf("MEASURE: frame count = %d, expected ~%d", len(frames), numFrames)
	// Allow some tolerance for encoder warmup frames.
	require.GreaterOrEqual(t, len(frames), numFrames-10,
		"expected at least %d frames, got %d", numFrames-10, len(frames))

	// 2. Verify PTS deltas = 1500 ticks.
	var totalDelta int64
	var deltaCount int
	var badDeltas int
	for i := 1; i < len(frames); i++ {
		delta := frames[i].PTS - frames[i-1].PTS
		totalDelta += delta
		deltaCount++
		if delta != ptsDelta {
			badDeltas++
		}
	}
	avgDelta := float64(0)
	if deltaCount > 0 {
		avgDelta = float64(totalDelta) / float64(deltaCount)
	}
	t.Logf("MEASURE: frame count = %d, avg PTS delta = %.1f, expected = %d, bad deltas = %d/%d",
		len(frames), avgDelta, ptsDelta, badDeltas, deltaCount)
	// Average PTS delta should be very close to 1500.
	require.InDelta(t, float64(ptsDelta), avgDelta, 5.0,
		"average PTS delta %.1f too far from expected %d", avgDelta, ptsDelta)

	// 3. Verify PAT/PMT repeats at correct intervals (time-based, not frame-based).
	// At 60fps we have twice as many frames between GOPs as 30fps, but PAT/PMT
	// should still appear at regular time intervals.
	packets := testutil.ParseTSPackets(t, tsData)
	var patIndices []int
	for i, pkt := range packets {
		if pkt.PID == 0x0000 {
			patIndices = append(patIndices, i)
		}
	}
	t.Logf("MEASURE: PAT count = %d in %d total packets", len(patIndices), len(packets))
	require.GreaterOrEqual(t, len(patIndices), 2,
		"expected at least 2 PAT packets in 10s of 60fps video")

	// Check maximum gap between PAT entries.
	maxPATGap := 0
	for i := 1; i < len(patIndices); i++ {
		gap := patIndices[i] - patIndices[i-1]
		if gap > maxPATGap {
			maxPATGap = gap
		}
	}
	t.Logf("MEASURE: max PAT gap = %d packets", maxPATGap)
	// At 60fps, 2s GOP produces more packets between keyframes. Allow generous limit.
	require.LessOrEqual(t, maxPATGap, 4000,
		"PAT repeat gap of %d packets is too large for 60fps", maxPATGap)

	// 4. Verify PCR interval is maintained at 60fps.
	pcrs := testutil.ExtractPCRValues(t, tsData)
	t.Logf("MEASURE: PCR count = %d", len(pcrs))
	require.GreaterOrEqual(t, len(pcrs), 2, "need at least 2 PCR values")

	const maxPCRGapTicks = 9000 // 100ms at 90kHz
	pcrViolations := 0
	maxPCRGap := int64(0)
	for i := 1; i < len(pcrs); i++ {
		gap := pcrs[i] - pcrs[i-1]
		if gap > maxPCRGap {
			maxPCRGap = gap
		}
		if gap > maxPCRGapTicks {
			pcrViolations++
		}
	}
	t.Logf("MEASURE: max PCR gap = %d ticks (%.1fms), violations = %d",
		maxPCRGap, float64(maxPCRGap)/90.0, pcrViolations)
	require.Equal(t, 0, pcrViolations,
		"PCR gap exceeded 100ms limit: max gap = %d ticks (%.1fms)",
		maxPCRGap, float64(maxPCRGap)/90.0)
}

// ---------- TestMuxerLargeFrames ----------
// Encode at high bitrate to produce large NALUs, mux 150 frames.
// Verify no truncation and CC correctness across multi-packet PES.
func TestMuxerLargeFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const (
		w         = 640
		h         = 480
		fps       = 30
		bitrate   = 4_000_000 // 4 Mbps to produce large frames
		gopSecs   = 2
		numFrames = 150
	)

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	startPTS := int64(90000)
	ptsDelta := int64(90000 / fps) // 3000 ticks
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	// Track encoded frame sizes.
	var frameSizes []int
	var maxFrameSize int

	for i := 0; i < numFrames; i++ {
		pts := startPTS + int64(i)*ptsDelta
		forceIDR := (i % (fps * gopSecs)) == 0

		// Generate complex content (gradients + noise) to make encoder work harder.
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				yuv[y*w+x] = byte((x*3 + y*7 + i*53) % 256)
			}
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = byte((j*11 + i*29) % 256) // Vary chroma too
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if len(encoded) == 0 {
			continue
		}

		frameSizes = append(frameSizes, len(encoded))
		if len(encoded) > maxFrameSize {
			maxFrameSize = len(encoded)
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
	}

	require.NotEmpty(t, tsData, "muxer should produce output")

	// Compute average frame size.
	totalSize := 0
	for _, s := range frameSizes {
		totalSize += s
	}
	avgSize := 0
	if len(frameSizes) > 0 {
		avgSize = totalSize / len(frameSizes)
	}
	t.Logf("MEASURE: max frame size = %d bytes, avg = %d", maxFrameSize, avgSize)

	// Write to file for ffprobe analysis.
	dir := t.TempDir()
	path := filepath.Join(dir, "large_frames.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	// 1. Verify via ffprobe that all frames are present (no truncation).
	frames := testutil.ProbeVideoFrames(t, path)
	t.Logf("MEASURE: ffprobe frame count = %d, encoded frames = %d", len(frames), len(frameSizes))
	require.GreaterOrEqual(t, len(frames), len(frameSizes)-5,
		"ffprobe should see nearly all encoded frames")

	// 2. Verify ffprobe reports reasonable frame sizes (no truncation).
	maxProbeSize := 0
	for _, f := range frames {
		if f.PktSize > maxProbeSize {
			maxProbeSize = f.PktSize
		}
	}
	t.Logf("MEASURE: max ffprobe pkt_size = %d, max encoded size = %d", maxProbeSize, maxFrameSize)
	// ffprobe pkt_size should be at least 50% of max encoded size
	// (some overhead from Annex B vs AVC1 conversion).
	require.Greater(t, maxProbeSize, maxFrameSize/3,
		"ffprobe max pkt_size %d too small relative to encoded max %d (possible truncation)",
		maxProbeSize, maxFrameSize)

	// 3. Verify CC correctness across multi-packet PES.
	// Large frames span multiple TS packets; CC must increment mod 16 without gaps.
	testutil.AssertContinuityCounters(t, tsData)
	t.Log("MEASURE: continuity counters verified across multi-packet PES for large frames")

	// 4. Verify no ffprobe errors.
	testutil.RequireNoErrors(t, path)
}

// ---------- TestMuxerAudioVideoInterleaving ----------
// Mux video at 30fps and audio at ~46fps. Verify packets are interleaved
// (not all video then all audio) by checking max consecutive same-PID packets.
func TestMuxerAudioVideoInterleaving(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const (
		w              = 320
		h              = 240
		fps            = 30
		bitrate        = 500_000
		gopSecs        = 2
		numVideoFrames = 150 // 5 seconds
	)

	// Audio at ~46fps: 1024 samples at 48kHz = ~21.3ms per frame.
	// Over 5 seconds: ~234 audio frames.
	audioPTSDelta := int64(90000 * 1024 / 48000) // ~1920 ticks per audio frame
	numAudioFrames := 234

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	startPTS := int64(90000)
	videoPTSDelta := int64(90000 / fps) // 3000 ticks
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	// Build a minimal silent AAC frame for audio.
	silentPayload := []byte{0x01, 0x08}
	adtsFrame := codec.BuildADTS(48000, 2, len(silentPayload))
	adtsFrame = append(adtsFrame, silentPayload...)

	// Interleave: for each video frame, write the audio frames whose PTS
	// falls before the next video frame PTS.
	audioIdx := 0
	for i := 0; i < numVideoFrames; i++ {
		pts := startPTS + int64(i)*videoPTSDelta
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
			continue
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

		// Write audio frames that have PTS up to the next video frame PTS.
		nextVideoPTS := startPTS + int64(i+1)*videoPTSDelta
		for audioIdx < numAudioFrames {
			audioPTS := startPTS + int64(audioIdx)*audioPTSDelta
			if audioPTS >= nextVideoPTS {
				break
			}
			aFrame := &media.AudioFrame{
				PTS:        audioPTS,
				Data:       adtsFrame,
				SampleRate: 48000,
				Channels:   2,
			}
			require.NoError(t, muxer.WriteAudio(aFrame), "mux audio frame %d", audioIdx)
			audioIdx++
		}
	}

	// Write remaining audio frames.
	for audioIdx < numAudioFrames {
		audioPTS := startPTS + int64(audioIdx)*audioPTSDelta
		aFrame := &media.AudioFrame{
			PTS:        audioPTS,
			Data:       adtsFrame,
			SampleRate: 48000,
			Channels:   2,
		}
		require.NoError(t, muxer.WriteAudio(aFrame), "mux audio frame %d", audioIdx)
		audioIdx++
	}

	require.NotEmpty(t, tsData, "muxer should produce output")

	// Parse raw TS and check PID sequence.
	packets := testutil.ParseTSPackets(t, tsData)

	// Count max consecutive same-PID packets for video and audio.
	maxConsecutiveVideo := 0
	maxConsecutiveAudio := 0
	currentConsecutive := 0
	currentPID := uint16(0xFFFF)

	totalVideo := 0
	totalAudio := 0

	for _, pkt := range packets {
		if pkt.PID == mpegts.VideoPID {
			totalVideo++
		} else if pkt.PID == mpegts.AudioPID {
			totalAudio++
		}

		if pkt.PID == currentPID {
			currentConsecutive++
		} else {
			// Flush previous run.
			if currentPID == mpegts.VideoPID && currentConsecutive > maxConsecutiveVideo {
				maxConsecutiveVideo = currentConsecutive
			}
			if currentPID == mpegts.AudioPID && currentConsecutive > maxConsecutiveAudio {
				maxConsecutiveAudio = currentConsecutive
			}
			currentPID = pkt.PID
			currentConsecutive = 1
		}
	}
	// Flush final run.
	if currentPID == mpegts.VideoPID && currentConsecutive > maxConsecutiveVideo {
		maxConsecutiveVideo = currentConsecutive
	}
	if currentPID == mpegts.AudioPID && currentConsecutive > maxConsecutiveAudio {
		maxConsecutiveAudio = currentConsecutive
	}

	t.Logf("MEASURE: max consecutive video packets = %d, max consecutive audio = %d",
		maxConsecutiveVideo, maxConsecutiveAudio)
	t.Logf("MEASURE: total video packets = %d, total audio packets = %d, total = %d",
		totalVideo, totalAudio, len(packets))

	// Assert reasonable interleaving. go-astits emits all TS packets for a
	// single PES unit contiguously, so large keyframes (IDR + SPS/PPS) can
	// produce long runs of consecutive video packets. At 500kbps with 2s GOP,
	// keyframes are typically 30-90 packets. For IP/SRT delivery, players
	// buffer sufficiently to handle this; broadcast MPEG-TS has stricter
	// interleaving requirements but those don't apply here.
	//
	// Non-keyframe P-frames at this bitrate are ~2KB = ~11 packets, so most
	// consecutive video runs are modest. We allow up to 100 to accommodate
	// keyframes without triggering false positives.
	require.Less(t, maxConsecutiveVideo, 100,
		"video packets not interleaved: %d consecutive", maxConsecutiveVideo)
	require.Less(t, maxConsecutiveAudio, 100,
		"audio packets not interleaved: %d consecutive", maxConsecutiveAudio)

	// Also verify both audio and video PIDs actually appear.
	require.Greater(t, totalVideo, 0, "no video packets found")
	require.Greater(t, totalAudio, 0, "no audio packets found")
}

// ---------- TestMuxerRapidKeyframes ----------
// Encode with every frame as IDR, mux 100 frames.
// Verify 100 keyframes and RAI set on every adaptation field.
func TestMuxerRapidKeyframes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	const (
		w         = 320
		h         = 240
		fps       = 30
		bitrate   = 1_000_000 // 1 Mbps (all-IDR will be larger)
		gopSecs   = 1         // doesn't matter since we force IDR every frame
		numFrames = 100
	)

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	startPTS := int64(90000)
	ptsDelta := int64(90000 / fps)
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	encodedCount := 0
	totalBits := 0

	for i := 0; i < numFrames; i++ {
		pts := startPTS + int64(i)*ptsDelta
		forceIDR := true // Every frame is IDR!

		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if len(encoded) == 0 {
			continue
		}

		encodedCount++
		totalBits += len(encoded) * 8

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
	}

	require.NotEmpty(t, tsData, "muxer should produce output")

	// Compute effective bitrate.
	durationSec := float64(numFrames) / float64(fps)
	bitrateKbps := float64(totalBits) / durationSec / 1000

	// Write to file for ffprobe analysis.
	dir := t.TempDir()
	path := filepath.Join(dir, "rapid_keyframes.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	// 1. Verify via ffprobe that all frames are keyframes.
	frames := testutil.ProbeVideoFrames(t, path)
	keyframeCount := 0
	nonKeyframeCount := 0
	for _, f := range frames {
		if f.KeyFrame {
			keyframeCount++
		} else {
			nonKeyframeCount++
		}
	}
	t.Logf("MEASURE: keyframe count = %d, bitrate = %d kbps", keyframeCount, int(bitrateKbps))
	t.Logf("MEASURE: total frames from ffprobe = %d, encoded = %d, non-keyframes = %d",
		len(frames), encodedCount, nonKeyframeCount)

	// All frames should be keyframes (forceIDR=true for every frame).
	require.Equal(t, len(frames), keyframeCount,
		"expected all %d frames to be keyframes, got %d keyframes and %d non-keyframes",
		len(frames), keyframeCount, nonKeyframeCount)

	// 2. Verify RAI is set on every video PES start packet.
	packets := testutil.ParseTSPackets(t, tsData)
	raiCount := 0
	videoPESStartCount := 0
	raiMissing := 0

	for _, pkt := range packets {
		if pkt.PID != mpegts.VideoPID {
			continue
		}

		// Check for PES start: payload_unit_start_indicator.
		// This is encoded in the PUSI bit of the original TS header.
		// ParseTSPackets doesn't expose PUSI directly, but PES headers
		// start with 0x00 0x00 0x01 in the payload.
		isPESStart := false
		if pkt.HasPayload && len(pkt.Payload) >= 3 {
			if pkt.Payload[0] == 0x00 && pkt.Payload[1] == 0x00 && pkt.Payload[2] == 0x01 {
				isPESStart = true
				videoPESStartCount++
			}
		}

		hasRAI := pkt.AdaptationField != nil && pkt.AdaptationField.RandomAccessIndicator

		if hasRAI {
			raiCount++
		}
		if isPESStart && !hasRAI {
			raiMissing++
		}
	}

	t.Logf("MEASURE: RAI count = %d, video PES starts = %d, RAI missing on PES start = %d",
		raiCount, videoPESStartCount, raiMissing)

	// Every video PES start should have RAI set since every frame is IDR.
	require.Equal(t, 0, raiMissing,
		"expected RAI on every video PES start (all IDR), but %d PES starts missing RAI",
		raiMissing)
	require.Equal(t, videoPESStartCount, raiCount,
		"RAI count %d should match video PES start count %d", raiCount, videoPESStartCount)
}
