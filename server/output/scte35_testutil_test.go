package output

import (
	"bytes"
	"testing"
	"time"

	scte35lib "github.com/Comcast/scte35-go/pkg/scte35"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/scte35"
)

// ---------------------------------------------------------------------------
// testHarness: wires scte35.Injector → TSMuxer with captured TS output
// ---------------------------------------------------------------------------

type testHarness struct {
	t          *testing.T
	muxer      *TSMuxer
	injector   *scte35.Injector
	output     []byte
	currentPTS int64
}

// newTestHarness creates a wired Injector → TSMuxer pair.
// The injector's muxerSink calls muxer.WriteSCTE35(data), and ptsFn
// returns h.currentPTS (starts at 90000 = 1 second at 90 kHz).
func newTestHarness(t *testing.T) *testHarness {
	t.Helper()

	h := &testHarness{
		t:          t,
		currentPTS: 90000,
	}

	h.muxer = NewTSMuxer()
	h.muxer.SetSCTE35PID(defaultSCTE35PID)
	h.muxer.SetOutput(func(data []byte) {
		h.output = append(h.output, data...)
	})

	h.injector = scte35.NewInjector(
		scte35.InjectorConfig{
			SCTE35PID:      defaultSCTE35PID,
			VerifyEncoding: true,
		},
		func(data []byte) {
			_ = h.muxer.WriteSCTE35(data)
		},
		func() int64 {
			return h.currentPTS
		},
	)

	return h
}

// close shuts down the injector.
func (h *testHarness) close() {
	h.injector.Close()
}

// initMuxer writes a synthetic keyframe to initialize the muxer, then
// clears the captured output so subsequent assertions see only new data.
func (h *testHarness) initMuxer(t *testing.T) {
	t.Helper()
	writeInitKeyframe(t, h.muxer)
	h.output = nil
}

// feedGOP writes 1 keyframe + (gopSize-1) P-frames at 30fps (3000 PTS
// ticks per frame). Returns the PTS of the keyframe. Uses the same
// SPS/PPS/WireData as existing tests.
func (h *testHarness) feedGOP(t *testing.T, gopSize int) int64 {
	t.Helper()
	require.Greater(t, gopSize, 0, "gopSize must be >= 1")

	keyframePTS := h.currentPTS

	// Keyframe (IDR)
	idr := &media.VideoFrame{
		PTS: keyframePTS, DTS: keyframePTS, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	require.NoError(t, h.muxer.WriteVideo(idr))
	h.currentPTS += 3000

	// P-frames
	for i := 1; i < gopSize; i++ {
		pFrame := &media.VideoFrame{
			PTS: h.currentPTS, DTS: h.currentPTS, IsKeyframe: false,
			WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x61, 0x88, 0x80, 0x40},
			Codec:    "h264",
		}
		require.NoError(t, h.muxer.WriteVideo(pFrame))
		h.currentPTS += 3000
	}

	return keyframePTS
}

// ---------------------------------------------------------------------------
// scte35Section: decoded SCTE-35 section extracted from TS bytes
// ---------------------------------------------------------------------------

type scte35Section struct {
	msg      *scte35.CueMessage
	raw      []byte // complete section bytes (before 0xFF stuffing)
	tsOffset int    // byte offset of the first TS packet carrying this section
	pid      uint16
}

// pendingSection tracks in-progress section reassembly across TS packets.
type pendingSection struct {
	buf      bytes.Buffer
	offset   int // TS byte offset of the first packet
	expected int // section_length + 3 (total section size), 0 = unknown
}

