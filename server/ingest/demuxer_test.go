package ingest

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// mockBroadcaster records BroadcastVideo/BroadcastAudio calls.
type mockBroadcaster struct {
	mu    sync.Mutex
	video []*media.VideoFrame
	audio []*media.AudioFrame
}

func (m *mockBroadcaster) BroadcastVideo(f *media.VideoFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.video = append(m.video, f)
}

func (m *mockBroadcaster) BroadcastAudio(f *media.AudioFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audio = append(m.audio, f)
}

func (m *mockBroadcaster) videoFrames() []*media.VideoFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*media.VideoFrame, len(m.video))
	copy(cp, m.video)
	return cp
}

func (m *mockBroadcaster) audioFrames() []*media.AudioFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*media.AudioFrame, len(m.audio))
	copy(cp, m.audio)
	return cp
}

func TestStreamDemuxer_EmptyReader(t *testing.T) {
	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", bytes.NewReader(nil), bc)
	err := d.Run(context.Background())
	require.NoError(t, err)
	require.Empty(t, bc.videoFrames())
}

func TestStreamDemuxer_ContextCancellation(t *testing.T) {
	r, _ := io.Pipe()
	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", r, bc)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestConvertAndBroadcastVideo_StripsSPSPPS(t *testing.T) {
	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", nil, bc)

	// Prism's demuxer includes SPS/PPS in NALUs (with Annex B start codes)
	// AND sets them as separate fields on the frame.
	spsNALU := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0xc0, 0x1e}
	ppsNALU := []byte{0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80}
	idrNALU := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00}

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		NALUs:      [][]byte{spsNALU, ppsNALU, idrNALU},
		SPS:        spsNALU[4:],
		PPS:        ppsNALU[4:],
		Codec:      "h264",
	}

	d.convertAndBroadcastVideo(frame)

	vf := bc.videoFrames()
	require.Len(t, vf, 1)
	require.NotEmpty(t, vf[0].WireData)

	// Parse AVC1 NALUs from WireData and collect types.
	var naluTypes []byte
	pos := 0
	for pos+4 <= len(vf[0].WireData) {
		naluLen := int(binary.BigEndian.Uint32(vf[0].WireData[pos : pos+4]))
		pos += 4
		if naluLen == 0 || pos+naluLen > len(vf[0].WireData) {
			break
		}
		naluTypes = append(naluTypes, vf[0].WireData[pos]&0x1F)
		pos += naluLen
	}

	// WireData must NOT contain SPS (7) or PPS (8).
	for _, nt := range naluTypes {
		require.NotEqual(t, byte(7), nt, "WireData should not contain SPS")
		require.NotEqual(t, byte(8), nt, "WireData should not contain PPS")
	}

	// WireData must contain IDR (5).
	require.Contains(t, naluTypes, byte(5), "WireData should contain IDR")

	// SPS/PPS fields must be preserved.
	require.NotEmpty(t, vf[0].SPS)
	require.NotEmpty(t, vf[0].PPS)
}

func TestConvertAndBroadcastVideo_PFramePassthrough(t *testing.T) {
	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", nil, bc)

	// P-frame (NAL type 1) — no SPS/PPS to strip.
	pNALU := []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a, 0x02, 0x40}

	frame := &media.VideoFrame{
		PTS:   2000,
		NALUs: [][]byte{pNALU},
		Codec: "h264",
	}

	d.convertAndBroadcastVideo(frame)

	vf := bc.videoFrames()
	require.Len(t, vf, 1)
	require.NotEmpty(t, vf[0].WireData)

	// AVC1: [4-byte length][NALU data without start code]
	naluLen := int(binary.BigEndian.Uint32(vf[0].WireData[0:4]))
	require.Equal(t, len(pNALU)-4, naluLen)
	require.Equal(t, byte(1), vf[0].WireData[4]&0x1F, "should be slice NAL type 1")
}

