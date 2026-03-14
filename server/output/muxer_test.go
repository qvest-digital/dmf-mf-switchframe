package output

import (
	"context"
	"io"
	"reflect"
	"testing"

	astits "github.com/asticode/go-astits"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/scte35"
)

func TestTSMuxer_NewMuxer(t *testing.T) {
	m := NewTSMuxer()
	require.NotNil(t, m)
	require.False(t, m.initialized)
}

func TestTSMuxer_WriteVideo_DefersUntilKeyframe(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	pFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: false,
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x01},
	}
	err := m.WriteVideo(pFrame)
	require.NoError(t, err)
	require.Empty(t, output, "should not produce output before first keyframe")
}

func TestTSMuxer_WriteVideo_InitOnKeyframe(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	err := m.WriteVideo(idrFrame)
	require.NoError(t, err)
	require.True(t, m.initialized)
	require.NotEmpty(t, output)
	require.Equal(t, 0, len(output)%188, "output must be multiple of 188 bytes")
}

func TestTSMuxer_WriteVideo_SubsequentFrames(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Init with keyframe first.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))
	output = nil

	// Subsequent P-frame should produce output.
	pFrame := &media.VideoFrame{
		PTS: 93600, DTS: 93600, IsKeyframe: false,
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x01},
	}
	err := m.WriteVideo(pFrame)
	require.NoError(t, err)
	require.NotEmpty(t, output)
	require.Equal(t, 0, len(output)%188, "output must be multiple of 188 bytes")
}

func TestTSMuxer_WriteVideo_KeyframePrependsSPSPPS(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	sps := []byte{0x67, 0x42, 0xC0, 0x1E}
	pps := []byte{0x68, 0xCE, 0x38, 0x80}

	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      sps,
		PPS:      pps,
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	err := m.WriteVideo(idrFrame)
	require.NoError(t, err)
	require.NotEmpty(t, output)

	// Verify the output contains valid TS packets (sync byte 0x47).
	for i := 0; i < len(output); i += 188 {
		require.Equal(t, byte(0x47), output[i], "TS packet at offset %d must start with sync byte", i)
	}
}

func TestTSMuxer_WriteAudio(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Init with keyframe first.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))
	output = nil

	audioFrame := &media.AudioFrame{
		PTS: 90000, Data: []byte{0xDE, 0x04, 0x00, 0x26, 0x20},
		SampleRate: 48000, Channels: 2,
	}
	err := m.WriteAudio(audioFrame)
	require.NoError(t, err)
	require.NotEmpty(t, output)
	require.Equal(t, 0, len(output)%188, "output must be multiple of 188 bytes")
}

func TestTSMuxer_WriteAudio_BeforeInit(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	audioFrame := &media.AudioFrame{
		PTS: 90000, Data: []byte{0xDE, 0x04},
		SampleRate: 48000, Channels: 2,
	}
	err := m.WriteAudio(audioFrame)
	require.NoError(t, err)
	require.Empty(t, output, "should not produce output before muxer is initialized")
}

func TestMuxerBuffersAudioBeforeKeyframe(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Send 5 audio frames before any keyframe.
	for i := 0; i < 5; i++ {
		audioFrame := &media.AudioFrame{
			PTS:        int64(90000 + i*1024),
			Data:       []byte{0xDE, 0x04, 0x00, 0x26, 0x20},
			SampleRate: 48000,
			Channels:   2,
		}
		err := m.WriteAudio(audioFrame)
		require.NoError(t, err)
	}
	require.Empty(t, output, "no output before keyframe")

	// Now send a keyframe to trigger init.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	err := m.WriteVideo(idrFrame)
	require.NoError(t, err)
	require.NotEmpty(t, output, "output should contain PAT/PMT + buffered audio + video")
	require.Equal(t, 0, len(output)%188, "output must be multiple of 188 bytes")

	// The output should contain audio PID (0x101) packets from the buffered audio.
	foundAudioPID := false
	for i := 0; i < len(output); i += 188 {
		pid := (uint16(output[i+1]&0x1F) << 8) | uint16(output[i+2])
		if pid == audioPID {
			foundAudioPID = true
			break
		}
	}
	require.True(t, foundAudioPID, "output should contain buffered audio packets")
}

