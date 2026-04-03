//go:build correctness

package ingest

import (
	"math/rand"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/internal/mpegts"
)

// splitTSPackets splits raw TS data into 188-byte packet slices.
// Returns nil if the data length is not a multiple of 188.
func splitTSPackets(data []byte) [][]byte {
	if len(data)%mpegts.PacketSize != 0 {
		return nil
	}
	n := len(data) / mpegts.PacketSize
	packets := make([][]byte, n)
	for i := 0; i < n; i++ {
		packets[i] = data[i*mpegts.PacketSize : (i+1)*mpegts.PacketSize]
	}
	return packets
}

// reassembleTS concatenates TS packet slices back into a contiguous byte slice.
func reassembleTS(packets [][]byte) []byte {
	out := make([]byte, 0, len(packets)*mpegts.PacketSize)
	for _, p := range packets {
		out = append(out, p...)
	}
	return out
}

// TestDemuxWithRandomPacketLoss drops 5% of TS packets randomly (preserving PAT PID 0x0000)
// and feeds the damaged stream to the demuxer. The demuxer must not crash.
func TestDemuxWithRandomPacketLoss(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 5, 320, 240, 30)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	packets := splitTSPackets(data)
	require.NotNil(t, packets, "TS file should be a multiple of 188 bytes")
	totalPackets := len(packets)
	t.Logf("MEASURE: total TS packets = %d", totalPackets)

	// Get baseline frame counts from clean stream.
	cleanVideo, cleanAudio, err := clip.DemuxFile(path)
	require.NoError(t, err, "clean DemuxFile should succeed")
	t.Logf("MEASURE: clean stream: %d video frames, %d audio frames", len(cleanVideo), len(cleanAudio))

	// Drop 5% of packets randomly, but PRESERVE PAT (PID 0x0000).
	rng := rand.New(rand.NewSource(42))
	var kept [][]byte
	dropped := 0
	for _, pkt := range packets {
		pid := mpegts.ExtractPID(pkt)
		if pid == 0x0000 {
			// Always keep PAT packets.
			kept = append(kept, pkt)
			continue
		}
		if rng.Float64() < 0.05 {
			dropped++
			continue
		}
		kept = append(kept, pkt)
	}

	t.Logf("MEASURE: %d/%d packets kept, %d dropped (%.1f%%)",
		len(kept), totalPackets, dropped, float64(dropped)*100/float64(totalPackets))

	damagedData := reassembleTS(kept)
	tmpPath := testutil.WriteTempFile(t, damagedData, ".ts")

	// The demuxer must not panic.
	var videoFrames []clip.BufferedFrame
	var audioFrames []clip.BufferedAudioFrame
	var demuxErr error
	require.NotPanics(t, func() {
		videoFrames, audioFrames, demuxErr = clip.DemuxFile(tmpPath)
	}, "demuxer must not panic on 5%% random packet loss")

	if demuxErr != nil {
		t.Logf("MEASURE: demuxer returned error on 5%% loss: %v", demuxErr)
		// Even with errors, we got here without panicking — that's the main check.
		// But we should still get some frames with only 5% loss.
		t.Logf("MEASURE: %d video frames, %d audio frames recovered", len(videoFrames), len(audioFrames))
	} else {
		t.Logf("MEASURE: %d/%d video frames survived (%.1f%%), %d/%d audio frames survived (%.1f%%)",
			len(videoFrames), len(cleanVideo), float64(len(videoFrames))*100/float64(len(cleanVideo)),
			len(audioFrames), len(cleanAudio), float64(len(audioFrames))*100/float64(len(cleanAudio)))

		// With only 5% random loss, we should still get a meaningful number of frames.
		assert.Greater(t, len(videoFrames), 0,
			"should decode at least some video frames with 5%% packet loss")
	}
}

