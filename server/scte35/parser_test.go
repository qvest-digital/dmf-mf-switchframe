package scte35

import (
	"encoding/binary"
	"testing"
	"time"
)

// wrapInTS wraps SCTE-35 section data into one or more 188-byte MPEG-TS packets
// on the given PID. Each packet has:
//   - Byte 0: 0x47 sync byte
//   - Byte 1-2: PID with payload_unit_start_indicator=1 (first packet)
//   - Byte 3: adaptation_field_control=01 (payload only), continuity_counter
//   - Byte 4: pointer_field=0 (section starts immediately, first packet only)
//   - Remaining: section data, padded with 0xFF
func wrapInTS(pid uint16, sectionData []byte) []byte {
	const packetSize = 188

	var result []byte
	remaining := sectionData
	cc := uint8(0)
	first := true

	for len(remaining) > 0 || first {
		pkt := make([]byte, packetSize)
		// Sync byte
		pkt[0] = 0x47

		// PID and flags
		pidHigh := byte((pid >> 8) & 0x1F)
		pidLow := byte(pid & 0xFF)
		if first {
			pidHigh |= 0x40 // payload_unit_start_indicator
		}
		pkt[1] = pidHigh
		pkt[2] = pidLow

		// Adaptation field control = 01 (payload only), CC
		pkt[3] = 0x10 | (cc & 0x0F)
		cc++

		payloadStart := 4
		if first {
			// Pointer field = 0
			pkt[4] = 0x00
			payloadStart = 5
			first = false
		}

		payloadLen := packetSize - payloadStart
		n := len(remaining)
		if n > payloadLen {
			n = payloadLen
		}
		copy(pkt[payloadStart:], remaining[:n])
		remaining = remaining[n:]

		// Pad remainder with 0xFF
		for i := payloadStart + n; i < packetSize; i++ {
			pkt[i] = 0xFF
		}

		result = append(result, pkt...)
	}

	return result
}

// buildMinimalPMT builds a minimal PMT section (without TS wrapping) that
// contains the given elementary streams.
func buildMinimalPMT(streams []pmtStream) []byte {
	// PMT structure:
	// table_id: 0x02 (1 byte)
	// section_syntax_indicator(1) + '0'(1) + reserved(2) + section_length(12) = 2 bytes
	// program_number: 2 bytes
	// reserved(2) + version_number(5) + current_next_indicator(1) = 1 byte
	// section_number: 1 byte
	// last_section_number: 1 byte
	// reserved(3) + PCR_PID(13) = 2 bytes
	// reserved(4) + program_info_length(12) = 2 bytes
	// [program descriptors - 0 bytes in our case]
	// For each stream:
	//   stream_type: 1 byte
	//   reserved(3) + elementary_PID(13) = 2 bytes
	//   reserved(4) + ES_info_length(12) = 2 bytes
	//   [ES descriptors - 0 bytes in our case]
	// CRC_32: 4 bytes

	headerLen := 9 // program_number(2) + flags(1) + section_number(1) + last_section_number(1) + PCR_PID(2) + program_info_length(2)
	streamDataLen := len(streams) * 5
	sectionLen := headerLen + streamDataLen + 4 // +4 for CRC

	buf := make([]byte, 3+sectionLen)

	// table_id
	buf[0] = 0x02

	// section_syntax_indicator=1, '0'=0, reserved=11, section_length
	buf[1] = 0xB0 | byte((sectionLen>>8)&0x0F) // 1011 + length high
	buf[2] = byte(sectionLen & 0xFF)

	// program_number = 1
	buf[3] = 0x00
	buf[4] = 0x01

	// reserved(2) + version_number(5) + current_next_indicator(1)
	buf[5] = 0xC1 // 11 00000 1

	// section_number = 0
	buf[6] = 0x00

	// last_section_number = 0
	buf[7] = 0x00

	// reserved(3) + PCR_PID(13) = 0x100 (video PID)
	buf[8] = 0xE1 // 111 + PID high
	buf[9] = 0x00 // PID low

	// reserved(4) + program_info_length(12) = 0
	buf[10] = 0xF0
	buf[11] = 0x00

	offset := 12
	for _, s := range streams {
		buf[offset] = s.streamType
		buf[offset+1] = 0xE0 | byte((s.pid>>8)&0x1F) // reserved(3) + PID high
		buf[offset+2] = byte(s.pid & 0xFF)
		buf[offset+3] = 0xF0 // reserved(4) + ES_info_length high
		buf[offset+4] = 0x00 // ES_info_length low
		offset += 5
	}

	// CRC-32: use a simple MPEG-2 CRC calculation
	crc := crc32MPEG2(buf[:offset])
	binary.BigEndian.PutUint32(buf[offset:], crc)

	return buf
}

type pmtStream struct {
	streamType uint8
	pid        uint16
}

