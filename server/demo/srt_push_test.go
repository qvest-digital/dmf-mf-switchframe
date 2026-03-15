package demo

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPacingTarget(t *testing.T) {
	anchor := time.Now()
	anchorPTS := int64(0)

	// Frame at PTS=0 should target anchor exactly.
	target0 := pacingTarget(anchor, anchorPTS, 0)
	if !target0.Equal(anchor) {
		t.Errorf("PTS=0 should target anchor exactly, got delta=%v", target0.Sub(anchor))
	}

	// Frame at PTS=90000 (1 second in 90kHz clock) should target anchor + 1s.
	target1s := pacingTarget(anchor, anchorPTS, 90000)
	diff := target1s.Sub(anchor)
	if diff < 990*time.Millisecond || diff > 1010*time.Millisecond {
		t.Errorf("PTS=90000 (1s) gave delta=%v, want ~1s", diff)
	}

	// Frame at PTS=180000 (2 seconds) should target anchor + 2s.
	target2s := pacingTarget(anchor, anchorPTS, 180000)
	diff = target2s.Sub(anchor)
	if diff < 1990*time.Millisecond || diff > 2010*time.Millisecond {
		t.Errorf("PTS=180000 (2s) gave delta=%v, want ~2s", diff)
	}
}

func TestPacingTarget_NonZeroAnchor(t *testing.T) {
	anchor := time.Now()
	anchorPTS := int64(10000)

	// Current PTS = anchor PTS -> should map to anchor time.
	target := pacingTarget(anchor, anchorPTS, 10000)
	if !target.Equal(anchor) {
		t.Errorf("currentPTS == anchorPTS should target anchor, got delta=%v", target.Sub(anchor))
	}

	// 0.5s later in PTS space.
	target500ms := pacingTarget(anchor, anchorPTS, 10000+45000)
	diff := target500ms.Sub(anchor)
	if diff < 490*time.Millisecond || diff > 510*time.Millisecond {
		t.Errorf("45000 ticks (0.5s) gave delta=%v, want ~500ms", diff)
	}
}

func TestEstimateFileDuration(t *testing.T) {
	// Build minimal TS data with two PES video frames:
	// Frame 1 at PTS=0, Frame 2 at PTS=90000 (1 second apart).
	data := buildTestTSWithPTS(t, 0, 90000)

	dur := estimateFileDuration(data)
	if dur <= 0 {
		t.Fatal("estimateFileDuration returned non-positive duration")
	}

	// Expect ~1 second (90000 ticks + 3750 frame padding = 93750 ticks = 1.0416s).
	expected := time.Duration(93750) * time.Second / 90000
	tolerance := 50 * time.Millisecond
	if dur < expected-tolerance || dur > expected+tolerance {
		t.Errorf("estimateFileDuration=%v, want ~%v", dur, expected)
	}
}

func TestEstimateFileDuration_EmptyData(t *testing.T) {
	dur := estimateFileDuration(nil)
	if dur <= 0 {
		t.Error("estimateFileDuration should return a positive fallback for empty data")
	}
}

func TestEstimateFileDuration_SinglePacket(t *testing.T) {
	// A single PTS means no delta can be computed -- should use fallback.
	data := buildTestTSWithSinglePTS(t, 12345)
	dur := estimateFileDuration(data)
	if dur <= 0 {
		t.Error("estimateFileDuration should return positive fallback for single PTS")
	}
}

func TestScanFirstLastPTS(t *testing.T) {
	data := buildTestTSWithPTS(t, 5000, 95000)
	first, last := scanFirstLastPTS(data)
	if first != 5000 {
		t.Errorf("firstPTS=%d, want 5000", first)
	}
	if last != 95000 {
		t.Errorf("lastPTS=%d, want 95000", last)
	}
}

func TestScanFirstLastPTS_Empty(t *testing.T) {
	first, last := scanFirstLastPTS(nil)
	if first != -1 || last != -1 {
		t.Errorf("empty data: firstPTS=%d lastPTS=%d, want -1, -1", first, last)
	}
}