// extractSCTE35Sections parses TS bytes and extracts all SCTE-35 sections
// on the given PID. SCTE-35 is carried as PSI (pointer_field framing, not
// PES), so we handle PUSI + pointer_field and adaptation fields.
func extractSCTE35Sections(t *testing.T, tsData []byte, scte35PID uint16) []scte35Section {
	t.Helper()

	const pktSize = 188

	var (
		current  *pendingSection
		sections []scte35Section
	)

	for i := 0; i+pktSize <= len(tsData); i += pktSize {
		pkt := tsData[i : i+pktSize]
		if pkt[0] != 0x47 {
			continue
		}

		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid != scte35PID {
			continue
		}

		pusi := (pkt[1] & 0x40) != 0
		adaptField := (pkt[3] & 0x20) != 0

		payloadStart := 4
		if adaptField {
			adaptLen := int(pkt[4])
			payloadStart = 5 + adaptLen
			if payloadStart >= pktSize {
				continue // no payload in this packet
			}
		}

		payload := pkt[payloadStart:]

		if pusi {
			// Complete any in-progress section.
			if current != nil {
				finishSection(t, current, scte35PID, &sections)
				current = nil
			}

			if len(payload) < 1 {
				continue
			}

			pointerField := int(payload[0])
			sectionStart := 1 + pointerField
			if sectionStart >= len(payload) {
				continue
			}

			// Skip 0xFF stuffing bytes that may precede the section.
			data := payload[sectionStart:]
			for len(data) > 0 && data[0] == 0xFF {
				data = data[1:]
			}
			if len(data) == 0 {
				continue
			}

			current = &pendingSection{offset: i}
			current.buf.Write(data)
		} else if current != nil {
			// Continuation packet.
			current.buf.Write(payload)
		}

		// If we have enough bytes to know the section length, check if complete.
		if current != nil && current.expected == 0 && current.buf.Len() >= 3 {
			b := current.buf.Bytes()
			sectionLen := int(b[1]&0x0F)<<8 | int(b[2])
			current.expected = sectionLen + 3
		}

		if current != nil && current.expected > 0 && current.buf.Len() >= current.expected {
			finishSection(t, current, scte35PID, &sections)
			current = nil
		}
	}

	// Flush any trailing section.
	if current != nil {
		finishSection(t, current, scte35PID, &sections)
	}

	return sections
}