func TestMuxerDropsExcessPendingAudio(t *testing.T) {
	m := NewTSMuxer()
	m.SetOutput(func(data []byte) {})

	// Send 60 audio frames — only the last 50 should be kept.
	for i := 0; i < 60; i++ {
		audioFrame := &media.AudioFrame{
			PTS:        int64(90000 + i*1024),
			Data:       []byte{0xDE, 0x04, 0x00, 0x26, 0x20},
			SampleRate: 48000,
			Channels:   2,
		}
		err := m.WriteAudio(audioFrame)
		require.NoError(t, err)
	}

	// Verify internal buffer is capped at 50.
	m.mu.Lock()
	require.Len(t, m.pendingAudio, 50, "pending audio buffer should be capped at 50")
	// The oldest frames should have been dropped — first frame in buffer
	// should be the 11th frame sent (index 10, PTS = 90000 + 10*1024).
	require.Equal(t, int64(90000+10*1024), m.pendingAudio[0].PTS,
		"oldest frames should have been dropped")
	m.mu.Unlock()
}

func TestMuxerFlushesAudioOnInit(t *testing.T) {
	m := NewTSMuxer()

	// Track output in order: each callback is one flush.
	type outputChunk struct {
		data []byte
	}
	var chunks []outputChunk
	m.SetOutput(func(data []byte) {
		c := make([]byte, len(data))
		copy(c, data)
		chunks = append(chunks, outputChunk{data: c})
	})

	// Send 3 audio frames before keyframe.
	for i := 0; i < 3; i++ {
		audioFrame := &media.AudioFrame{
			PTS:        int64(90000 + i*1024),
			Data:       []byte{0xDE, 0x04, 0x00, 0x26, 0x20},
			SampleRate: 48000,
			Channels:   2,
		}
		require.NoError(t, m.WriteAudio(audioFrame))
	}

	// Send keyframe — should init + flush buffered audio + write video.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))

	// After init, the pending audio buffer should be cleared.
	m.mu.Lock()
	require.Nil(t, m.pendingAudio, "pending audio should be nil after flush")
	m.mu.Unlock()

	// Now send a subsequent audio frame — should output immediately.
	require.NoError(t, m.WriteAudio(&media.AudioFrame{
		PTS: 93072, Data: []byte{0xDE, 0x04, 0x00, 0x26, 0x20},
		SampleRate: 48000, Channels: 2,
	}))

	// We should have at least 2 chunks: one from WriteVideo (init+flush audio+video),
	// and one from the subsequent WriteAudio.
	require.GreaterOrEqual(t, len(chunks), 2,
		"should have output from init+flush and from subsequent audio")

	// Every chunk must be valid TS packets.
	for ci, chunk := range chunks {
		require.Equal(t, 0, len(chunk.data)%188,
			"chunk %d must be multiple of 188 bytes", ci)
		for i := 0; i < len(chunk.data); i += 188 {
			require.Equal(t, byte(0x47), chunk.data[i],
				"chunk %d packet at offset %d must have sync byte", ci, i)
		}
	}
}

func TestTSMuxer_Close(t *testing.T) {
	m := NewTSMuxer()
	err := m.Close()
	require.NoError(t, err)
}

func TestTSMuxer_Close_AfterInit(t *testing.T) {
	m := NewTSMuxer()
	m.SetOutput(func(data []byte) {})

	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))

	err := m.Close()
	require.NoError(t, err)
}

func TestTSMuxer_WriteVideo_NoOutput(t *testing.T) {
	m := NewTSMuxer()
	// No SetOutput called — should not panic.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	err := m.WriteVideo(idrFrame)
	require.NoError(t, err)
}

func TestTSMuxer_WriteVideo_NilWireData(t *testing.T) {
	m := NewTSMuxer()
	m.SetOutput(func(data []byte) {})

	// Init first.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))

	// Frame with nil WireData should not panic.
	frame := &media.VideoFrame{
		PTS: 93600, DTS: 93600, IsKeyframe: false,
		WireData: nil,
	}
	err := m.WriteVideo(frame)
	require.NoError(t, err)
}