func TestStartSRTSources_ContextCancel(t *testing.T) {
	// Verify that StartSRTSources respects context cancellation
	// without blocking. We don't need a real SRT listener; the dial
	// will fail and retry, and ctx cancel should stop it promptly.
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		// Use an unreachable address so Dial fails fast.
		StartSRTSources(ctx, "127.0.0.1:1", []string{"/nonexistent.ts"}, nil)
		close(done)
	}()

	// Cancel quickly.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Good -- returned promptly.
	case <-time.After(5 * time.Second):
		t.Fatal("StartSRTSources did not stop after context cancel")
	}
}

func TestScanTimestamps_VideoPTS(t *testing.T) {
	data := buildTestTSWithPTS(t, 1000, 91000)
	entries, firstPTS, lastPTS := scanTimestamps(data)

	if len(entries) < 2 {
		t.Fatalf("expected at least 2 timestamp entries, got %d", len(entries))
	}
	if firstPTS != 1000 {
		t.Errorf("firstPTS=%d, want 1000", firstPTS)
	}
	if lastPTS != 91000 {
		t.Errorf("lastPTS=%d, want 91000", lastPTS)
	}
}

func TestAddTimestampOffset(t *testing.T) {
	data := buildTestTSWithPTS(t, 1000, 91000)

	entries, firstBefore, lastBefore := scanTimestamps(data)
	delta := int64(100000) // add ~1.1s

	addTimestampOffset(data, entries, delta)

	// Re-scan to verify.
	_, firstAfter, lastAfter := scanTimestamps(data)
	if firstAfter != firstBefore+delta {
		t.Errorf("after offset: firstPTS=%d, want %d", firstAfter, firstBefore+delta)
	}
	if lastAfter != lastBefore+delta {
		t.Errorf("after offset: lastPTS=%d, want %d", lastAfter, lastBefore+delta)
	}
}

func TestDecodePTS_EncodePTS_Roundtrip(t *testing.T) {
	testCases := []int64{0, 1, 90000, 180000, 1234567890, (1 << 33) - 1}
	for _, pts := range testCases {
		buf := make([]byte, 5)
		// Set prefix nibble (0x20 = PTS-only indicator).
		buf[0] = 0x20
		encodePTS(buf, pts)
		got := decodePTS(buf)
		if got != pts {
			t.Errorf("roundtrip PTS=%d, got %d", pts, got)
		}
	}
}

func TestDecodePCR_EncodePCR_Roundtrip(t *testing.T) {
	testCases := []int64{0, 90000, 1234567890}
	for _, pcr := range testCases {
		buf := make([]byte, 6)
		// Set reserved bits as they'd appear in real data.
		buf[4] = 0x7E
		encodePCR(buf, pcr)
		got := decodePCR(buf)
		if got != pcr {
			t.Errorf("roundtrip PCR=%d, got %d", pcr, got)
		}
	}
}

func TestEstimateFileDuration_RealClip(t *testing.T) {
	// Test with a real clip if available (not required for CI).
	data, err := os.ReadFile("../../test/clips/tears_of_steel.ts")
	if err != nil {
		t.Skip("test clip not available:", err)
	}

	dur := estimateFileDuration(data)
	// Real clips are ~10-15 seconds.
	if dur < 5*time.Second || dur > 30*time.Second {
		t.Errorf("estimateFileDuration for real clip = %v, expected 5-30s", dur)
	}
}

func TestScanTimestamps_RealClip(t *testing.T) {
	data, err := os.ReadFile("../../test/clips/tears_of_steel.ts")
	if err != nil {
		t.Skip("test clip not available:", err)
	}

	entries, firstPTS, lastPTS := scanTimestamps(data)
	if len(entries) == 0 {
		t.Fatal("no timestamp entries found in real clip")
	}
	if firstPTS < 0 {
		t.Error("firstPTS should be >= 0 for real clip")
	}
	if lastPTS <= firstPTS {
		t.Errorf("lastPTS=%d should be > firstPTS=%d", lastPTS, firstPTS)
	}
	t.Logf("real clip: %d timestamp entries, firstPTS=%d, lastPTS=%d, delta=%.3fs",
		len(entries), firstPTS, lastPTS, float64(lastPTS-firstPTS)/90000)
}