// TestDemuxWithBurstLoss drops ALL packets in the 30%-40% byte range of the stream,
// simulating a ~500ms network outage. The demuxer should produce frames both before
// and after the gap.
func TestDemuxWithBurstLoss(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 5, 320, 240, 30)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	packets := splitTSPackets(data)
	require.NotNil(t, packets, "TS file should be a multiple of 188 bytes")
	totalPackets := len(packets)

	// Drop all packets between packet index 30% and 40%.
	burstStart := totalPackets * 30 / 100
	burstEnd := totalPackets * 40 / 100

	var kept [][]byte
	for i, pkt := range packets {
		if i >= burstStart && i < burstEnd {
			continue // drop
		}
		kept = append(kept, pkt)
	}

	droppedCount := burstEnd - burstStart
	t.Logf("MEASURE: total packets = %d, burst dropped packets %d-%d (%d packets, %.1f%%)",
		totalPackets, burstStart, burstEnd-1, droppedCount, float64(droppedCount)*100/float64(totalPackets))

	damagedData := reassembleTS(kept)
	tmpPath := testutil.WriteTempFile(t, damagedData, ".ts")

	// The demuxer must not panic.
	var videoFrames []clip.BufferedFrame
	var audioFrames []clip.BufferedAudioFrame
	var demuxErr error
	require.NotPanics(t, func() {
		videoFrames, audioFrames, demuxErr = clip.DemuxFile(tmpPath)
	}, "demuxer must not panic on burst packet loss")

	if demuxErr != nil {
		t.Logf("MEASURE: demuxer returned error on burst loss: %v", demuxErr)
		// Log whatever we got.
		t.Logf("MEASURE: frames before+after gap = %d video, %d audio", len(videoFrames), len(audioFrames))
		return
	}

	t.Logf("MEASURE: total recovered: %d video frames, %d audio frames", len(videoFrames), len(audioFrames))

	// The burst removes ~10% of the stream in the middle (30%-40%).
	// At 30fps over 5 seconds, we expect ~150 frames total.
	// The gap is ~500ms = ~15 frames, so we should still get >100 frames.
	// More importantly, we should get frames from BOTH sides of the gap.

	// Compute PTS midpoint: frames from before and after the gap should
	// have PTS values spanning a wide range.
	if len(videoFrames) > 1 {
		firstPTS := videoFrames[0].PTS()
		lastPTS := videoFrames[len(videoFrames)-1].PTS()
		// Total duration in ticks: 5s * 90000 = 450000 ticks.
		totalDuration := lastPTS - firstPTS
		t.Logf("MEASURE: PTS range = %d to %d (span = %d ticks, %.2f seconds)",
			firstPTS, lastPTS, totalDuration, float64(totalDuration)/90000.0)

		// The burst removes 30-40% of the timeline. If we have frames after the gap,
		// the PTS span should be > 2 seconds (covering time before and after the gap).
		assert.Greater(t, float64(totalDuration)/90000.0, 2.0,
			"PTS span should cover >2 seconds, indicating frames from both sides of the gap")

		// Count frames before and after the approximate gap PTS.
		// The gap is at 30-40% of the timeline, so roughly 1.5-2.0 seconds in.
		gapPTS := firstPTS + int64(float64(lastPTS-firstPTS)*0.35)
		framesBefore := 0
		framesAfter := 0
		for _, f := range videoFrames {
			if f.PTS() < gapPTS {
				framesBefore++
			} else {
				framesAfter++
			}
		}
		t.Logf("MEASURE: frames before gap = %d, frames after gap = %d", framesBefore, framesAfter)

		assert.Greater(t, framesBefore, 0, "should have frames before the burst gap")
		assert.Greater(t, framesAfter, 0, "should have frames after the burst gap")
	}

	_ = audioFrames
}