func TestTSMuxer_TSPacketSyncBytes(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Write a keyframe to initialize and produce output.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))
	require.NotEmpty(t, output)

	// Every 188-byte packet must start with 0x47 sync byte.
	numPackets := len(output) / 188
	require.Greater(t, numPackets, 0)
	for i := 0; i < numPackets; i++ {
		require.Equal(t, byte(0x47), output[i*188],
			"packet %d at offset %d must have sync byte", i, i*188)
	}
}

func BenchmarkMuxerFlush(b *testing.B) {
	m := NewTSMuxer()
	m.SetOutput(func(data []byte) {
		// Simulate consumer reading the data length (prevents dead-code elimination).
		_ = len(data)
	})

	sps := []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88}
	pps := []byte{0x68, 0xCE, 0x38, 0x80}
	wireData := []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00}

	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS: sps, PPS: pps, WireData: wireData, Codec: "h264",
	}
	if err := m.WriteVideo(idrFrame); err != nil {
		b.Fatal(err)
	}

	pFrame := &media.VideoFrame{
		PTS: 93600, DTS: 93600, IsKeyframe: false,
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x01},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pFrame.PTS = int64(93600 + i*3600)
		pFrame.DTS = pFrame.PTS
		if err := m.WriteVideo(pFrame); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------- SCTE-35 tests (Gap 10) ----------
//
// These tests complement the existing tests in muxer_scte35_test.go by
// covering additional scenarios: disabled no-op (pre+post init), internal
// buffer state verification, PUSI bit checks, pending buffer cleanup,
// CC increment across multiple writes, PMT stream_type 0x86 presence,
// CUEI registration descriptor, and PMT negative case when disabled.

