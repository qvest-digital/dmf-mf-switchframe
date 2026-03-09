package output

import (
	"testing"

	scte35lib "github.com/Comcast/scte35-go/pkg/scte35"
	"github.com/zsiec/prism/media"
)

func TestTSMuxer_WriteSCTE35_BeforeInit(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35Enabled(true)
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
	m.SetSCTE35Enabled(true)
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

	if len(output) == 0 {
		t.Fatal("expected TS output after WriteSCTE35")
	}
}

func TestTSMuxer_WriteSCTE35_PID(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35Enabled(true)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	writeInitKeyframe(t, m)
	// Clear output from keyframe init
	output = nil

	data := encodeSpliceNull(t)
	_ = m.WriteSCTE35(data)

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
	m.SetSCTE35Enabled(true)
	var output []byte
	m.SetOutput(func(data []byte) {
		output = append(output, data...)
	})

	writeInitKeyframe(t, m)

	data := encodeSpliceNull(t)

	// Write two SCTE-35 sections — CC should increment
	_ = m.WriteSCTE35(data)
	output = nil
	_ = m.WriteSCTE35(data)

	// Find the SCTE-35 packet and check CC
	for i := 0; i+188 <= len(output); i += 188 {
		pkt := output[i : i+188]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid == 0x102 {
			cc := pkt[3] & 0x0F
			if cc != 1 {
				t.Fatalf("expected continuity counter 1, got %d", cc)
			}
			return
		}
	}
	t.Fatal("no SCTE-35 packet found for continuity counter check")
}

func TestTSMuxer_WriteSCTE35_PendingFlushed(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35Enabled(true)
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