func TestStartSRTSources_MultipleClips(t *testing.T) {
	// Verify multiple clips each get their own goroutine and all stop on cancel.
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		StartSRTSources(ctx, "127.0.0.1:1", []string{
			"/a.ts",
			"/b.ts",
			"/c.ts",
		}, nil)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StartSRTSources did not stop after context cancel with multiple clips")
	}
}

func TestAddTimestampOffset_PatchingLatency(t *testing.T) {
	// Verify that patching all timestamp entries at a loop boundary
	// completes in a bounded time (< 5ms for typical files).
	// Build a large synthetic TS stream with many PTS entries.
	const numPackets = 5000 // ~940KB, simulating a moderate file
	data := make([]byte, 0, numPackets*tsPacketLen)
	for i := 0; i < numPackets; i++ {
		pts := int64(i) * 3750 // one frame at 24fps per packet
		pkt := buildTSPacketWithPTS(0xE0, pts)
		data = append(data, pkt...)
	}

	entries, _, _ := scanTimestamps(data)
	if len(entries) < numPackets {
		t.Fatalf("expected >= %d entries, got %d", numPackets, len(entries))
	}

	delta := int64(90000) // 1 second offset

	start := time.Now()
	addTimestampOffset(data, entries, delta)
	elapsed := time.Since(start)

	// Patching should be fast — well under 5ms even for thousands of entries.
	if elapsed > 5*time.Millisecond {
		t.Errorf("addTimestampOffset took %v for %d entries, want < 5ms", elapsed, len(entries))
	}

	// Verify correctness: first PTS should be delta (was 0).
	_, firstPTS, _ := scanTimestamps(data)
	if firstPTS != delta {
		t.Errorf("after offset: firstPTS=%d, want %d", firstPTS, delta)
	}
}

func TestConcurrentDataBufferIsolation(t *testing.T) {
	// Verify that independent data buffers (as used by concurrent pushFile
	// goroutines) don't interfere with each other when timestamps are patched.
	// Each goroutine gets its own os.ReadFile copy; this test simulates that.
	basePTS1 := int64(1000)
	basePTS2 := int64(91000)

	// Create two independent copies of the same TS data (simulating two
	// os.ReadFile calls on the same file from different goroutines).
	template := buildTestTSWithPTS(t, basePTS1, basePTS2)
	buf1 := make([]byte, len(template))
	buf2 := make([]byte, len(template))
	copy(buf1, template)
	copy(buf2, template)

	entries1, _, _ := scanTimestamps(buf1)
	entries2, _, _ := scanTimestamps(buf2)

	delta1 := int64(100000)
	delta2 := int64(200000)

	// Patch both concurrently.
	done := make(chan struct{})
	go func() {
		addTimestampOffset(buf1, entries1, delta1)
		close(done)
	}()
	addTimestampOffset(buf2, entries2, delta2)
	<-done

	// Verify each buffer was patched independently.
	_, first1, last1 := scanTimestamps(buf1)
	_, first2, last2 := scanTimestamps(buf2)

	if first1 != basePTS1+delta1 {
		t.Errorf("buf1 firstPTS=%d, want %d", first1, basePTS1+delta1)
	}
	if last1 != basePTS2+delta1 {
		t.Errorf("buf1 lastPTS=%d, want %d", last1, basePTS2+delta1)
	}
	if first2 != basePTS1+delta2 {
		t.Errorf("buf2 firstPTS=%d, want %d", first2, basePTS1+delta2)
	}
	if last2 != basePTS2+delta2 {
		t.Errorf("buf2 lastPTS=%d, want %d", last2, basePTS2+delta2)
	}
}

func TestFindVideoPID(t *testing.T) {
	// Build TS with video on PID 0x100 and audio on PID 0x101.
	data := buildTestTSWithPTS(t, 1000, 91000)
	pid := findVideoPID(data)
	if pid != 0x100 {
		t.Errorf("findVideoPID=%d, want 0x100", pid)
	}
}