// finishSection trims stuffing, decodes, and appends to sections.
func finishSection(t *testing.T, ps *pendingSection, pid uint16, sections *[]scte35Section) {
	t.Helper()

	raw := ps.buf.Bytes()

	// Trim to declared section length if known.
	if ps.expected > 0 && len(raw) > ps.expected {
		raw = raw[:ps.expected]
	}

	// Trim trailing 0xFF stuffing.
	for len(raw) > 0 && raw[len(raw)-1] == 0xFF {
		raw = raw[:len(raw)-1]
	}

	if len(raw) == 0 {
		return
	}

	rawCopy := make([]byte, len(raw))
	copy(rawCopy, raw)

	msg, err := scte35.Decode(rawCopy)
	require.NoError(t, err, "failed to decode SCTE-35 section at TS offset %d", ps.offset)

	*sections = append(*sections, scte35Section{
		msg:      msg,
		raw:      rawCopy,
		tsOffset: ps.offset,
		pid:      pid,
	})
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

// expectedSpliceInsert holds the expected values for assertSpliceInsert.
type expectedSpliceInsert struct {
	eventID         uint32
	isOut           bool
	autoReturn      bool
	durationMs      *int64 // nil = no break duration
	timing          string // "immediate" or "scheduled"
	spliceTimePTS   *int64 // nil = not present
	uniqueProgramID uint16
	availNum        uint8
	availsExpected  uint8
	tier            uint16 // 0 means don't check (or expect 0xFFF)
}

// assertSpliceInsert validates all splice_insert fields on a decoded CueMessage.
func assertSpliceInsert(t *testing.T, msg *scte35.CueMessage, exp expectedSpliceInsert) {
	t.Helper()
	require.NotNil(t, msg, "CueMessage must not be nil")
	require.Equal(t, uint8(scte35.CommandSpliceInsert), msg.CommandType, "expected splice_insert command type")

	require.Equal(t, exp.eventID, msg.EventID, "EventID mismatch")
	require.Equal(t, exp.isOut, msg.IsOut, "IsOut mismatch")
	require.Equal(t, exp.autoReturn, msg.AutoReturn, "AutoReturn mismatch")
	require.Equal(t, exp.timing, msg.Timing, "Timing mismatch")
	require.Equal(t, exp.uniqueProgramID, msg.UniqueProgramID, "UniqueProgramID mismatch")
	require.Equal(t, exp.availNum, msg.AvailNum, "AvailNum mismatch")
	require.Equal(t, exp.availsExpected, msg.AvailsExpected, "AvailsExpected mismatch")

	if exp.durationMs != nil {
		require.NotNil(t, msg.BreakDuration, "expected BreakDuration to be set")
		// Convert ticks back to ms for comparison. The scte35lib uses 90kHz
		// ticks internally, so round-trip through ticks may have small rounding.
		// Compare within 1ms tolerance.
		gotMs := msg.BreakDuration.Milliseconds()
		require.InDelta(t, *exp.durationMs, gotMs, 1, "BreakDuration mismatch (ms)")
	} else {
		require.Nil(t, msg.BreakDuration, "expected no BreakDuration")
	}

	if exp.spliceTimePTS != nil {
		require.NotNil(t, msg.SpliceTimePTS, "expected SpliceTimePTS to be set")
		require.Equal(t, *exp.spliceTimePTS, *msg.SpliceTimePTS, "SpliceTimePTS mismatch")
	} else {
		require.Nil(t, msg.SpliceTimePTS, "expected no SpliceTimePTS")
	}

	if exp.tier != 0 {
		require.Equal(t, exp.tier, msg.Tier, "Tier mismatch")
	}
}

// expectedTimeSignalDescriptor holds the expected values for one descriptor
// in a time_signal message.
type expectedTimeSignalDescriptor struct {
	segmentationType uint8
	durationMs       *int64 // nil = no duration
	upidType         uint8
	upid             []byte
	segNum           uint8
	segExpected      uint8
}

// assertTimeSignalDescriptor validates a specific descriptor in a decoded
// time_signal CueMessage at the given index.
func assertTimeSignalDescriptor(t *testing.T, msg *scte35.CueMessage, idx int, exp expectedTimeSignalDescriptor) {
	t.Helper()
	require.NotNil(t, msg, "CueMessage must not be nil")
	require.Equal(t, uint8(scte35.CommandTimeSignal), msg.CommandType, "expected time_signal command type")
	require.Greater(t, len(msg.Descriptors), idx, "descriptor index %d out of range (have %d)", idx, len(msg.Descriptors))

	d := msg.Descriptors[idx]
	require.Equal(t, exp.segmentationType, d.SegmentationType, "SegmentationType mismatch at index %d", idx)
	require.Equal(t, exp.upidType, d.UPIDType, "UPIDType mismatch at index %d", idx)
	require.Equal(t, exp.upid, d.UPID, "UPID mismatch at index %d", idx)
	require.Equal(t, exp.segNum, d.SegNum, "SegNum mismatch at index %d", idx)
	require.Equal(t, exp.segExpected, d.SegExpected, "SegExpected mismatch at index %d", idx)

	if exp.durationMs != nil {
		require.NotNil(t, d.DurationTicks, "expected DurationTicks to be set at index %d", idx)
		gotDur := scte35lib.TicksToDuration(*d.DurationTicks)
		gotMs := gotDur.Milliseconds()
		require.InDelta(t, *exp.durationMs, gotMs, 1, "Duration mismatch at index %d (ms)", idx)
	} else {
		require.Nil(t, d.DurationTicks, "expected no DurationTicks at index %d", idx)
	}
}

// assertSCTE35BeforeIDR verifies that the first TS packet on the SCTE-35
// PID appears before the first TS packet on the video PID in the stream.
func assertSCTE35BeforeIDR(t *testing.T, tsData []byte) {
	t.Helper()

	const pktSize = 188
	firstSCTE35 := -1
	firstVideo := -1

	for i := 0; i+pktSize <= len(tsData); i += pktSize {
		pkt := tsData[i : i+pktSize]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid == defaultSCTE35PID && firstSCTE35 < 0 {
			firstSCTE35 = i
		}
		if pid == videoPID && firstVideo < 0 {
			firstVideo = i
		}
	}

	require.GreaterOrEqual(t, firstSCTE35, 0, "no SCTE-35 packet found in TS data")
	require.GreaterOrEqual(t, firstVideo, 0, "no video packet found in TS data")
	require.Less(t, firstSCTE35, firstVideo,
		"SCTE-35 packet (offset %d) must appear before video packet (offset %d)", firstSCTE35, firstVideo)
}

// assertValidTSPackets verifies that tsData consists of properly aligned
// 188-byte TS packets with 0x47 sync bytes.
func assertValidTSPackets(t *testing.T, tsData []byte) {
	t.Helper()

	const pktSize = 188
	require.NotEmpty(t, tsData, "TS data is empty")
	require.Zero(t, len(tsData)%pktSize,
		"TS data length %d is not a multiple of %d", len(tsData), pktSize)

	for i := 0; i+pktSize <= len(tsData); i += pktSize {
		require.Equal(t, byte(0x47), tsData[i],
			"missing sync byte at offset %d", i)
	}
}

// durationPtr returns a pointer to the number of milliseconds in d.
func durationPtr(ms int64) *int64 {
	return &ms
}

// ptsPtr returns a pointer to the given PTS value.
func ptsPtr(pts int64) *int64 {
	return &pts
}

// Ensure unused imports don't cause a compile error — these are used by
// the functions above but the compiler needs to see usage in this file.
var _ = time.Second
