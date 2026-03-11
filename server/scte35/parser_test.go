package scte35

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

func TestParseFromTS_ValidSpliceInsert(t *testing.T) {
	t.Parallel()
	// Create a splice_insert using the existing message.go API
	dur := 30 * time.Second
	msg := NewSpliceInsert(42, dur, true, true)

	// Encode to SCTE-35 binary
	sectionData, err := msg.Encode(true)
	require.NoError(t, err)

	// Wrap in TS packets on PID 0x102
	pid := uint16(0x102)
	tsData := wrapInTS(pid, sectionData)

	// Parse it back
	parsed, err := ParseFromTS(pid, tsData)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandSpliceInsert), parsed.CommandType)
	require.Equal(t, uint32(42), parsed.EventID)
	require.True(t, parsed.IsOut, "expected IsOut=true")
	require.True(t, parsed.AutoReturn, "expected AutoReturn=true")
	require.NotNil(t, parsed.BreakDuration)
	// Allow some rounding tolerance (90kHz tick resolution)
	require.InDelta(t, float64(dur), float64(*parsed.BreakDuration), float64(time.Millisecond))
}

func TestParseFromTS_ValidTimeSignal(t *testing.T) {
	t.Parallel()
	// Create a time_signal with a segmentation descriptor
	dur := 60 * time.Second
	upid := []byte("https://ads.example.com/avail/1")
	msg := NewTimeSignal(0x34, dur, 0x0F, upid)

	// Encode to SCTE-35 binary
	sectionData, err := msg.Encode(true)
	require.NoError(t, err)

	// Wrap in TS packets on PID 0x102
	pid := uint16(0x102)
	tsData := wrapInTS(pid, sectionData)

	// Parse it back
	parsed, err := ParseFromTS(pid, tsData)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), parsed.CommandType)
	require.Len(t, parsed.Descriptors, 1)
	d := parsed.Descriptors[0]
	require.Equal(t, uint8(0x34), d.SegmentationType)
	require.Equal(t, uint8(0x0F), d.UPIDType)
}

func TestParseFromTS_InvalidCRC(t *testing.T) {
	t.Parallel()
	// Create a valid splice_insert
	msg := NewSpliceInsert(1, 10*time.Second, true, false)
	sectionData, err := msg.Encode(false)
	require.NoError(t, err)

	// Corrupt the last byte (CRC-32)
	sectionData[len(sectionData)-1] ^= 0xFF

	// Wrap in TS
	pid := uint16(0x102)
	tsData := wrapInTS(pid, sectionData)

	// Parse should fail due to CRC mismatch
	_, err = ParseFromTS(pid, tsData)
	require.Error(t, err)
}

func TestParseFromTS_EmptyData(t *testing.T) {
	t.Parallel()
	_, err := ParseFromTS(0x102, nil)
	require.Error(t, err)

	_, err = ParseFromTS(0x102, []byte{})
	require.Error(t, err)
}

func TestParseFromTS_WrongPID(t *testing.T) {
	t.Parallel()
	// Create a valid splice_insert on PID 0x102
	msg := NewSpliceInsert(1, 10*time.Second, true, false)
	sectionData, err := msg.Encode(false)
	require.NoError(t, err)

	tsData := wrapInTS(0x102, sectionData)

	// Try to parse on a different PID
	_, err = ParseFromTS(0x200, tsData)
	require.Error(t, err)
}

func TestParseFromTS_InvalidSyncByte(t *testing.T) {
	t.Parallel()
	// Create a valid packet but corrupt the sync byte
	msg := NewSpliceInsert(1, 10*time.Second, true, false)
	sectionData, err := msg.Encode(false)
	require.NoError(t, err)

	tsData := wrapInTS(0x102, sectionData)
	tsData[0] = 0x00 // corrupt sync byte

	_, err = ParseFromTS(0x102, tsData)
	require.Error(t, err)
}

func TestParseFromTS_ShortPacket(t *testing.T) {
	t.Parallel()
	// Data that is less than 188 bytes
	data := make([]byte, 100)
	data[0] = 0x47

	_, err := ParseFromTS(0x102, data)
	require.Error(t, err)
}

