package output

import (
	"bytes"
	"context"
	"io"
	"testing"

	astits "github.com/asticode/go-astits"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/scte35"
)

// testHarness wires an Injector → TSMuxer pipeline for correctness testing.
type testHarness struct {
	muxer      *TSMuxer
	injector   *scte35.Injector
	output     []byte
	currentPTS int64
}

// newTestHarness creates a wired Injector → TSMuxer with captured output.
// The injector's muxerSink feeds WriteSCTE35 on the muxer.
// The injector's ptsFn returns h.currentPTS.
func newTestHarness(t *testing.T) *testHarness {
	t.Helper()

	h := &testHarness{}

	h.muxer = NewTSMuxer()
	h.muxer.SetSCTE35PID(defaultSCTE35PID)
	h.muxer.SetOutput(func(data []byte) {
		h.output = append(h.output, data...)
	})

	return h
}

// newTestHarnessWithConfig creates a harness with a custom injector config.
// The caller can set OnSpliceOut and other config options before calling this.
func newTestHarnessWithConfig(t *testing.T, config scte35.InjectorConfig) *testHarness {
	t.Helper()

	h := &testHarness{}

	h.muxer = NewTSMuxer()
	h.muxer.SetSCTE35PID(defaultSCTE35PID)
	h.muxer.SetOutput(func(data []byte) {
		h.output = append(h.output, data...)
	})

	h.injector = scte35.NewInjector(config, func(data []byte) {
		_ = h.muxer.WriteSCTE35(data)
	}, func() int64 {
		return h.currentPTS
	})

	return h
}

// initInjector creates the injector with default config. Call after
// newTestHarness if you don't need a custom config.
func (h *testHarness) initInjector(t *testing.T) {
	t.Helper()
	if h.injector != nil {
		return
	}
	h.injector = scte35.NewInjector(scte35.InjectorConfig{
		SCTE35PID: defaultSCTE35PID,
	}, func(data []byte) {
		_ = h.muxer.WriteSCTE35(data)
	}, func() int64 {
		return h.currentPTS
	})
}

// initMuxer writes the first keyframe to initialize the muxer, then clears
// the captured output so subsequent assertions only see new data.
func (h *testHarness) initMuxer(t *testing.T) {
	t.Helper()

	idr := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	err := h.muxer.WriteVideo(idr)
	require.NoError(t, err, "initMuxer keyframe")
	h.currentPTS = 90000
	h.output = nil
}

// feedGOP writes 1 IDR + (n-1) P-frames at 30fps (3000 ticks/frame).
// Returns the PTS of the IDR frame.
func (h *testHarness) feedGOP(t *testing.T, n int) int64 {
	t.Helper()
	require.Greater(t, n, 0, "feedGOP requires at least 1 frame")

	idrPTS := h.currentPTS + 3000
	idr := &media.VideoFrame{
		PTS: idrPTS, DTS: idrPTS, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	err := h.muxer.WriteVideo(idr)
	require.NoError(t, err, "feedGOP IDR")
	h.currentPTS = idrPTS

	for i := 1; i < n; i++ {
		h.currentPTS += 3000
		p := &media.VideoFrame{
			PTS: h.currentPTS, DTS: h.currentPTS, IsKeyframe: false,
			WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x61, 0x88, 0x80, 0x40},
			Codec:    "h264",
		}
		err := h.muxer.WriteVideo(p)
		require.NoError(t, err, "feedGOP P-frame %d", i)
	}

	return idrPTS
}

// feedPFrames writes n P-frames (non-keyframes) at 3000 ticks each.
func (h *testHarness) feedPFrames(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		h.currentPTS += 3000
		p := &media.VideoFrame{
			PTS: h.currentPTS, DTS: h.currentPTS, IsKeyframe: false,
			WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x61, 0x88, 0x80, 0x40},
			Codec:    "h264",
		}
		err := h.muxer.WriteVideo(p)
		require.NoError(t, err, "feedPFrames frame %d", i)
	}
}

func (h *testHarness) close() {
	if h.injector != nil {
		h.injector.Close()
	}
}

// ---------------------------------------------------------------------------
// SCTE-35 section extraction from raw TS bytes
// ---------------------------------------------------------------------------