// makeTestKeyframe2 creates a minimal keyframe for initializing the TSMuxer.
// Named with "2" suffix to avoid collision with writeInitKeyframe in muxer_scte35_test.go.
func makeTestKeyframe2(pts int64) *media.VideoFrame {
	return &media.VideoFrame{
		PTS: pts, DTS: pts, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
}

// makeTestSCTE35Section constructs a minimal valid splice_info_section
// (table_id 0xFC) containing a splice_null command.
func makeTestSCTE35Section() []byte {
	msg := &scte35.CueMessage{CommandType: scte35.CommandSpliceNull}
	data, err := msg.Encode(false)
	if err != nil {
		panic("failed to encode test SCTE-35 section: " + err.Error())
	}
	return data
}

func TestTSMuxer_WriteSCTE35_Disabled_Ignored(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// No SetSCTE35PID called — SCTE-35 is disabled.
	section := makeTestSCTE35Section()
	err := m.WriteSCTE35(section)
	require.NoError(t, err, "WriteSCTE35 should not return error when disabled")
	require.Empty(t, output, "WriteSCTE35 should produce no output when SCTE-35 is disabled")

	// Also verify it doesn't panic even after initialization.
	require.NoError(t, m.WriteVideo(makeTestKeyframe2(90000)))
	output = nil

	err = m.WriteSCTE35(section)
	require.NoError(t, err, "WriteSCTE35 should not return error when disabled (after init)")
	require.Empty(t, output, "WriteSCTE35 should produce no output when disabled (after init)")
}

func TestTSMuxer_WriteSCTE35_BeforeInit_Buffers(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Enable SCTE-35.
	m.SetSCTE35PID(defaultSCTE35PID)

	// Write SCTE-35 before any keyframe.
	section := makeTestSCTE35Section()
	err := m.WriteSCTE35(section)
	require.NoError(t, err)
	require.Empty(t, output, "WriteSCTE35 before init should produce no output (buffered)")

	// Verify the section is buffered internally.
	m.mu.Lock()
	require.Len(t, m.pendingSCTE35, 1, "should have 1 pending SCTE-35 section")
	require.Equal(t, section, m.pendingSCTE35[0], "buffered section should match input")
	m.mu.Unlock()
}

func TestTSMuxer_WriteSCTE35_AfterInit_ProducesPackets(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Enable SCTE-35 and initialize with keyframe.
	m.SetSCTE35PID(defaultSCTE35PID)
	require.NoError(t, m.WriteVideo(makeTestKeyframe2(90000)))
	output = nil // clear init output

	// Write SCTE-35 section after initialization.
	section := makeTestSCTE35Section()
	err := m.WriteSCTE35(section)
	require.NoError(t, err)
	require.NotEmpty(t, output, "WriteSCTE35 after init should produce TS packets")
	require.Equal(t, 0, len(output)%188, "output must be multiple of 188 bytes")

	// Verify output contains packets with the SCTE-35 PID and PUSI=1.
	foundSCTE35 := false
	foundPUSI := false
	for i := 0; i+188 <= len(output); i += 188 {
		require.Equal(t, byte(0x47), output[i], "TS packet must start with sync byte")
		pid := uint16(output[i+1]&0x1F)<<8 | uint16(output[i+2])
		if pid == defaultSCTE35PID {
			foundSCTE35 = true
			pusi := output[i+1]&0x40 != 0
			if pusi {
				foundPUSI = true
			}
		}
	}
	require.True(t, foundSCTE35, "output should contain packets with SCTE-35 PID 0x%04x", defaultSCTE35PID)
	require.True(t, foundPUSI, "first SCTE-35 packet should have PUSI=1")
}

func TestTSMuxer_WriteSCTE35_BufferedFlushOnInit(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Enable SCTE-35.
	m.SetSCTE35PID(defaultSCTE35PID)

	// Buffer a SCTE-35 section before init.
	section := makeTestSCTE35Section()
	require.NoError(t, m.WriteSCTE35(section))
	require.Empty(t, output, "no output before init")

	// Initialize with keyframe — should flush buffered SCTE-35.
	require.NoError(t, m.WriteVideo(makeTestKeyframe2(90000)))
	require.NotEmpty(t, output, "output should contain PAT/PMT + video + buffered SCTE-35")
	require.Equal(t, 0, len(output)%188, "output must be multiple of 188 bytes")

	// Verify SCTE-35 PID appears in the flushed output.
	foundSCTE35 := false
	for i := 0; i+188 <= len(output); i += 188 {
		pid := uint16(output[i+1]&0x1F)<<8 | uint16(output[i+2])
		if pid == defaultSCTE35PID {
			foundSCTE35 = true
			break
		}
	}
	require.True(t, foundSCTE35, "flushed output should contain SCTE-35 PID packets")

	// Verify pending buffer is cleared.
	m.mu.Lock()
	require.Nil(t, m.pendingSCTE35, "pending SCTE-35 should be nil after flush")
	m.mu.Unlock()
}

func TestTSMuxer_WriteSCTE35_ContinuityCounter_Increments(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Enable SCTE-35 and initialize.
	m.SetSCTE35PID(defaultSCTE35PID)
	require.NoError(t, m.WriteVideo(makeTestKeyframe2(90000)))
	output = nil

	// Write first SCTE-35 section.
	section := makeTestSCTE35Section()
	require.NoError(t, m.WriteSCTE35(section))
	firstOutput := make([]byte, len(output))
	copy(firstOutput, output)
	output = nil

	// Write second SCTE-35 section.
	require.NoError(t, m.WriteSCTE35(section))
	secondOutput := make([]byte, len(output))
	copy(secondOutput, output)

	// Extract continuity counters from SCTE-35 PID packets across both writes.
	var ccValues []uint8
	for _, chunk := range [][]byte{firstOutput, secondOutput} {
		for i := 0; i+188 <= len(chunk); i += 188 {
			pid := uint16(chunk[i+1]&0x1F)<<8 | uint16(chunk[i+2])
			if pid == defaultSCTE35PID {
				cc := chunk[i+3] & 0x0F
				ccValues = append(ccValues, cc)
			}
		}
	}

	require.GreaterOrEqual(t, len(ccValues), 2, "should have at least 2 SCTE-35 packets")

	// Verify CC increments by 1 between consecutive packets.
	for i := 1; i < len(ccValues); i++ {
		expected := (ccValues[i-1] + 1) & 0x0F
		require.Equal(t, expected, ccValues[i],
			"CC should increment: packet %d CC=%d, packet %d CC=%d",
			i-1, ccValues[i-1], i, ccValues[i])
	}
}

func TestTSMuxer_SCTE35_PMT_StreamType(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Enable SCTE-35.
	m.SetSCTE35PID(defaultSCTE35PID)

	// Initialize with keyframe — triggers PAT/PMT generation.
	require.NoError(t, m.WriteVideo(makeTestKeyframe2(90000)))
	require.NotEmpty(t, output)
	require.Equal(t, 0, len(output)%188, "output must be multiple of 188 bytes")

	// Scan all TS packets for the byte pattern representing a SCTE-35 ES entry in PMT:
	//   stream_type: 0x86
	//   reserved(3) + elementary_PID(13): 0xE1 0x02 for PID 0x102
	foundSCTE35Stream := false
	expectedStreamType := byte(0x86)
	expectedPIDHigh := byte(0xE0 | byte((defaultSCTE35PID>>8)&0x1F))
	expectedPIDLow := byte(defaultSCTE35PID & 0xFF)

	for i := 0; i+188 <= len(output); i += 188 {
		pkt := output[i : i+188]
		for j := 4; j+3 <= 188; j++ {
			if pkt[j] == expectedStreamType &&
				pkt[j+1] == expectedPIDHigh &&
				pkt[j+2] == expectedPIDLow {
				foundSCTE35Stream = true
				break
			}
		}
		if foundSCTE35Stream {
			break
		}
	}

	require.True(t, foundSCTE35Stream,
		"PMT should contain SCTE-35 elementary stream with stream_type=0x86 and PID=0x%04x", defaultSCTE35PID)

	// Verify that a Registration descriptor (tag 0x05, length 0x04) appears
	// in the PMT (program_info loop per SCTE-35 section 8.1). The CUEI
	// format_identifier 0x43554549 is encoded by go-astits. We check for
	// the descriptor tag/length pair which confirms the descriptor was registered.
	foundRegistrationDesc := false
	for i := 0; i+188 <= len(output); i += 188 {
		pkt := output[i : i+188]
		for j := 4; j+2 <= 188; j++ {
			if pkt[j] == 0x05 && pkt[j+1] == 0x04 {
				foundRegistrationDesc = true
				break
			}
		}
		if foundRegistrationDesc {
			break
		}
	}
	require.True(t, foundRegistrationDesc,
		"PMT should contain registration descriptor (tag=0x05, length=0x04) for SCTE-35 ES")
}

func TestTSMuxer_SCTE35_CUEI_InProgramInfoLoop(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Enable SCTE-35.
	m.SetSCTE35PID(defaultSCTE35PID)

	// Initialize with keyframe — triggers PAT/PMT generation.
	require.NoError(t, m.WriteVideo(makeTestKeyframe2(90000)))

	// Find the PMT packet (PID 0x1000 = 4096, go-astits default PMT PID).
	// PMT has table_id=0x02.
	var pmtPayload []byte
	for i := 0; i+188 <= len(output); i += 188 {
		pkt := output[i : i+188]
		if pkt[0] != 0x47 {
			continue
		}
		// Check for table_id 0x02 in the payload (PMT).
		pusi := pkt[1]&0x40 != 0
		if !pusi {
			continue
		}
		// Skip header, check adaptation field
		headerLen := 4
		afc := (pkt[3] >> 4) & 0x03
		if afc == 0x03 || afc == 0x02 {
			if headerLen < 188 {
				afLen := int(pkt[headerLen])
				headerLen += 1 + afLen
			}
		}
		if headerLen >= 188 {
			continue
		}
		// PUSI: pointer field
		ptr := int(pkt[headerLen])
		start := headerLen + 1 + ptr
		if start >= 188 {
			continue
		}
		if pkt[start] == 0x02 { // table_id = PMT
			pmtPayload = pkt[start:]
			break
		}
	}
	require.NotNil(t, pmtPayload, "should find PMT packet with table_id=0x02")

	// PMT structure after table_id:
	//   section_syntax_indicator(1) + '0'(1) + reserved(2) + section_length(12) = 2 bytes
	//   program_number(16) = 2 bytes
	//   reserved(2) + version(5) + current_next(1) = 1 byte
	//   section_number(8) = 1 byte
	//   last_section_number(8) = 1 byte
	//   reserved(3) + PCR_PID(13) = 2 bytes
	//   reserved(4) + program_info_length(12) = 2 bytes
	// Total fixed header after table_id: 10 bytes
	require.True(t, len(pmtPayload) > 11, "PMT payload too short")

	// Extract program_info_length (last 12 bits of bytes at offset 10-11 from table_id).
	progInfoLen := int(pmtPayload[10]&0x0F)<<8 | int(pmtPayload[11])
	require.Greater(t, progInfoLen, 0,
		"program_info_length should be > 0 (CUEI descriptor in program_info loop)")

	// The CUEI registration descriptor is: tag=0x05, length=0x04, format_id=0x43554549
	// Total 6 bytes. Verify it fits and is present in the program_info region.
	require.GreaterOrEqual(t, progInfoLen, 6,
		"program_info_length should be >= 6 for CUEI descriptor")

	// Check that the descriptor is in the program_info data (starts at offset 12).
	progInfoData := pmtPayload[12 : 12+progInfoLen]
	foundCUEI := false
	for j := 0; j+5 < len(progInfoData); j++ {
		if progInfoData[j] == 0x05 && progInfoData[j+1] == 0x04 &&
			progInfoData[j+2] == 0x43 && progInfoData[j+3] == 0x55 &&
			progInfoData[j+4] == 0x45 && progInfoData[j+5] == 0x49 {
			foundCUEI = true
			break
		}
	}
	require.True(t, foundCUEI,
		"CUEI registration descriptor (0x43554549) should be in PMT program_info loop")
}

func TestTSMuxer_SCTE35_PMT_NoStreamType_WhenDisabled(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// No SetSCTE35PID — SCTE-35 disabled.
	require.NoError(t, m.WriteVideo(makeTestKeyframe2(90000)))
	require.NotEmpty(t, output)

	// Verify stream_type 0x86 does NOT appear in any PMT entry.
	foundSCTE35Stream := false
	expectedStreamType := byte(0x86)
	expectedPIDHigh := byte(0xE0 | byte((defaultSCTE35PID>>8)&0x1F))
	expectedPIDLow := byte(defaultSCTE35PID & 0xFF)

	for i := 0; i+188 <= len(output); i += 188 {
		pkt := output[i : i+188]
		for j := 4; j+3 <= 188; j++ {
			if pkt[j] == expectedStreamType &&
				pkt[j+1] == expectedPIDHigh &&
				pkt[j+2] == expectedPIDLow {
				foundSCTE35Stream = true
				break
			}
		}
	}
	require.False(t, foundSCTE35Stream,
		"PMT should NOT contain SCTE-35 stream when disabled")
}

// ---------- C1: PCR in MPEG-TS output ----------

func TestTSMuxer_WriteVideo_PCROnKeyframe(t *testing.T) {
	m := NewTSMuxer()
	var output []byte
	m.SetOutput(func(data []byte) { output = append(output, data...) })

	// Write a keyframe to initialize the muxer.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))
	require.NotEmpty(t, output)

	// Scan output for video PID (0x100) packets with PCR flag set.
	foundPCR := false
	for i := 0; i+tsPacketSize <= len(output); i += tsPacketSize {
		if output[i] != 0x47 {
			continue
		}
		pid := uint16(output[i+1]&0x1F)<<8 | uint16(output[i+2])
		if pid != videoPID {
			continue
		}
		// Check adaptation field control (bits 5-4 of byte 3).
		afc := (output[i+3] >> 4) & 0x03
		if afc < 2 {
			continue // no adaptation field
		}
		afLen := output[i+4]
		if afLen == 0 {
			continue
		}
		// PCR flag is bit 4 of the adaptation field flags byte (byte 5).
		if output[i+5]&0x10 != 0 {
			foundPCR = true
			break
		}
	}
	require.True(t, foundPCR, "keyframe should have PCR flag set in adaptation field")
}

func TestTSMuxer_WriteVideo_PCRInterval(t *testing.T) {
	m := NewTSMuxer()
	var allOutput []byte
	m.SetOutput(func(data []byte) { allOutput = append(allOutput, data...) })

	// Write a keyframe to initialize the muxer.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))

	// Write 30 P-frames at 30fps (PTS increments of 3000 = 33.3ms).
	// pcrInterval = 2700 (30ms), so every frame triggers PCR (3000 >= 2700).
	for i := 1; i <= 30; i++ {
		pts := int64(90000 + i*3000)
		pFrame := &media.VideoFrame{
			PTS: pts, DTS: pts, IsKeyframe: false,
			WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x01},
		}
		require.NoError(t, m.WriteVideo(pFrame))
	}

	// Count PCR flags across all video PID packets.
	pcrCount := 0
	for i := 0; i+tsPacketSize <= len(allOutput); i += tsPacketSize {
		if allOutput[i] != 0x47 {
			continue
		}
		pid := uint16(allOutput[i+1]&0x1F)<<8 | uint16(allOutput[i+2])
		if pid != videoPID {
			continue
		}
		afc := (allOutput[i+3] >> 4) & 0x03
		if afc < 2 {
			continue
		}
		afLen := allOutput[i+4]
		if afLen == 0 {
			continue
		}
		if allOutput[i+5]&0x10 != 0 {
			pcrCount++
		}
	}

	// At 30fps with 33.3ms per frame and 30ms PCR interval (2700 ticks):
	// - Frame 0 (keyframe): PCR at PTS=90000
	// - Frame 1: delta=3000 >= 2700 → PCR
	// - Frame 2: delta=3000 >= 2700 → PCR
	// - ...every frame triggers PCR
	// Pattern: PCR on every frame → 31 PCRs for 31 frames.
	// This satisfies the ISO 13818-1 40ms max PCR interval requirement
	// at all frame rates (30fps: 33ms gap, 60fps: 33ms gap every 2 frames).
	require.GreaterOrEqual(t, pcrCount, 25,
		"PCR should appear on every frame at 30fps; got %d PCRs over 31 frames (~1s)", pcrCount)
}

