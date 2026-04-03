package output

import (
	"testing"

	scte35lib "github.com/Comcast/scte35-go/pkg/scte35"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal/mpegts"
)

func TestTSMuxer_WriteSCTE35_BeforeInit(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35PID(mpegts.SCTE35PID)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	data := encodeSpliceNull(t)
	if err := m.WriteSCTE35(data); err != nil {
		t.Fatalf("WriteSCTE35 before init: %v", err)
	}

	// No output yet (not initialized - needs keyframe first)
	if len(output) > 0 {
		t.Fatal("expected no output before init")
	}
}

func TestTSMuxer_WriteSCTE35_AfterInit(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35PID(mpegts.SCTE35PID)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	// Init with a keyframe
	writeInitKeyframe(t, m)

	data := encodeSpliceNull(t)
	if err := m.WriteSCTE35(data); err != nil {
		t.Fatalf("WriteSCTE35 after init: %v", err)
	}

	// SCTE-35 is now IDR-aligned: buffered until next keyframe.
	// Write a keyframe to flush it.
	idr := &media.VideoFrame{
		PTS: 96000, DTS: 96000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	_ = m.WriteVideo(idr)

	if len(output) == 0 {
		t.Fatal("expected TS output after WriteSCTE35 + keyframe flush")
	}
}

func TestTSMuxer_WriteSCTE35_PID(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35PID(mpegts.SCTE35PID)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	writeInitKeyframe(t, m)
	// Clear output from keyframe init
	output = nil

	data := encodeSpliceNull(t)
	_ = m.WriteSCTE35(data)

	// SCTE-35 is IDR-aligned: write a keyframe to flush.
	idr := &media.VideoFrame{
		PTS: 96000, DTS: 96000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	_ = m.WriteVideo(idr)

	// Scan TS packets for PID 0x102
	found := false
	for i := 0; i+188 <= len(output); i += 188 {
		pkt := output[i : i+188]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid == 0x102 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no TS packet with PID 0x102 found")
	}
}

func TestTSMuxer_SCTE35_Disabled(t *testing.T) {
	m := NewTSMuxer()
	// scte35 NOT enabled
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	writeInitKeyframe(t, m)

	data := encodeSpliceNull(t)
	_ = m.WriteSCTE35(data)

	// Should silently discard when disabled
	for i := 0; i+188 <= len(output); i += 188 {
		pkt := output[i : i+188]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid == 0x102 {
			t.Fatal("SCTE-35 PID found but feature is disabled")
		}
	}
}

func TestTSMuxer_CurrentPTS(t *testing.T) {
	m := NewTSMuxer()
	m.SetOutput(func(data []byte) {})

	if m.CurrentPTS() != 0 {
		t.Fatal("expected 0 PTS before any writes")
	}

	writeInitKeyframe(t, m)

	if m.CurrentPTS() == 0 {
		t.Fatal("expected non-zero PTS after keyframe")
	}
}

func TestTSMuxer_WriteSCTE35_ContinuityCounter(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35PID(mpegts.SCTE35PID)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	writeInitKeyframe(t, m)

	data := encodeSpliceNull(t)

	// Write two SCTE-35 sections — CC should increment.
	_ = m.WriteSCTE35(data)
	_ = m.WriteSCTE35(data)

	// Flush both by writing a keyframe.
	output = nil
	idr := &media.VideoFrame{
		PTS: 96000, DTS: 96000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	_ = m.WriteVideo(idr)

	// Find the second SCTE-35 packet and check CC = 1.
	scte35Count := 0
	for i := 0; i+188 <= len(output); i += 188 {
		pkt := output[i : i+188]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid == 0x102 {
			scte35Count++
			if scte35Count == 2 {
				cc := pkt[3] & 0x0F
				if cc != 1 {
					t.Fatalf("expected continuity counter 1, got %d", cc)
				}
				return
			}
		}
	}
	t.Fatalf("expected at least 2 SCTE-35 packets, got %d", scte35Count)
}

func TestTSMuxer_WriteSCTE35_PendingFlushed(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35PID(mpegts.SCTE35PID)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	// Write SCTE-35 before init (should be buffered)
	data := encodeSpliceNull(t)
	_ = m.WriteSCTE35(data)

	// Init with keyframe — pending SCTE-35 should be flushed
	writeInitKeyframe(t, m)

	// Scan for SCTE-35 PID in output
	found := false
	for i := 0; i+188 <= len(output); i += 188 {
		pkt := output[i : i+188]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid == 0x102 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("pending SCTE-35 was not flushed after init")
	}
}

func TestTSMuxer_WriteSCTE35_CustomPID(t *testing.T) {
	customPID := uint16(0x1FF)
	m := NewTSMuxer()
	m.SetSCTE35PID(customPID)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	writeInitKeyframe(t, m)
	output = nil

	data := encodeSpliceNull(t)
	_ = m.WriteSCTE35(data)

	// SCTE-35 is IDR-aligned: write a keyframe to flush.
	idr := &media.VideoFrame{
		PTS: 96000, DTS: 96000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	_ = m.WriteVideo(idr)

	// Scan TS packets for the custom PID
	found := false
	for i := 0; i+188 <= len(output); i += 188 {
		pkt := output[i : i+188]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid == customPID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no TS packet with custom PID 0x%X found", customPID)
	}
}

func TestTSMuxer_WriteSCTE35_IDRAligned_FlushedOnKeyframe(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35PID(mpegts.SCTE35PID)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	// Init with a keyframe.
	writeInitKeyframe(t, m)
	output = nil

	// Write a SCTE-35 section — it should be buffered, not flushed.
	data := encodeSpliceNull(t)
	if err := m.WriteSCTE35(data); err != nil {
		t.Fatalf("WriteSCTE35: %v", err)
	}
	if len(output) > 0 {
		t.Fatal("expected SCTE-35 to be buffered, not flushed immediately")
	}

	// Write a non-keyframe — SCTE-35 should still be buffered.
	nonIDR := &media.VideoFrame{
		PTS: 93000, DTS: 93000, IsKeyframe: false,
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x61, 0x88, 0x80, 0x40},
		Codec:    "h264",
	}
	_ = m.WriteVideo(nonIDR)
	// Check there are no SCTE-35 PID packets in the output.
	if countSCTE35Packets(output, mpegts.SCTE35PID) > 0 {
		t.Fatal("SCTE-35 should not flush on non-keyframe")
	}

	// Write a keyframe — SCTE-35 should be flushed.
	output = nil
	idr := &media.VideoFrame{
		PTS: 96000, DTS: 96000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	_ = m.WriteVideo(idr)
	if countSCTE35Packets(output, mpegts.SCTE35PID) == 0 {
		t.Fatal("SCTE-35 should flush on keyframe")
	}

	// Verify SCTE-35 packets appear before video packets in the output.
	firstSCTE35 := -1
	firstVideo := -1
	for i := 0; i+mpegts.PacketSize <= len(output); i += mpegts.PacketSize {
		pkt := output[i : i+mpegts.PacketSize]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid == mpegts.SCTE35PID && firstSCTE35 < 0 {
			firstSCTE35 = i
		}
		if pid == mpegts.VideoPID && firstVideo < 0 {
			firstVideo = i
		}
	}
	if firstSCTE35 < 0 {
		t.Fatal("no SCTE-35 packet found in output")
	}
	if firstVideo < 0 {
		t.Fatal("no video packet found in output")
	}
	if firstSCTE35 >= firstVideo {
		t.Fatalf("SCTE-35 packet (offset %d) should appear before video (offset %d)", firstSCTE35, firstVideo)
	}
}

func TestTSMuxer_WriteSCTE35_IDRAligned_TimeoutFlush(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35PID(mpegts.SCTE35PID)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	// Init with a keyframe.
	writeInitKeyframe(t, m)
	output = nil

	// Write a SCTE-35 section.
	data := encodeSpliceNull(t)
	_ = m.WriteSCTE35(data)

	// Write enough non-keyframes to trigger the timeout (idrAlignTimeoutFrames).
	for i := 0; i < idrAlignTimeoutFrames; i++ {
		nonIDR := &media.VideoFrame{
			PTS: int64(93000 + i*3000), DTS: int64(93000 + i*3000), IsKeyframe: false,
			WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x61, 0x88, 0x80, 0x40},
			Codec:    "h264",
		}
		_ = m.WriteVideo(nonIDR)
	}

	// The SCTE-35 section should have been flushed by the timeout.
	if countSCTE35Packets(output, mpegts.SCTE35PID) == 0 {
		t.Fatal("SCTE-35 should flush after timeout frames even without keyframe")
	}
}

func TestTSMuxer_WriteSCTE35_IDRAligned_NoPendingNoFlush(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35PID(mpegts.SCTE35PID)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	// Init with a keyframe.
	writeInitKeyframe(t, m)
	output = nil

	// Write a keyframe without any pending SCTE-35 — no SCTE-35 packets.
	idr := &media.VideoFrame{
		PTS: 96000, DTS: 96000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	_ = m.WriteVideo(idr)

	if countSCTE35Packets(output, mpegts.SCTE35PID) > 0 {
		t.Fatal("no SCTE-35 packets expected when none are pending")
	}
}

func TestTSMuxer_WriteSCTE35_IDRAligned_MultipleSections(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35PID(mpegts.SCTE35PID)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	// Init with a keyframe.
	writeInitKeyframe(t, m)
	output = nil

	// Write two SCTE-35 sections.
	data := encodeSpliceNull(t)
	_ = m.WriteSCTE35(data)
	_ = m.WriteSCTE35(data)

	// Write a keyframe — both should flush.
	idr := &media.VideoFrame{
		PTS: 96000, DTS: 96000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	_ = m.WriteVideo(idr)

	count := countSCTE35Packets(output, mpegts.SCTE35PID)
	if count < 2 {
		t.Fatalf("expected at least 2 SCTE-35 packets (one per section), got %d", count)
	}
}

// countSCTE35Packets counts TS packets with the given SCTE-35 PID.
func countSCTE35Packets(output []byte, pid uint16) int {
	count := 0
	for i := 0; i+mpegts.PacketSize <= len(output); i += mpegts.PacketSize {
		pkt := output[i : i+mpegts.PacketSize]
		if pkt[0] != 0x47 {
			continue
		}
		pktPID := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pktPID == pid {
			count++
		}
	}
	return count
}

// helpers

func encodeSpliceNull(t *testing.T) []byte {
	t.Helper()
	sis := scte35lib.SpliceInfoSection{
		SpliceCommand: &scte35lib.SpliceNull{},
		Tier:          4095,
		SAPType:       scte35lib.SAPTypeNotSpecified,
	}
	data, err := sis.Encode()
	if err != nil {
		t.Fatalf("encode splice_null: %v", err)
	}
	return data
}

// writeInitKeyframe writes a synthetic H.264 keyframe to trigger muxer init.
// Uses the same pattern as existing muxer_test.go tests.
func writeInitKeyframe(t *testing.T, m *TSMuxer) {
	t.Helper()
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	if err := m.WriteVideo(idrFrame); err != nil {
		t.Fatalf("writeInitKeyframe: %v", err)
	}
}