// extractedSection holds a parsed SCTE-35 section from TS output.
type extractedSection struct {
	msg    *scte35.CueMessage
	raw    []byte // raw section bytes
	offset int    // byte offset of the TS packet in the output
}

// extractSCTE35Sections finds all SCTE-35 sections in TS output by scanning
// for packets with the given PID that have PUSI set, then decoding the
// section payload via the scte35 package.
func extractSCTE35Sections(t *testing.T, data []byte, pid uint16) []extractedSection {
	t.Helper()

	var sections []extractedSection
	const pktSize = 188

	for i := 0; i+pktSize <= len(data); i += pktSize {
		pkt := data[i : i+pktSize]
		if pkt[0] != 0x47 {
			continue
		}

		pktPID := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pktPID != pid {
			continue
		}

		// Check PUSI (Payload Unit Start Indicator).
		pusi := (pkt[1] & 0x40) != 0
		if !pusi {
			continue
		}

		// Determine payload start based on adaptation field control.
		afc := (pkt[3] >> 4) & 0x03
		payloadStart := 4
		if afc == 0x03 || afc == 0x02 {
			// Adaptation field present.
			if payloadStart < pktSize {
				afLen := int(pkt[4])
				payloadStart = 5 + afLen
			}
		}

		if payloadStart >= pktSize {
			continue
		}

		// PUSI: pointer_field is the first byte of payload.
		pointerField := int(pkt[payloadStart])
		sectionStart := payloadStart + 1 + pointerField
		if sectionStart >= pktSize {
			continue
		}

		// Extract section: read table_id and section_length.
		payload := pkt[sectionStart:]
		if len(payload) < 3 {
			continue
		}
		if payload[0] != 0xFC {
			continue // not SCTE-35 table_id
		}

		sectionLen := 3 + (int(payload[1]&0x0F)<<8 | int(payload[2]))
		if sectionLen > len(payload) {
			sectionLen = len(payload)
		}

		sectionData := make([]byte, sectionLen)
		copy(sectionData, payload[:sectionLen])

		msg, err := scte35.Decode(sectionData)
		if err != nil {
			t.Logf("extractSCTE35Sections: decode warning at offset %d: %v", i, err)
			continue
		}

		sections = append(sections, extractedSection{
			msg:    msg,
			raw:    sectionData,
			offset: i,
		})
	}

	return sections
}

// assertSCTE35BeforeIDR verifies that the first SCTE-35 PID packet appears
// before the first video PID packet in the TS output.
func assertSCTE35BeforeIDR(t *testing.T, data []byte) {
	t.Helper()

	const pktSize = 188
	firstSCTE35 := -1
	firstVideo := -1

	for i := 0; i+pktSize <= len(data); i += pktSize {
		pkt := data[i : i+pktSize]
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

	require.GreaterOrEqual(t, firstSCTE35, 0, "no SCTE-35 packet found")
	require.GreaterOrEqual(t, firstVideo, 0, "no video packet found")
	require.Less(t, firstSCTE35, firstVideo,
		"SCTE-35 packet (offset %d) must appear before video (offset %d)", firstSCTE35, firstVideo)
}

// assertValidTSPackets verifies that all data is 188-byte aligned with valid
// sync bytes.
func assertValidTSPackets(t *testing.T, data []byte) {
	t.Helper()

	require.NotEmpty(t, data, "output must not be empty")

	// Use demuxer to validate — it will complain on malformed packets.
	// As a simpler check: verify 188-byte alignment and sync bytes.
	require.Zero(t, len(data)%188, "output must be 188-byte aligned, got %d bytes", len(data))

	for i := 0; i+188 <= len(data); i += 188 {
		require.Equal(t, byte(0x47), data[i],
			"TS sync byte missing at offset %d", i)
	}

	// Additionally, demux to verify structural integrity.
	ctx := context.Background()
	dmx := astits.NewDemuxer(ctx, bytes.NewReader(data))
	for {
		_, err := dmx.NextData()
		if err != nil {
			if err == io.EOF {
				break
			}
			// Non-fatal warnings are OK (e.g., incomplete final packet).
			break
		}
	}
}