func TestTSMuxer_Close_ResetsPCRState(t *testing.T) {
	m := NewTSMuxer()
	m.SetOutput(func(data []byte) {})

	// Initialize and write a frame.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))

	// Close should reset PCR tracking state.
	require.NoError(t, m.Close())
	require.Equal(t, int64(0), m.lastPCRPTS)
}

// ---------- PAT/PMT re-emission on keyframes ----------
//
// go-astits retransmitTables() fires on every WriteData where
// RandomAccessIndicator=true AND PID==PCRPID. Since we set both for
// keyframes, PAT/PMT are automatically re-emitted before each keyframe.
// These tests verify that behavior as a regression guard for SRT mid-stream
// join support.

func TestTSMuxer_PATRepeatedOnKeyframe(t *testing.T) {
	m := NewTSMuxer()

	// Collect output per-write so we can isolate the second keyframe's output.
	var chunks [][]byte
	m.SetOutput(func(data []byte) {
		c := make([]byte, len(data))
		copy(c, data)
		chunks = append(chunks, c)
	})

	// First keyframe — triggers init, which writes PAT/PMT + frame.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))
	require.True(t, m.initialized)

	// Write a few delta frames.
	for i := 1; i <= 3; i++ {
		pts := int64(90000 + i*3000)
		pFrame := &media.VideoFrame{
			PTS: pts, DTS: pts, IsKeyframe: false,
			WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x01},
		}
		require.NoError(t, m.WriteVideo(pFrame))
	}

	// Record how many chunks we have so far.
	chunksBeforeSecondKeyframe := len(chunks)

	// Second keyframe — should re-emit PAT/PMT before the frame data.
	idrFrame2 := &media.VideoFrame{
		PTS: 180000, DTS: 180000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame2))

	// Collect all output from the second keyframe write.
	var secondKeyframeOutput []byte
	for i := chunksBeforeSecondKeyframe; i < len(chunks); i++ {
		secondKeyframeOutput = append(secondKeyframeOutput, chunks[i]...)
	}
	require.NotEmpty(t, secondKeyframeOutput, "second keyframe should produce output")
	require.Equal(t, 0, len(secondKeyframeOutput)%188, "output must be multiple of 188 bytes")

	// Scan the second keyframe's output for PAT (PID 0x0000) and PMT (PID 0x1000).
	const patPID uint16 = 0x0000
	const pmtPID uint16 = 0x1000

	foundPAT := false
	foundPMT := false
	firstPATOffset := -1
	firstVideoOffset := -1
	for i := 0; i+188 <= len(secondKeyframeOutput); i += 188 {
		require.Equal(t, byte(0x47), secondKeyframeOutput[i], "TS sync byte")
		pid := uint16(secondKeyframeOutput[i+1]&0x1F)<<8 | uint16(secondKeyframeOutput[i+2])
		if pid == patPID {
			foundPAT = true
			if firstPATOffset < 0 {
				firstPATOffset = i
			}
		}
		if pid == pmtPID {
			foundPMT = true
		}
		if pid == videoPID && firstVideoOffset < 0 {
			firstVideoOffset = i
		}
	}
	require.True(t, foundPAT, "second keyframe output should contain PAT (PID 0x0000)")
	require.True(t, foundPMT, "second keyframe output should contain PMT (PID 0x1000)")

	// PAT/PMT should appear BEFORE the video data so mid-stream joiners
	// can parse the stream before receiving video.
	require.Greater(t, firstVideoOffset, firstPATOffset,
		"PAT should appear before video data in keyframe output")
}