func TestFindVideoPID_NoVideo(t *testing.T) {
	// Build TS with audio-only packets (stream ID 0xC0).
	pkt := buildTSPacketWithPTS(0xC0, 1000)
	pid := findVideoPID(pkt)
	if pid != -1 {
		t.Errorf("findVideoPID for audio-only=%d, want -1", pid)
	}
}

func TestParseAccessUnits_VideoFrameAligned(t *testing.T) {
	// Build a TS stream: [video PES frame1] [audio PES] [video PES frame2] [audio PES]
	var data []byte
	// Video frame 1 (PTS=0): 3 packets (PUSI + 2 continuation)
	data = append(data, buildTSPacketWithPTS(0xE0, 0)...)      // video PUSI
	data = append(data, buildTSContinuation(0x100)...)          // video cont
	data = append(data, buildTSContinuation(0x100)...)          // video cont
	// Audio (PTS=500): 1 packet
	data = append(data, buildTSPacketWithPTSOnPID(0xC0, 500, 0x101)...)
	// Video frame 2 (PTS=3750): 2 packets
	data = append(data, buildTSPacketWithPTS(0xE0, 3750)...)    // video PUSI
	data = append(data, buildTSContinuation(0x100)...)          // video cont
	// Audio (PTS=4250): 1 packet
	data = append(data, buildTSPacketWithPTSOnPID(0xC0, 4250, 0x101)...)

	units := parseAccessUnits(data)

	// Should have 2 access units (one per video frame).
	if len(units) != 2 {
		t.Fatalf("got %d access units, want 2", len(units))
	}

	// Unit 1: video frame 1 (3 video pkts + 1 audio pkt = 4 pkts).
	if units[0].pts != 0 {
		t.Errorf("unit[0].pts=%d, want 0", units[0].pts)
	}
	pktCount0 := (units[0].end - units[0].start) / tsPacketLen
	if pktCount0 != 4 {
		t.Errorf("unit[0] has %d packets, want 4", pktCount0)
	}

	// Unit 2: video frame 2 (2 video pkts + 1 audio pkt = 3 pkts).
	if units[1].pts != 3750 {
		t.Errorf("unit[1].pts=%d, want 3750", units[1].pts)
	}
	pktCount1 := (units[1].end - units[1].start) / tsPacketLen
	if pktCount1 != 3 {
		t.Errorf("unit[1] has %d packets, want 3", pktCount1)
	}
}

func TestParseAccessUnits_PreambleMerge(t *testing.T) {
	// PAT/PMT before first video PES should be merged into first video unit.
	var data []byte
	data = append(data, buildTSContinuation(0x00)...)      // PAT (PID 0 — skipped in preamble, becomes preamble)
	data = append(data, buildTSPacketWithPTS(0xE0, 1000)...) // video PUSI

	units := parseAccessUnits(data)

	// Preamble PAT has PID=0, which is skipped by findVideoPID, but
	// parseAccessUnits handles it. The PAT packet at PID 0 would be caught
	// by the else-if current != nil branch. Let's verify we get 1 unit
	// with the video PTS.
	if len(units) != 1 {
		t.Fatalf("got %d units, want 1 (preamble merged)", len(units))
	}
	if units[0].pts != 1000 {
		t.Errorf("unit[0].pts=%d, want 1000", units[0].pts)
	}
}

