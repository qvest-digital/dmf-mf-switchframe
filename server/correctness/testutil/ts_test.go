package testutil

import (
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// recordingTestingT is a minimal require.TestingT used to capture whether a
// parse helper reported a failure, without failing the enclosing test. It
// mirrors testify's real FailNow semantics by calling runtime.Goexit so the
// helper unwinds at the assertion point exactly as it would under *testing.T.
type recordingTestingT struct {
	mu       sync.Mutex
	failed   bool
	lastMsg  string
	failNowN int
}

func (r *recordingTestingT) Errorf(format string, args ...interface{}) {
	r.mu.Lock()
	r.failed = true
	r.lastMsg = format
	r.mu.Unlock()
}

func (r *recordingTestingT) FailNow() {
	r.mu.Lock()
	r.failNowN++
	r.mu.Unlock()
	runtime.Goexit()
}

// runParseInGoroutine runs parseTSPackets on an isolated goroutine so a
// require failure (runtime.Goexit) terminates only that goroutine. Returns the
// recorder and whether the parse ran to completion (i.e. did not abort).
func runParseInGoroutine(data []byte) (*recordingTestingT, bool) {
	rt := &recordingTestingT{}
	completed := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = parseTSPackets(rt, data)
		completed = true
	}()
	<-done
	return rt, completed
}

// buildTSPacket creates a minimal valid 188-byte TS packet.
func buildTSPacket(pid uint16, cc uint8, hasAdaptation, hasPayload bool) []byte {
	pkt := make([]byte, 188)
	pkt[0] = 0x47 // sync byte

	// PID: 13 bits split across bytes 1 and 2
	pkt[1] = byte(pid>>8) & 0x1F
	pkt[2] = byte(pid & 0xFF)

	// Adaptation field control (2 bits) + continuity counter (4 bits) in byte 3
	var afc byte
	if hasAdaptation && hasPayload {
		afc = 0x30
	} else if hasAdaptation {
		afc = 0x20
	} else if hasPayload {
		afc = 0x10
	}
	pkt[3] = afc | (cc & 0x0F)

	if hasAdaptation {
		// Minimal adaptation field: length=1, flags=0
		pkt[4] = 1 // adaptation field length
		pkt[5] = 0 // flags
		// Fill rest with stuffing (0xFF)
		for i := 6; i < 188; i++ {
			pkt[i] = 0xFF
		}
	}

	return pkt
}

// buildTSPacketWithPCR creates a TS packet with a PCR in the adaptation field.
func buildTSPacketWithPCR(pid uint16, cc uint8, pcrBase int64) []byte {
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	pkt[1] = byte(pid>>8) & 0x1F
	pkt[2] = byte(pid & 0xFF)
	pkt[3] = 0x20 | (cc & 0x0F) // adaptation only

	// Adaptation field with PCR
	pkt[4] = 7    // adaptation field length (1 flag + 6 PCR bytes)
	pkt[5] = 0x10 // PCR flag set

	// PCR: 33-bit base + 6 reserved + 9-bit extension
	// We set extension to 0 for simplicity
	pkt[6] = byte(pcrBase >> 25)
	pkt[7] = byte(pcrBase >> 17)
	pkt[8] = byte(pcrBase >> 9)
	pkt[9] = byte(pcrBase >> 1)
	pkt[10] = byte((pcrBase&1)<<7) | 0x7E // bit0 of base << 7, then 6 reserved bits = 1
	pkt[11] = 0                           // extension = 0

	// Fill rest with stuffing
	for i := 12; i < 188; i++ {
		pkt[i] = 0xFF
	}

	return pkt
}

func TestParseTSPackets_Basic(t *testing.T) {
	// Build 3 valid packets
	data := make([]byte, 0, 188*3)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)
	data = append(data, buildTSPacket(0x0100, 2, false, true)...)

	packets := ParseTSPackets(t, data)
	require.Len(t, packets, 3)

	require.Equal(t, byte(0x47), packets[0].SyncByte)
	require.Equal(t, uint16(0x0100), packets[0].PID)
	require.Equal(t, uint8(0), packets[0].ContinuityCounter)
	require.True(t, packets[0].HasPayload)
	require.False(t, packets[0].HasAdaptation)

	require.Equal(t, uint8(1), packets[1].ContinuityCounter)
	require.Equal(t, uint8(2), packets[2].ContinuityCounter)
}

func TestParseTSPackets_WithAdaptation(t *testing.T) {
	data := buildTSPacket(0x0100, 0, true, false)
	packets := ParseTSPackets(t, data)
	require.Len(t, packets, 1)
	require.True(t, packets[0].HasAdaptation)
	require.False(t, packets[0].HasPayload)
	require.NotNil(t, packets[0].AdaptationField)
}

func TestParseTSPackets_WithPCR(t *testing.T) {
	pcrBase := int64(90000) // 1 second at 90kHz
	data := buildTSPacketWithPCR(0x0100, 0, pcrBase)
	packets := ParseTSPackets(t, data)

	require.Len(t, packets, 1)
	require.NotNil(t, packets[0].AdaptationField)
	require.NotNil(t, packets[0].AdaptationField.PCR)
	require.Equal(t, pcrBase, *packets[0].AdaptationField.PCR)
}

func TestAssertValidTSStructure(t *testing.T) {
	data := make([]byte, 0, 188*2)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)

	// Should not fail
	AssertValidTSStructure(t, data)
}

func TestAssertContinuityCounters(t *testing.T) {
	// Valid: sequential CC for same PID
	data := make([]byte, 0, 188*4)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)
	data = append(data, buildTSPacket(0x0100, 2, false, true)...)
	data = append(data, buildTSPacket(0x0100, 3, false, true)...)

	AssertContinuityCounters(t, data)
}