func TestConvertAndBroadcastVideo_OnlySPSPPSFrame(t *testing.T) {
	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", nil, bc)

	// Frame containing only SPS + PPS NALUs (no slice data).
	// Should produce empty WireData and not broadcast.
	spsNALU := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0xc0, 0x1e}
	ppsNALU := []byte{0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80}

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		NALUs:      [][]byte{spsNALU, ppsNALU},
		SPS:        spsNALU[4:],
		PPS:        ppsNALU[4:],
		Codec:      "h264",
	}

	d.convertAndBroadcastVideo(frame)

	// No slice NALUs → no frame should be broadcast.
	vf := bc.videoFrames()
	require.Empty(t, vf, "frame with only SPS/PPS should not be broadcast")
}

func TestConvertAndBroadcastVideo_HEVCStripsSPSPPSKeepsVPS(t *testing.T) {
	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", nil, bc)

	// HEVC NAL type is (first_byte >> 1) & 0x3F, NOT first_byte & 0x1F.
	// HEVC NAL header is 2 bytes: [type<<1 | layerID_high] [layerID_low<<3 | tid]
	// VPS: type=32 → first byte = 32<<1 = 0x40
	// SPS: type=33 → first byte = 33<<1 = 0x42
	// PPS: type=34 → first byte = 34<<1 = 0x44
	// IDR_W_RADL: type=19 → first byte = 19<<1 = 0x26
	vpsNALU := []byte{0x00, 0x00, 0x00, 0x01, 0x40, 0x01, 0x0c, 0x01}
	spsNALU := []byte{0x00, 0x00, 0x00, 0x01, 0x42, 0x01, 0x01, 0x01}
	ppsNALU := []byte{0x00, 0x00, 0x00, 0x01, 0x44, 0x01, 0xc0, 0xf7}
	idrNALU := []byte{0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xaf, 0x08, 0x40}

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		NALUs:      [][]byte{vpsNALU, spsNALU, ppsNALU, idrNALU},
		VPS:        vpsNALU[4:],
		SPS:        spsNALU[4:],
		PPS:        ppsNALU[4:],
		Codec:      "h265",
	}

	d.convertAndBroadcastVideo(frame)

	vf := bc.videoFrames()
	require.Len(t, vf, 1)
	require.NotEmpty(t, vf[0].WireData)

	// Parse length-prefixed NALUs and extract HEVC NAL types.
	var hevcNALTypes []byte
	pos := 0
	for pos+4 <= len(vf[0].WireData) {
		naluLen := int(binary.BigEndian.Uint32(vf[0].WireData[pos : pos+4]))
		pos += 4
		if naluLen == 0 || pos+naluLen > len(vf[0].WireData) {
			break
		}
		hevcNALTypes = append(hevcNALTypes, (vf[0].WireData[pos]>>1)&0x3F)
		pos += naluLen
	}

	// SPS (33) and PPS (34) must be stripped — sourceDecoder prepends them.
	for _, nt := range hevcNALTypes {
		require.NotEqual(t, byte(33), nt, "WireData should not contain SPS")
		require.NotEqual(t, byte(34), nt, "WireData should not contain PPS")
	}

	// VPS (32) must be KEPT — sourceDecoder does NOT re-inject VPS,
	// so stripping it would lose it entirely. HEVC decoders need VPS
	// before SPS to initialize.
	require.Contains(t, hevcNALTypes, byte(32), "WireData must keep VPS inline")

	// IDR_W_RADL (19) must be present.
	require.Contains(t, hevcNALTypes, byte(19), "WireData should contain HEVC IDR")

	// Parameter set fields must be preserved.
	require.NotEmpty(t, vf[0].VPS)
	require.NotEmpty(t, vf[0].SPS)
	require.NotEmpty(t, vf[0].PPS)
}

func TestConvertAndBroadcastVideo_BufferReuse(t *testing.T) {
	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", nil, bc)

	pNALU := []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a, 0x02, 0x40}

	// First call allocates the internal buffer.
	d.convertAndBroadcastVideo(&media.VideoFrame{
		PTS: 1000, NALUs: [][]byte{pNALU}, Codec: "h264",
	})
	require.NotNil(t, d.annexBBuf, "annexBBuf should be allocated after first call")
	firstCap := cap(d.annexBBuf)

	// Second call with same-size data should reuse (same capacity).
	d.convertAndBroadcastVideo(&media.VideoFrame{
		PTS: 2000, NALUs: [][]byte{pNALU}, Codec: "h264",
	})
	require.Equal(t, firstCap, cap(d.annexBBuf), "annexBBuf should reuse capacity")

	vf := bc.videoFrames()
	require.Len(t, vf, 2)
	// Each frame must have independent WireData (not shared backing).
	require.NotEmpty(t, vf[0].WireData)
	require.NotEmpty(t, vf[1].WireData)
}