func TestParseAccessUnits_RealClip(t *testing.T) {
	data, err := os.ReadFile("../../test/clips/tears_of_steel.ts")
	if err != nil {
		t.Skip("test clip not available:", err)
	}

	units := parseAccessUnits(data)
	if len(units) == 0 {
		t.Fatal("no access units found")
	}

	// tears_of_steel is ~15s at 24fps = ~360 video frames.
	if len(units) < 300 || len(units) > 400 {
		t.Errorf("got %d access units for tears_of_steel, expected ~360", len(units))
	}

	// All units should have valid PTS.
	for i, u := range units {
		if u.pts < 0 {
			t.Errorf("unit[%d] has pts=%d, want >= 0", i, u.pts)
		}
		if u.end <= u.start {
			t.Errorf("unit[%d] has invalid range: start=%d end=%d", i, u.start, u.end)
		}
	}

	// DTS values (used for pacing) should be monotonically increasing.
	// With B-frames, PTS can go backward, but our extractPESDTS returns
	// DTS when available, which is always monotonic.
	for i := 1; i < len(units); i++ {
		if units[i].pts < units[i-1].pts {
			t.Errorf("unit[%d].dts=%d < unit[%d].dts=%d (not monotonic)",
				i, units[i].pts, i-1, units[i-1].pts)
			break
		}
	}

	t.Logf("tears_of_steel: %d access units, first PTS=%d, last PTS=%d",
		len(units), units[0].pts, units[len(units)-1].pts)
}

// --- test helpers ---

// buildTSContinuation creates a TS continuation packet (no PUSI, no PES header).
func buildTSContinuation(pid int) []byte {
	pkt := make([]byte, tsPacketSize)
	pkt[0] = 0x47
	pkt[1] = byte((pid >> 8) & 0x1F) // no PUSI
	pkt[2] = byte(pid & 0xFF)
	pkt[3] = 0x10 // payload only
	return pkt
}

// buildTSPacketWithPTSOnPID constructs a TS packet with a PES header on a specific PID.
func buildTSPacketWithPTSOnPID(streamID byte, pts int64, pid int) []byte {
	pkt := buildTSPacketWithPTS(streamID, pts)
	// Override PID (bytes 1-2, preserving PUSI bit).
	pkt[1] = 0x40 | byte((pid>>8)&0x1F) // PUSI=1 + PID high
	pkt[2] = byte(pid & 0xFF)            // PID low
	return pkt
}

const tsPacketSize = 188

// buildTestTSWithPTS creates minimal MPEG-TS data containing two
// video PES packets with the given PTS values (90kHz clock).
func buildTestTSWithPTS(t *testing.T, pts1, pts2 int64) []byte {
	t.Helper()
	pkt1 := buildTSPacketWithPTS(0xE0, pts1) // video stream ID
	pkt2 := buildTSPacketWithPTS(0xE0, pts2)
	return append(pkt1, pkt2...)
}

// buildTestTSWithSinglePTS creates a single-packet TS with one video PTS.
func buildTestTSWithSinglePTS(t *testing.T, pts int64) []byte {
	t.Helper()
	return buildTSPacketWithPTS(0xE0, pts)
}

// buildTSPacketWithPTS constructs a single 188-byte TS packet containing
// a minimal PES header with the given stream ID and PTS.
func buildTSPacketWithPTS(streamID byte, pts int64) []byte {
	pkt := make([]byte, tsPacketSize)
	pkt[0] = 0x47 // sync byte
	// PID = 0x100 (arbitrary video PID), PUSI = 1
	pkt[1] = 0x41 // TEI=0, PUSI=1, priority=0, PID high=0x01
	pkt[2] = 0x00 // PID low=0x00
	pkt[3] = 0x10 // no adaptation, payload only, CC=0

	// PES header
	off := 4
	pkt[off] = 0x00   // packet start code prefix
	pkt[off+1] = 0x00 //
	pkt[off+2] = 0x01 //
	pkt[off+3] = streamID
	pkt[off+4] = 0x00 // PES packet length (0 = unbounded)
	pkt[off+5] = 0x00
	pkt[off+6] = 0x80 // marker bits
	pkt[off+7] = 0x80 // PTS flag set, no DTS
	pkt[off+8] = 0x05 // PES header data length = 5 (PTS only)

	// Encode PTS (5 bytes, same format as MPEG PES)
	pkt[off+9] = byte(0x20|((pts>>29)&0x0E)) | 0x01
	pkt[off+10] = byte(pts >> 22)
	pkt[off+11] = byte(((pts>>14)&0xFE) | 0x01)
	pkt[off+12] = byte(pts >> 7)
	pkt[off+13] = byte(((pts << 1) & 0xFE) | 0x01)

	return pkt
}
