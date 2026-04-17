package output

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/internal/mpegts"
)

func collectVideoPESPayload(t *testing.T, output []byte) []byte {
	t.Helper()
	var pes []byte
	for i := 0; i+mpegts.PacketSize <= len(output); i += mpegts.PacketSize {
		pkt := output[i : i+mpegts.PacketSize]
		require.Equal(t, byte(0x47), pkt[0], "TS packet must start with sync byte")
		pid := (uint16(pkt[1]&0x1F) << 8) | uint16(pkt[2])
		if pid != mpegts.VideoPID {
			continue
		}
		pusi := pkt[1]&0x40 != 0
		afc := (pkt[3] >> 4) & 0x03
		off := 4
		if afc == 0x02 || afc == 0x03 {
			afLen := int(pkt[off])
			off += 1 + afLen
		}
		payload := pkt[off:]
		if pusi {
			require.GreaterOrEqual(t, len(payload), 9, "PES start packet must have full PES header")
			require.Equal(t, byte(0x00), payload[0])
			require.Equal(t, byte(0x00), payload[1])
			require.Equal(t, byte(0x01), payload[2])
			pesHeaderDataLen := int(payload[8])
			esStart := 9 + pesHeaderDataLen
			pes = append(pes, payload[esStart:]...)
		} else {
			pes = append(pes, payload...)
		}
	}
	return pes
}

func extractSEIPayloadType(es []byte) (int, bool) {
	for i := 0; i+5 < len(es); i++ {
		if es[i] == 0x00 && es[i+1] == 0x00 && es[i+2] == 0x00 && es[i+3] == 0x01 {
			if es[i+4] == 0x06 {
				return int(es[i+5]), true
			}
		}
	}
	return 0, false
}

func TestMuxer_WriteVideo_PrependsCaptionSEI_WhenManagerActive(t *testing.T) {
	m := NewTSMuxer()
	mgr := caption.NewManager()
	mgr.SetMode(caption.ModeAuthor)
	mgr.IngestText("HI")
	m.SetCaptionManager(mgr)

	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	idr := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idr))

	es := collectVideoPESPayload(t, output)
	pt, found := extractSEIPayloadType(es)
	require.True(t, found, "expected an SEI NALU in the elementary stream")
	require.Equal(t, 4, pt, "expected SEI payload type 4 (user_data_registered_itu_t_t35)")
}

func TestMuxer_WriteVideo_NoCaptionSEI_WhenManagerNil(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	idr := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idr))

	es := collectVideoPESPayload(t, output)
	if _, found := extractSEIPayloadType(es); found {
		t.Errorf("did not expect an SEI NALU when caption manager is nil; ES = %x", es)
	}
}

func TestMuxer_WriteVideo_KeyframeSEIOrdering_SPS_PPS_SEI_IDR(t *testing.T) {
	m := NewTSMuxer()
	mgr := caption.NewManager()
	mgr.SetMode(caption.ModeAuthor)
	mgr.IngestText("AB")
	m.SetCaptionManager(mgr)

	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	idr := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idr))

	es := collectVideoPESPayload(t, output)

	spsIdx := bytes.Index(es, []byte{0x00, 0x00, 0x00, 0x01, 0x67})
	ppsIdx := bytes.Index(es, []byte{0x00, 0x00, 0x00, 0x01, 0x68})
	seiIdx := bytes.Index(es, []byte{0x00, 0x00, 0x00, 0x01, 0x06})
	idrIdx := bytes.Index(es, []byte{0x00, 0x00, 0x00, 0x01, 0x65})

	require.GreaterOrEqual(t, spsIdx, 0, "SPS missing")
	require.GreaterOrEqual(t, ppsIdx, 0, "PPS missing")
	require.GreaterOrEqual(t, seiIdx, 0, "SEI missing")
	require.GreaterOrEqual(t, idrIdx, 0, "IDR missing")

	if spsIdx >= ppsIdx || ppsIdx >= seiIdx || seiIdx >= idrIdx {
		t.Errorf("ordering wrong: SPS(%d) PPS(%d) SEI(%d) IDR(%d), want SPS<PPS<SEI<IDR", spsIdx, ppsIdx, seiIdx, idrIdx)
	}
}

func TestMuxer_WriteVideo_NonKeyframeSEIOrdering_SEI_BeforeSlice(t *testing.T) {
	m := NewTSMuxer()
	mgr := caption.NewManager()
	mgr.SetMode(caption.ModeAuthor)
	m.SetCaptionManager(mgr)

	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	idr := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idr))
	output = nil

	mgr.IngestText("HI")

	pframe := &media.VideoFrame{
		PTS: 93600, DTS: 93600, IsKeyframe: false,
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x01},
	}
	require.NoError(t, m.WriteVideo(pframe))

	es := collectVideoPESPayload(t, output)
	seiIdx := bytes.Index(es, []byte{0x00, 0x00, 0x00, 0x01, 0x06})
	sliceIdx := bytes.Index(es, []byte{0x00, 0x00, 0x00, 0x01, 0x41})

	require.GreaterOrEqual(t, seiIdx, 0, "SEI missing")
	require.GreaterOrEqual(t, sliceIdx, 0, "non-IDR slice missing")
	if seiIdx >= sliceIdx {
		t.Errorf("SEI(%d) must come before slice(%d)", seiIdx, sliceIdx)
	}
}