// TestDemuxWithCorruptedPESHeaders flips random bytes in 10% of non-PAT/non-PMT packets.
// The demuxer should not crash and should still recover some frames.
func TestDemuxWithCorruptedPESHeaders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 5, 320, 240, 30)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	// Get clean baseline.
	cleanVideo, cleanAudio, err := clip.DemuxFile(path)
	require.NoError(t, err)
	t.Logf("MEASURE: clean baseline: %d video frames, %d audio frames", len(cleanVideo), len(cleanAudio))

	packets := splitTSPackets(data)
	require.NotNil(t, packets)
	totalPackets := len(packets)

	// Make a copy and corrupt 10% of non-PAT/non-PMT packets by flipping random bytes.
	// PAT = PID 0x0000, PMT is typically PID 0x1000 (4096), but we'll preserve
	// anything below PID 0x0020 to be safe (PAT, CAT, NIT, etc.).
	rng := rand.New(rand.NewSource(99))
	corrupted := make([]byte, len(data))
	copy(corrupted, data)

	corruptedCount := 0
	for i := 0; i < totalPackets; i++ {
		offset := i * mpegts.PacketSize
		pkt := corrupted[offset : offset+mpegts.PacketSize]
		pid := mpegts.ExtractPID(pkt)

		// Preserve PAT and system PIDs.
		if pid < 0x0020 {
			continue
		}

		if rng.Float64() < 0.10 {
			// Flip between 1 and 5 random bytes in the payload area (bytes 4-187).
			numFlips := 1 + rng.Intn(5)
			for f := 0; f < numFlips; f++ {
				bytePos := 4 + rng.Intn(mpegts.PacketSize-4)
				pkt[bytePos] ^= byte(1 + rng.Intn(255))
			}
			corruptedCount++
		}
	}

	t.Logf("MEASURE: corrupted %d/%d packets (%.1f%%)",
		corruptedCount, totalPackets, float64(corruptedCount)*100/float64(totalPackets))

	tmpPath := testutil.WriteTempFile(t, corrupted, ".ts")

	// The demuxer must not panic.
	var videoFrames []clip.BufferedFrame
	var audioFrames []clip.BufferedAudioFrame
	var demuxErr error
	require.NotPanics(t, func() {
		videoFrames, audioFrames, demuxErr = clip.DemuxFile(tmpPath)
	}, "demuxer must not panic on corrupted PES headers")

	if demuxErr != nil {
		t.Logf("MEASURE: demuxer returned error on corrupted stream: %v", demuxErr)
		t.Logf("MEASURE: %d video frames, %d audio frames from corrupted stream", len(videoFrames), len(audioFrames))
	} else {
		t.Logf("MEASURE: %d frames from corrupted stream vs %d from clean (video)",
			len(videoFrames), len(cleanVideo))
		t.Logf("MEASURE: %d frames from corrupted stream vs %d from clean (audio)",
			len(audioFrames), len(cleanAudio))

		// With 10% corruption of non-system packets, we should still get some frames.
		assert.Greater(t, len(videoFrames), 0,
			"should recover at least some video frames from corrupted stream")
	}
}

// TestDemuxWithTruncatedPackets truncates 10% of TS packets to 100 bytes (instead of 188),
// which breaks sync byte alignment for subsequent packets. The demuxer must not crash.
func TestDemuxWithTruncatedPackets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	path := generateFFmpegTS(t, 5, 320, 240, 30)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	packets := splitTSPackets(data)
	require.NotNil(t, packets)
	totalPackets := len(packets)

	// Truncate 10% of packets to 100 bytes. This will break 188-byte alignment
	// for the rest of the stream. The demuxer needs to handle misalignment.
	rng := rand.New(rand.NewSource(77))
	var result []byte
	truncatedCount := 0
	for _, pkt := range packets {
		if rng.Float64() < 0.10 {
			// Truncate to 100 bytes.
			result = append(result, pkt[:100]...)
			truncatedCount++
		} else {
			result = append(result, pkt...)
		}
	}

	t.Logf("MEASURE: %d/%d packets truncated to 100 bytes (%.1f%%), total size %d -> %d",
		truncatedCount, totalPackets, float64(truncatedCount)*100/float64(totalPackets),
		len(data), len(result))
	t.Logf("MEASURE: result is multiple of 188? %v (remainder = %d)",
		len(result)%mpegts.PacketSize == 0, len(result)%mpegts.PacketSize)

	tmpPath := testutil.WriteTempFile(t, result, ".ts")

	// The demuxer must not panic. Given the alignment breakage, it may error entirely.
	var videoFrames []clip.BufferedFrame
	var audioFrames []clip.BufferedAudioFrame
	var demuxErr error
	require.NotPanics(t, func() {
		videoFrames, audioFrames, demuxErr = clip.DemuxFile(tmpPath)
	}, "demuxer must not panic on truncated packets (broken alignment)")

	if demuxErr != nil {
		t.Logf("MEASURE: demuxer returned error (expected with broken alignment): %v", demuxErr)
	} else {
		t.Logf("MEASURE: demuxer succeeded despite truncated packets: %d video, %d audio frames",
			len(videoFrames), len(audioFrames))
	}

	// Either way, we got here without a panic — that's the key invariant.
	t.Logf("MEASURE: no panic — demuxer handled truncated packets gracefully")
}