func TestParseFromTS_MultiPacket(t *testing.T) {
	t.Parallel()
	// Create a time_signal with enough descriptors to exceed a single TS packet
	// payload (~183 bytes). Build a large UPID to force multi-packet.
	largeUPID := make([]byte, 200)
	for i := range largeUPID {
		largeUPID[i] = byte('A' + (i % 26))
	}

	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, largeUPID)
	sectionData, err := msg.Encode(true)
	require.NoError(t, err)

	// Verify section data exceeds single TS packet payload
	require.Greater(t, len(sectionData), 183)

	pid := uint16(0x102)
	tsData := wrapInTS(pid, sectionData)

	// Should require multiple packets
	require.Greater(t, len(tsData), 188)

	parsed, err := ParseFromTS(pid, tsData)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), parsed.CommandType)
}

func TestDetectSCTE35PID_Found(t *testing.T) {
	t.Parallel()
	streams := []pmtStream{
		{streamType: 0x1B, pid: 0x100}, // H.264 video
		{streamType: 0x0F, pid: 0x101}, // AAC audio
		{streamType: 0x86, pid: 0x102}, // SCTE-35
	}
	pmtData := buildMinimalPMT(streams)

	pids := DetectSCTE35PID(pmtData)
	require.Len(t, pids, 1)
	require.Equal(t, uint16(0x102), pids[0])
}

func TestDetectSCTE35PID_MultiplePIDs(t *testing.T) {
	t.Parallel()
	streams := []pmtStream{
		{streamType: 0x1B, pid: 0x100}, // H.264 video
		{streamType: 0x86, pid: 0x102}, // SCTE-35
		{streamType: 0x0F, pid: 0x101}, // AAC audio
		{streamType: 0x86, pid: 0x103}, // Another SCTE-35 stream
	}
	pmtData := buildMinimalPMT(streams)

	pids := DetectSCTE35PID(pmtData)
	require.Len(t, pids, 2)
	require.Equal(t, uint16(0x102), pids[0])
	require.Equal(t, uint16(0x103), pids[1])
}

func TestDetectSCTE35PID_NotFound(t *testing.T) {
	t.Parallel()
	streams := []pmtStream{
		{streamType: 0x1B, pid: 0x100}, // H.264 video
		{streamType: 0x0F, pid: 0x101}, // AAC audio
	}
	pmtData := buildMinimalPMT(streams)

	pids := DetectSCTE35PID(pmtData)
	require.Len(t, pids, 0)
}

func TestDetectSCTE35PID_EmptyPMT(t *testing.T) {
	t.Parallel()
	// Empty/nil data should return nil
	pids := DetectSCTE35PID(nil)
	require.Len(t, pids, 0)

	pids = DetectSCTE35PID([]byte{})
	require.Len(t, pids, 0)
}

func TestDetectSCTE35PID_TruncatedPMT(t *testing.T) {
	t.Parallel()
	// Truncated PMT should not panic
	pids := DetectSCTE35PID([]byte{0x02, 0xB0, 0x10})
	// nil or empty is acceptable, just not a panic
	_ = pids
}

func TestDetectSCTE35PID_WrongTableID(t *testing.T) {
	t.Parallel()
	// PMT must have table_id 0x02
	streams := []pmtStream{
		{streamType: 0x86, pid: 0x102},
	}
	pmtData := buildMinimalPMT(streams)
	pmtData[0] = 0x00 // corrupt table_id

	pids := DetectSCTE35PID(pmtData)
	require.Len(t, pids, 0)
}

func TestParseFromTS_NotSCTE35TableID(t *testing.T) {
	t.Parallel()
	// Create valid section but change table_id to something other than 0xFC
	msg := NewSpliceInsert(1, 10*time.Second, true, false)
	sectionData, err := msg.Encode(false)
	require.NoError(t, err)

	// Change table_id from 0xFC to 0x00
	sectionData[0] = 0x00

	pid := uint16(0x102)
	tsData := wrapInTS(pid, sectionData)

	_, err = ParseFromTS(pid, tsData)
	require.Error(t, err)
}