// crc32MPEG2 calculates the MPEG-2 CRC-32 used in MPEG-TS PSI tables.
func crc32MPEG2(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func TestParseFromTS_ValidSpliceInsert(t *testing.T) {
	// Create a splice_insert using the existing message.go API
	dur := 30 * time.Second
	msg := NewSpliceInsert(42, dur, true, true)

	// Encode to SCTE-35 binary
	sectionData, err := msg.Encode(true)
	if err != nil {
		t.Fatalf("encode splice_insert: %v", err)
	}

	// Wrap in TS packets on PID 0x102
	pid := uint16(0x102)
	tsData := wrapInTS(pid, sectionData)

	// Parse it back
	parsed, err := ParseFromTS(pid, tsData)
	if err != nil {
		t.Fatalf("ParseFromTS failed: %v", err)
	}

	if parsed.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert (0x%02x), got 0x%02x", CommandSpliceInsert, parsed.CommandType)
	}
	if parsed.EventID != 42 {
		t.Fatalf("expected event ID 42, got %d", parsed.EventID)
	}
	if !parsed.IsOut {
		t.Fatal("expected IsOut=true")
	}
	if !parsed.AutoReturn {
		t.Fatal("expected AutoReturn=true")
	}
	if parsed.BreakDuration == nil {
		t.Fatal("expected break duration to be set")
	}
	// Allow some rounding tolerance (90kHz tick resolution)
	if diff := *parsed.BreakDuration - dur; diff < -time.Millisecond || diff > time.Millisecond {
		t.Fatalf("expected ~30s duration, got %v", *parsed.BreakDuration)
	}
}

func TestParseFromTS_ValidTimeSignal(t *testing.T) {
	// Create a time_signal with a segmentation descriptor
	dur := 60 * time.Second
	upid := []byte("https://ads.example.com/avail/1")
	msg := NewTimeSignal(0x34, dur, 0x0F, upid)

	// Encode to SCTE-35 binary
	sectionData, err := msg.Encode(true)
	if err != nil {
		t.Fatalf("encode time_signal: %v", err)
	}

	// Wrap in TS packets on PID 0x102
	pid := uint16(0x102)
	tsData := wrapInTS(pid, sectionData)

	// Parse it back
	parsed, err := ParseFromTS(pid, tsData)
	if err != nil {
		t.Fatalf("ParseFromTS failed: %v", err)
	}

	if parsed.CommandType != CommandTimeSignal {
		t.Fatalf("expected time_signal (0x%02x), got 0x%02x", CommandTimeSignal, parsed.CommandType)
	}
	if len(parsed.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(parsed.Descriptors))
	}
	d := parsed.Descriptors[0]
	if d.SegmentationType != 0x34 {
		t.Fatalf("expected seg type 0x34, got 0x%02x", d.SegmentationType)
	}
	if d.UPIDType != 0x0F {
		t.Fatalf("expected UPID type 0x0F, got 0x%02x", d.UPIDType)
	}
}

func TestParseFromTS_InvalidCRC(t *testing.T) {
	// Create a valid splice_insert
	msg := NewSpliceInsert(1, 10*time.Second, true, false)
	sectionData, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Corrupt the last byte (CRC-32)
	sectionData[len(sectionData)-1] ^= 0xFF

	// Wrap in TS
	pid := uint16(0x102)
	tsData := wrapInTS(pid, sectionData)

	// Parse should fail due to CRC mismatch
	_, err = ParseFromTS(pid, tsData)
	if err == nil {
		t.Fatal("expected CRC error on corrupt data")
	}
}

func TestParseFromTS_EmptyData(t *testing.T) {
	_, err := ParseFromTS(0x102, nil)
	if err == nil {
		t.Fatal("expected error for nil data")
	}

	_, err = ParseFromTS(0x102, []byte{})
	if err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestParseFromTS_WrongPID(t *testing.T) {
	// Create a valid splice_insert on PID 0x102
	msg := NewSpliceInsert(1, 10*time.Second, true, false)
	sectionData, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	tsData := wrapInTS(0x102, sectionData)

	// Try to parse on a different PID
	_, err = ParseFromTS(0x200, tsData)
	if err == nil {
		t.Fatal("expected error when target PID not found")
	}
}

func TestParseFromTS_InvalidSyncByte(t *testing.T) {
	// Create a valid packet but corrupt the sync byte
	msg := NewSpliceInsert(1, 10*time.Second, true, false)
	sectionData, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	tsData := wrapInTS(0x102, sectionData)
	tsData[0] = 0x00 // corrupt sync byte

	_, err = ParseFromTS(0x102, tsData)
	if err == nil {
		t.Fatal("expected error for invalid sync byte")
	}
}

func TestParseFromTS_ShortPacket(t *testing.T) {
	// Data that is less than 188 bytes
	data := make([]byte, 100)
	data[0] = 0x47

	_, err := ParseFromTS(0x102, data)
	if err == nil {
		t.Fatal("expected error for short packet data")
	}
}

func TestParseFromTS_MultiPacket(t *testing.T) {
	// Create a time_signal with enough descriptors to exceed a single TS packet
	// payload (~183 bytes). Build a large UPID to force multi-packet.
	largeUPID := make([]byte, 200)
	for i := range largeUPID {
		largeUPID[i] = byte('A' + (i % 26))
	}

	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, largeUPID)
	sectionData, err := msg.Encode(true)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Verify section data exceeds single TS packet payload
	if len(sectionData) <= 183 {
		t.Fatalf("expected section data > 183 bytes for multi-packet test, got %d", len(sectionData))
	}

	pid := uint16(0x102)
	tsData := wrapInTS(pid, sectionData)

	// Should require multiple packets
	if len(tsData) <= 188 {
		t.Fatalf("expected multi-packet TS data, got %d bytes", len(tsData))
	}

	parsed, err := ParseFromTS(pid, tsData)
	if err != nil {
		t.Fatalf("ParseFromTS multi-packet failed: %v", err)
	}

	if parsed.CommandType != CommandTimeSignal {
		t.Fatalf("expected time_signal, got 0x%02x", parsed.CommandType)
	}
}

