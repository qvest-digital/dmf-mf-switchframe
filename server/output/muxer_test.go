package output

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
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