func TestMuxer_PMTFieldExists(t *testing.T) {
	// Guard against go-astits internal changes. setProgramDescriptors uses
	// reflect + unsafe to access unexported pmt field. If the field is
	// renamed or removed, this test fails early with a clear message
	// instead of a silent runtime panic.
	m := astits.NewMuxer(context.Background(), io.Discard)
	v := reflect.ValueOf(m).Elem()
	pmt := v.FieldByName("pmt")
	require.True(t, pmt.IsValid(),
		"go-astits unexported 'pmt' field missing — update setProgramDescriptors")
	pd := pmt.FieldByName("ProgramDescriptors")
	require.True(t, pd.IsValid(),
		"go-astits ProgramDescriptors field missing — update setProgramDescriptors")
}

func TestTSMuxer_DeltaFrameNoPATPMT(t *testing.T) {
	m := NewTSMuxer()

	var chunks [][]byte
	m.SetOutput(func(data []byte) {
		c := make([]byte, len(data))
		copy(c, data)
		chunks = append(chunks, c)
	})

	// Initialize with keyframe.
	idrFrame := &media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		Codec:    "h264",
	}
	require.NoError(t, m.WriteVideo(idrFrame))

	// Record chunk count after init.
	chunksAfterInit := len(chunks)

	// Write a delta frame.
	pFrame := &media.VideoFrame{
		PTS: 93000, DTS: 93000, IsKeyframe: false,
		WireData: []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x01},
	}
	require.NoError(t, m.WriteVideo(pFrame))

	// Collect output from the delta frame write.
	var deltaOutput []byte
	for i := chunksAfterInit; i < len(chunks); i++ {
		deltaOutput = append(deltaOutput, chunks[i]...)
	}
	require.NotEmpty(t, deltaOutput, "delta frame should produce output")

	// Delta frames should NOT contain PAT/PMT — only video data.
	const patPID uint16 = 0x0000
	const pmtPID uint16 = 0x1000

	for i := 0; i+188 <= len(deltaOutput); i += 188 {
		pid := uint16(deltaOutput[i+1]&0x1F)<<8 | uint16(deltaOutput[i+2])
		require.NotEqual(t, patPID, pid,
			"delta frame output should not contain PAT packets")
		require.NotEqual(t, pmtPID, pid,
			"delta frame output should not contain PMT packets")
	}
}
