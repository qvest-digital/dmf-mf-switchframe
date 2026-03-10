package scte35

import (
	"encoding/binary"
	"testing"
	"time"
)

// FuzzDecode exercises the Decode function with arbitrary SCTE-35 binary data.
// Seeds include valid splice_null, splice_insert, time_signal, and splice_insert cancel messages.
func FuzzDecode(f *testing.F) {
	// Seed: splice_null
	spliceNull := &CueMessage{CommandType: CommandSpliceNull}
	if data, err := spliceNull.Encode(false); err == nil {
		f.Add(data)
	}

	// Seed: splice_insert (immediate, cue-out with 30s break)
	spliceInsert := NewSpliceInsert(42, 30*time.Second, true, true)
	if data, err := spliceInsert.Encode(false); err == nil {
		f.Add(data)
	}

	// Seed: time_signal with segmentation descriptor
	timeSignal := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("https://ads.example.com/avail/1"))
	if data, err := timeSignal.Encode(false); err == nil {
		f.Add(data)
	}

	// Seed: splice_insert cancel
	cancel := &CueMessage{
		CommandType:                 CommandSpliceInsert,
		EventID:                    99,
		SpliceEventCancelIndicator: true,
	}
	if data, err := cancel.Encode(false); err == nil {
		f.Add(data)
	}

	// Seed: empty input.
	f.Add([]byte{})

	// Seed: single byte.
	f.Add([]byte{0xFC})

	// Seed: truncated header (< 14 bytes but starts with table_id).
	f.Add([]byte{0xFC, 0x30, 0x05, 0x00, 0x00, 0x00})

	// Seed: all zeros (invalid but shouldn't panic).
	f.Add(make([]byte, 32))

	// Seed: all 0xFF bytes.
	allFF := make([]byte, 32)
	for i := range allFF {
		allFF[i] = 0xFF
	}
	f.Add(allFF)

	f.Fuzz(func(t *testing.T, data []byte) {
		// Recover from panics and fail the test.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Decode panicked on input of length %d: %v", len(data), r)
			}
		}()
		// Must not panic. Any return value (including errors) is acceptable.
		_, _ = Decode(data)
	})
}

// FuzzParseFromTS exercises the ParseFromTS function with arbitrary MPEG-TS data.
// Seed includes a valid 188-byte TS packet containing a splice_null on PID 0x102.
func FuzzParseFromTS(f *testing.F) {
	// Seed: valid TS packet with splice_null on PID 0x102.
	spliceNull := &CueMessage{CommandType: CommandSpliceNull}
	sectionData, err := spliceNull.Encode(false)
	if err != nil {
		f.Fatalf("failed to encode splice_null seed: %v", err)
	}
	tsPkt := buildSeedTSPacket(0x102, sectionData)
	f.Add(tsPkt)

	// Seed: valid TS packet with splice_insert on PID 0x102.
	spliceInsert := NewSpliceInsert(1, 30*time.Second, true, true)
	insertSection, err := spliceInsert.Encode(false)
	if err == nil {
		insertTS := buildSeedTSPacket(0x102, insertSection)
		f.Add(insertTS)
	}

	// Seed: empty packet (188 bytes of zeros with sync byte).
	emptyPkt := make([]byte, 188)
	emptyPkt[0] = 0x47
	f.Add(emptyPkt)

	// Seed: 188 bytes of 0xFF.
	ffPkt := make([]byte, 188)
	for i := range ffPkt {
		ffPkt[i] = 0xFF
	}
	f.Add(ffPkt)

	// Seed: empty input.
	f.Add([]byte{})

	// Seed: short data (< 188 bytes).
	f.Add([]byte{0x47, 0x01, 0x02, 0x10})

	// Seed: two TS packets concatenated (splice_null + splice_insert).
	if insertSection != nil {
		twoPackets := make([]byte, 0, 376)
		twoPackets = append(twoPackets, tsPkt...)
		twoPackets = append(twoPackets, buildSeedTSPacket(0x102, insertSection)...)
		f.Add(twoPackets)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ParseFromTS panicked on input of length %d: %v", len(data), r)
			}
		}()
		// Test with a common PID. Must not panic.
		_, _ = ParseFromTS(0x102, data)
	})
}

// FuzzDecodeSpliceInsertCancel exercises the Decode function specifically with
// data shaped like splice_insert cancel messages.
func FuzzDecodeSpliceInsertCancel(f *testing.F) {
	// Seed: valid splice_insert cancel message.
	cancel := &CueMessage{
		CommandType:                 CommandSpliceInsert,
		EventID:                    12345,
		SpliceEventCancelIndicator: true,
	}
	if data, err := cancel.Encode(false); err == nil {
		f.Add(data)
	}

	// Seed: cancel with event ID 0.
	cancel0 := &CueMessage{
		CommandType:                 CommandSpliceInsert,
		EventID:                    0,
		SpliceEventCancelIndicator: true,
	}
	if data, err := cancel0.Encode(false); err == nil {
		f.Add(data)
	}

	// Seed: cancel with max event ID.
	cancelMax := &CueMessage{
		CommandType:                 CommandSpliceInsert,
		EventID:                    0xFFFFFFFF,
		SpliceEventCancelIndicator: true,
	}
	if data, err := cancelMax.Encode(false); err == nil {
		f.Add(data)
	}

	// Seed: minimal data that looks like a cancel (table_id + enough bytes).
	// Construct a hand-crafted minimal cancel section:
	// table_id(0xFC) + flags/length + header + splice_command_type(0x05) + event_id + cancel_indicator
	minCancel := make([]byte, 25)
	minCancel[0] = 0xFC            // table_id
	minCancel[13] = 0x05           // splice_command_type = splice_insert
	binary.BigEndian.PutUint32(minCancel[14:18], 42) // splice_event_id
	minCancel[18] = 0x80           // splice_event_cancel_indicator = 1, reserved = 1111111
	f.Add(minCancel)

	// Seed: empty input.
	f.Add([]byte{})

	// Seed: single byte.
	f.Add([]byte{0xFC})

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Decode (cancel) panicked on input of length %d: %v", len(data), r)
			}
		}()
		// Must not panic. Any return value is acceptable.
		_, _ = Decode(data)
	})
}

// buildSeedTSPacket constructs a single 188-byte TS packet with the given PID
// and SCTE-35 section data. Used only for seeding fuzz tests.
func buildSeedTSPacket(pid uint16, sectionData []byte) []byte {
	pkt := make([]byte, 188)

	// Sync byte.
	pkt[0] = 0x47

	// PID with PUSI=1.
	pkt[1] = 0x40 | byte((pid>>8)&0x1F)
	pkt[2] = byte(pid & 0xFF)

	// Adaptation field control = payload only (0x10), CC=0.
	pkt[3] = 0x10

	// Pointer field = 0 (section starts immediately).
	pkt[4] = 0x00

	// Copy section data (truncate if too large for single packet).
	payloadSpace := 188 - 5
	n := len(sectionData)
	if n > payloadSpace {
		n = payloadSpace
	}
	copy(pkt[5:], sectionData[:n])

	// Pad remaining bytes with 0xFF (stuffing).
	for i := 5 + n; i < 188; i++ {
		pkt[i] = 0xFF
	}

	return pkt
}