func TestMuxer_WriteVideo_EmitsContinuousSEI_WhenManagerInOffMode(t *testing.T) {
	m := NewTSMuxer()
	mgr := caption.NewManager()
	m.SetCaptionManager(mgr)

	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	idr := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idr))

	es := collectVideoPESPayload(t, output)
	pt, found := extractSEIPayloadType(es)
	require.True(t, found, "expected continuous null-pair SEI even in ModeOff for downstream decoder acquisition")
	require.Equal(t, 4, pt)
}

func TestMuxer_WriteVideo_DoesNotDoubleInjectSEI_WhenWireDataAlreadyHasCaptionSEI(t *testing.T) {
	m := NewTSMuxer()
	mgr := caption.NewManager()
	mgr.SetMode(caption.ModeAuthor)
	m.SetCaptionManager(mgr)

	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	preBuiltSEI := caption.BuildSEINALU([]caption.CCPair{{Data: [2]byte{'A', 'B'}}})
	require.NotEmpty(t, preBuiltSEI)

	wire := []byte{}
	seiBody := preBuiltSEI[4:]
	wire = append(wire, byte(len(seiBody)>>24), byte(len(seiBody)>>16), byte(len(seiBody)>>8), byte(len(seiBody)))
	wire = append(wire, seiBody...)
	wire = append(wire, 0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00)

	idr := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: wire,
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idr))

	es := collectVideoPESPayload(t, output)
	count := countT35SEIMessages(es, "h264")
	if count != 1 {
		t.Errorf("expected exactly 1 caption SEI NALU in ES (no double-injection), got %d", count)
	}
}

// countT35SEIMessages walks every SEI NALU in an Annex B elementary stream and
// counts how many SEI messages have payload_type == 4 (user_data_registered_itu_t_t35).
// Walks the standard ITU-T H.264 §7.3.2.3 / H.265 §7.3.2.4 SEI message list and
// strips emulation-prevention bytes inline.
func countT35SEIMessages(es []byte, videoCodec string) int {
	isHEVC := videoCodec == "h265"
	count := 0
	i := 0
	for i+3 < len(es) {
		var scLen int
		switch {
		case es[i] == 0x00 && es[i+1] == 0x00 && es[i+2] == 0x00 && es[i+3] == 0x01:
			scLen = 4
		case es[i] == 0x00 && es[i+1] == 0x00 && es[i+2] == 0x01:
			scLen = 3
		default:
			i++
			continue
		}
		body := i + scLen
		if body >= len(es) {
			break
		}
		end := len(es)
		for j := body; j+2 < len(es); j++ {
			if es[j] == 0x00 && es[j+1] == 0x00 && (es[j+2] == 0x00 || es[j+2] == 0x01) {
				end = j
				break
			}
		}
		isSEI := false
		hdrLen := 0
		if isHEVC {
			if body+1 < end && (es[body]>>1)&0x3F == 39 {
				isSEI = true
				hdrLen = 2
			}
		} else {
			if es[body]&0x1F == 6 {
				isSEI = true
				hdrLen = 1
			}
		}
		if !isSEI {
			i = end
			continue
		}
		raw := append([]byte{}, es[body+hdrLen:end]...)
		stripped := raw[:0]
		zeros := 0
		for k := 0; k < len(raw); k++ {
			b := raw[k]
			if zeros >= 2 && b == 0x03 && k+1 < len(raw) && raw[k+1] <= 0x03 {
				zeros = 0
				continue
			}
			stripped = append(stripped, b)
			if b == 0x00 {
				zeros++
			} else {
				zeros = 0
			}
		}
		pos := 0
		for pos < len(stripped) {
			pt := 0
			for pos < len(stripped) && stripped[pos] == 0xFF {
				pt += 255
				pos++
			}
			if pos >= len(stripped) {
				break
			}
			pt += int(stripped[pos])
			pos++
			ps := 0
			for pos < len(stripped) && stripped[pos] == 0xFF {
				ps += 255
				pos++
			}
			if pos >= len(stripped) {
				break
			}
			ps += int(stripped[pos])
			pos++
			if pt == 4 {
				count++
			}
			if pos+ps > len(stripped) {
				break
			}
			pos += ps
			if pos < len(stripped) && stripped[pos] == 0x80 {
				break
			}
		}
		i = end
	}
	return count
}

func TestMuxer_WriteVideo_DoesNotDoubleInject_WithCombinedPicTimingCaptionSEI(t *testing.T) {
	m := NewTSMuxer()
	mgr := caption.NewManager()
	mgr.SetMode(caption.ModeAuthor)
	mgr.IngestText("HELLO")
	m.SetCaptionManager(mgr)

	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	combinedSEI := caption.BuildPicTimingAndCaptionSEI(
		[]caption.CCPair{{Data: [2]byte{'C', 'D'}}},
		caption.DefaultPicTimingConfig(),
		0,
		nil,
		"h264",
	)
	require.NotEmpty(t, combinedSEI)
	require.Equal(t, byte(0x06), combinedSEI[4], "combined SEI must use H.264 SEI NALU header (0x06)")
	require.Equal(t, byte(0x01), combinedSEI[5], "combined SEI must start with pic_timing payload_type=1, not T.35 (this exposes the precheck bug)")

	wire := []byte{}
	seiBody := combinedSEI[4:]
	wire = append(wire, byte(len(seiBody)>>24), byte(len(seiBody)>>16), byte(len(seiBody)>>8), byte(len(seiBody)))
	wire = append(wire, seiBody...)
	wire = append(wire, 0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00)

	idr := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: wire,
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idr))

	es := collectVideoPESPayload(t, output)
	got := countT35SEIMessages(es, "h264")
	if got != 1 {
		t.Errorf("expected exactly 1 T.35 caption SEI in ES (precheck must detect combined pic_timing+caption SEI), got %d", got)
	}
}