func TestDetectSCTE35PID_Found(t *testing.T) {
	streams := []pmtStream{
		{streamType: 0x1B, pid: 0x100}, // H.264 video
		{streamType: 0x0F, pid: 0x101}, // AAC audio
		{streamType: 0x86, pid: 0x102}, // SCTE-35
	}
	pmtData := buildMinimalPMT(streams)

	pids := DetectSCTE35PID(pmtData)
	if len(pids) != 1 {
		t.Fatalf("expected 1 SCTE-35 PID, got %d", len(pids))
	}
	if pids[0] != 0x102 {
		t.Fatalf("expected PID 0x102, got 0x%04x", pids[0])
	}
}

func TestDetectSCTE35PID_MultiplePIDs(t *testing.T) {
	streams := []pmtStream{
		{streamType: 0x1B, pid: 0x100}, // H.264 video
		{streamType: 0x86, pid: 0x102}, // SCTE-35
		{streamType: 0x0F, pid: 0x101}, // AAC audio
		{streamType: 0x86, pid: 0x103}, // Another SCTE-35 stream
	}
	pmtData := buildMinimalPMT(streams)

	pids := DetectSCTE35PID(pmtData)
	if len(pids) != 2 {
		t.Fatalf("expected 2 SCTE-35 PIDs, got %d", len(pids))
	}
	if pids[0] != 0x102 {
		t.Fatalf("expected first PID 0x102, got 0x%04x", pids[0])
	}
	if pids[1] != 0x103 {
		t.Fatalf("expected second PID 0x103, got 0x%04x", pids[1])
	}
}

func TestDetectSCTE35PID_NotFound(t *testing.T) {
	streams := []pmtStream{
		{streamType: 0x1B, pid: 0x100}, // H.264 video
		{streamType: 0x0F, pid: 0x101}, // AAC audio
	}
	pmtData := buildMinimalPMT(streams)

	pids := DetectSCTE35PID(pmtData)
	if len(pids) != 0 {
		t.Fatalf("expected 0 SCTE-35 PIDs, got %d", len(pids))
	}
}

func TestDetectSCTE35PID_EmptyPMT(t *testing.T) {
	// Empty/nil data should return nil
	pids := DetectSCTE35PID(nil)
	if len(pids) != 0 {
		t.Fatalf("expected 0 PIDs for nil data, got %d", len(pids))
	}

	pids = DetectSCTE35PID([]byte{})
	if len(pids) != 0 {
		t.Fatalf("expected 0 PIDs for empty data, got %d", len(pids))
	}
}

func TestDetectSCTE35PID_TruncatedPMT(t *testing.T) {
	// Truncated PMT should not panic
	pids := DetectSCTE35PID([]byte{0x02, 0xB0, 0x10})
	// nil or empty is acceptable, just not a panic
	_ = pids
}

func TestDetectSCTE35PID_WrongTableID(t *testing.T) {
	// PMT must have table_id 0x02
	streams := []pmtStream{
		{streamType: 0x86, pid: 0x102},
	}
	pmtData := buildMinimalPMT(streams)
	pmtData[0] = 0x00 // corrupt table_id

	pids := DetectSCTE35PID(pmtData)
	if len(pids) != 0 {
		t.Fatalf("expected 0 PIDs for wrong table_id, got %d", len(pids))
	}
}

func TestParseFromTS_NotSCTE35TableID(t *testing.T) {
	// Create valid section but change table_id to something other than 0xFC
	msg := NewSpliceInsert(1, 10*time.Second, true, false)
	sectionData, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Change table_id from 0xFC to 0x00
	sectionData[0] = 0x00

	pid := uint16(0x102)
	tsData := wrapInTS(pid, sectionData)

	_, err = ParseFromTS(pid, tsData)
	if err == nil {
		t.Fatal("expected error for non-SCTE-35 table_id")
	}
}
