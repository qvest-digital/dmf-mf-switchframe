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

// --- test helpers ---

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