func TestStreamDemuxer_RealTS(t *testing.T) {
	f, err := os.Open("testdata/sample.ts")
	if err != nil {
		t.Skip("testdata/sample.ts not available")
	}
	defer f.Close()

	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", f, bc)
	err = d.Run(context.Background())
	require.NoError(t, err)

	vf := bc.videoFrames()
	require.NotEmpty(t, vf, "expected video frames")

	// First video frame should be a keyframe with SPS/PPS and AVC1 WireData.
	require.True(t, vf[0].IsKeyframe, "first frame should be a keyframe")
	require.NotEmpty(t, vf[0].SPS, "keyframe should have SPS")
	require.NotEmpty(t, vf[0].PPS, "keyframe should have PPS")
	require.NotEmpty(t, vf[0].WireData, "frame should have AVC1 WireData")
	require.Equal(t, "h264", vf[0].Codec)

	// Audio frames should have sample rate and channels parsed from ADTS.
	af := bc.audioFrames()
	require.NotEmpty(t, af, "expected audio frames")
	require.Greater(t, af[0].SampleRate, 0, "sample rate should be parsed from ADTS")
	require.Greater(t, af[0].Channels, 0, "channels should be parsed from ADTS")
}

func TestStreamDemuxer_60FPS(t *testing.T) {
	f, err := os.Open("testdata/sample_60fps.ts")
	if err != nil {
		t.Skip("testdata/sample_60fps.ts not available")
	}
	defer f.Close()

	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", f, bc)
	err = d.Run(context.Background())
	require.NoError(t, err)

	vf := bc.videoFrames()
	require.NotEmpty(t, vf, "expected video frames from 60fps stream")

	// Should produce ~600 frames for a 10-second 59.94fps clip.
	require.Greater(t, len(vf), 500, "expected at least 500 video frames for 10s @ 59.94fps")

	// First frame must be a keyframe with SPS/PPS.
	require.True(t, vf[0].IsKeyframe, "first frame should be a keyframe")
	require.NotEmpty(t, vf[0].SPS, "keyframe should have SPS")
	require.NotEmpty(t, vf[0].PPS, "keyframe should have PPS")
	require.NotEmpty(t, vf[0].WireData, "frame should have AVC1 WireData")
	require.Equal(t, "h264", vf[0].Codec)

	// WireData must not contain SPS/PPS NALUs (they're stripped by convertAndBroadcastVideo).
	for i, frame := range vf {
		pos := 0
		for pos+4 <= len(frame.WireData) {
			naluLen := int(binary.BigEndian.Uint32(frame.WireData[pos : pos+4]))
			pos += 4
			if naluLen == 0 || pos+naluLen > len(frame.WireData) {
				break
			}
			naluType := frame.WireData[pos] & 0x1F
			require.NotEqual(t, byte(7), naluType, "frame %d WireData contains SPS", i)
			require.NotEqual(t, byte(8), naluType, "frame %d WireData contains PPS", i)
			pos += naluLen
		}
	}

	// PTS from Prism's demuxer is in microseconds (PES 90kHz * 1000000 / 90000).
	// With B-frames, PTS order != decode order, but overall range should match ~10 seconds.
	firstPTS := vf[0].PTS
	lastPTS := vf[len(vf)-1].PTS
	ptsDuration := lastPTS - firstPTS
	// 10 seconds = 10,000,000 µs. Allow 8-11 second range.
	require.Greater(t, ptsDuration, int64(8_000_000), "PTS range should span at least 8 seconds")
	require.Less(t, ptsDuration, int64(11_000_000), "PTS range should span at most 11 seconds")

	// Audio frames should be present with valid ADTS-parsed parameters.
	af := bc.audioFrames()
	require.NotEmpty(t, af, "expected audio frames")
	require.Greater(t, af[0].SampleRate, 0, "audio sample rate should be parsed")
	require.Greater(t, af[0].Channels, 0, "audio channels should be parsed")
}