func TestAssertContinuityCounters_Wrap(t *testing.T) {
	// CC wraps from 15 to 0
	data := make([]byte, 0, 188*2)
	data = append(data, buildTSPacket(0x0100, 15, false, true)...)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)

	AssertContinuityCounters(t, data)
}

func TestAssertContinuityCounters_MultiplePIDs(t *testing.T) {
	// Different PIDs track CC independently
	data := make([]byte, 0, 188*4)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)
	data = append(data, buildTSPacket(0x0200, 0, false, true)...)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)
	data = append(data, buildTSPacket(0x0200, 1, false, true)...)

	AssertContinuityCounters(t, data)
}

func TestAssertPATPresent(t *testing.T) {
	data := make([]byte, 0, 188*3)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...) // not PAT
	data = append(data, buildTSPacket(0x0000, 0, false, true)...) // PAT (PID 0)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)

	AssertPATPresent(t, data, 3)
}

func TestAssertPMTPresent(t *testing.T) {
	data := make([]byte, 0, 188*3)
	data = append(data, buildTSPacket(0x0100, 0, false, true)...)
	data = append(data, buildTSPacket(0x1000, 0, false, true)...) // PMT at PID 0x1000
	data = append(data, buildTSPacket(0x0100, 1, false, true)...)

	AssertPMTPresent(t, data, 0x1000, 3)
}

func TestExtractPCRValues(t *testing.T) {
	data := make([]byte, 0, 188*3)
	data = append(data, buildTSPacketWithPCR(0x0100, 0, 90000)...)
	data = append(data, buildTSPacket(0x0100, 1, false, true)...) // no PCR
	data = append(data, buildTSPacketWithPCR(0x0100, 2, 180000)...)

	pcrs := ExtractPCRValues(t, data)
	require.Len(t, pcrs, 2)
	require.Equal(t, int64(90000), pcrs[0])
	require.Equal(t, int64(180000), pcrs[1])
}

func TestAssertStuffingBytes(t *testing.T) {
	// Build packet with adaptation field containing stuffing
	pkt := buildTSPacket(0x0100, 0, true, false)
	packets := ParseTSPackets(t, pkt)

	// Should pass: stuffing bytes are 0xFF
	AssertStuffingBytes(t, packets)
}

func TestAssertRandomAccessIndicator(t *testing.T) {
	// Build a packet with RAI set
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	pkt[1] = 0x01
	pkt[2] = 0x00
	pkt[3] = 0x20 // adaptation only
	pkt[4] = 1    // adaptation field length
	pkt[5] = 0x40 // random_access_indicator set

	for i := 6; i < 188; i++ {
		pkt[i] = 0xFF
	}

	packets := ParseTSPackets(t, pkt)
	require.True(t, packets[0].AdaptationField.RandomAccessIndicator)
}

// buildTSPacketBadAdaptationLen builds a payload-bearing packet (afc=0x30)
// whose adaptation_field_length pushes the computed payload start to
// payloadStart = 4 + 1 + afLen. With afLen=183 this lands exactly at the
// 188-byte packet boundary, leaving no room for payload.
func buildTSPacketBadAdaptationLen(afLen byte) []byte {
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	pkt[1] = 0x01
	pkt[2] = 0x00
	pkt[3] = 0x30  // adaptation + payload
	pkt[4] = afLen // adaptation field length
	for i := 5; i < 188; i++ {
		pkt[i] = 0xFF
	}
	return pkt
}

// TestParseTSPackets_AdaptationLengthDesyncFailsParse ensures a payload-bearing
// packet whose adaptation length overruns the packet is rejected loudly rather
// than silently downgraded to no-payload. Silently dropping the payload would
// let AssertContinuityCounters skip the CC check for the packet (it treats
// payload-less packets as allowed-to-repeat) and mask a real CC discontinuity.
func TestParseTSPackets_AdaptationLengthDesyncFailsParse(t *testing.T) {
	// afLen=183 -> payloadStart = 4 + 1 + 183 = 188 == PacketSize (no room).
	bad := buildTSPacketBadAdaptationLen(183)

	rt, completed := runParseInGoroutine(bad)
	require.False(t, completed,
		"parse should abort on adaptation-length desync, not run to completion")
	require.True(t, rt.failed, "parse should report a failure via Errorf")
	require.Greater(t, rt.failNowN, 0, "parse should call FailNow")
}

// TestParseTSPackets_AdaptationLengthMaxValidStillParses confirms the guard does
// not fire for the largest adaptation length that still leaves a payload byte
// (afLen=182 -> payloadStart=187 < 188).
func TestParseTSPackets_AdaptationLengthMaxValidStillParses(t *testing.T) {
	good := buildTSPacketBadAdaptationLen(182)

	packets := ParseTSPackets(t, good)
	require.Len(t, packets, 1)
	require.True(t, packets[0].HasPayload)
	require.Len(t, packets[0].Payload, 1, "exactly one payload byte should remain")
}

// TestParseTSPackets_AdaptationOnlyMaxLenNoFailure confirms an adaptation-only
// packet (no payload bit) with a maximal adaptation length is not affected by
// the payload-start guard.
func TestParseTSPackets_AdaptationOnlyMaxLenNoFailure(t *testing.T) {
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	pkt[1] = 0x01
	pkt[2] = 0x00
	pkt[3] = 0x20 // adaptation only, no payload
	pkt[4] = 183  // fills the rest of the packet
	for i := 5; i < 188; i++ {
		pkt[i] = 0xFF
	}

	packets := ParseTSPackets(t, pkt)
	require.Len(t, packets, 1)
	require.False(t, packets[0].HasPayload)
	require.Nil(t, packets[0].Payload)
}
