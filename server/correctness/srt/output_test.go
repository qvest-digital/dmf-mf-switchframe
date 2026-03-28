//go:build correctness

package srt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	srtgo "github.com/zsiec/srtgo"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
)

// encodeVideoFrames encodes N video frames using libx264 and returns encoded media.VideoFrame slices.
// Each keyframe has SPS/PPS extracted. Content varies per frame for realistic bitrates.
func encodeVideoFrames(t *testing.T, w, h, fps, gopSecs, bitrate, numFrames int, startPTS int64) []*media.VideoFrame {
	t.Helper()

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	ptsDelta := int64(90000 / fps)
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	var frames []*media.VideoFrame
	for i := 0; i < numFrames; i++ {
		pts := startPTS + int64(i)*ptsDelta
		forceIDR := (i % (fps * gopSecs)) == 0

		// Vary content each frame.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if len(encoded) == 0 {
			continue
		}

		avc1 := codec.AnnexBToAVC1(encoded)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
		}

		if isKeyframe {
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7:
					frame.SPS = append([]byte(nil), nalu...)
				case 8:
					frame.PPS = append([]byte(nil), nalu...)
				}
			}
		}

		frames = append(frames, frame)
	}

	return frames
}

// TestSRTOutputProducesValidTS creates an SRT loopback: muxer -> SRT push -> SRT listener -> capture -> ffprobe.
// Validates that TS data transiting SRT is valid H.264 with correct frame count.
func TestSRTOutputProducesValidTS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Encode 90 video frames (3s at 30fps).
	frames := encodeVideoFrames(t, 320, 240, 30, 1, 500_000, 90, 90000)
	require.GreaterOrEqual(t, len(frames), 60, "encoder should produce at least 60 frames")
	t.Logf("MEASURE: encoded %d video frames for SRT transit", len(frames))

	// Find free port.
	addrStr := findFreeUDPAddr(t) // returns "127.0.0.1:PORT"
	var port int
	_, err := fmt.Sscanf(addrStr, "127.0.0.1:%d", &port)
	require.NoError(t, err, "parse port from %s", addrStr)

	// Start SRT listener (receiver side).
	cfg := srtgo.DefaultConfig()
	cfg.Latency = 120 * time.Millisecond
	ln, err := srtgo.Listen(addrStr, cfg)
	require.NoError(t, err, "srt listen on %s", addrStr)
	defer ln.Close()

	// Channel to collect received data.
	var receivedData []byte
	var receiveMu sync.Mutex
	receiveDone := make(chan struct{})

	// Accept connections in background.
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 65536)
		for {
			n, readErr := conn.Read(buf)
			if readErr != nil {
				break
			}
			if n > 0 {
				receiveMu.Lock()
				receivedData = append(receivedData, buf[:n]...)
				receiveMu.Unlock()
			}
		}
		close(receiveDone)
	}()

	// Give listener time to be ready.
	time.Sleep(200 * time.Millisecond)

	// Connect SRT caller (sender side).
	dialCfg := srtgo.DefaultConfig()
	dialCfg.Latency = 120 * time.Millisecond
	senderConn, err := srtgo.Dial(fmt.Sprintf("127.0.0.1:%d", port), dialCfg)
	require.NoError(t, err, "srt dial")

	// Create muxer, wire output to SRT sender.
	muxer := output.NewTSMuxer()
	var sentBytes int64
	muxer.SetOutput(func(data []byte) {
		// SRT live mode: chunk into 1316-byte (7 * 188) packets.
		for len(data) > 0 {
			chunk := data
			if len(chunk) > 1316 {
				chunk = data[:1316]
			}
			n, writeErr := senderConn.Write(chunk)
			if writeErr != nil {
				return
			}
			sentBytes += int64(n)
			data = data[len(chunk):]
		}
	})

	// Mux all frames.
	videoFramesSent := 0
	for _, frame := range frames {
		muxErr := muxer.WriteVideo(frame)
		require.NoError(t, muxErr, "mux video frame %d", videoFramesSent)
		videoFramesSent++
	}

	t.Logf("MEASURE: sent %d video frames, %d bytes via SRT", videoFramesSent, sentBytes)

	// Wait for SRT internal buffers to flush before closing.
	// SRT live mode has a latency buffer (120ms) plus network stack latency.
	time.Sleep(1 * time.Second)

	// Close sender to signal EOF to receiver.
	_ = senderConn.Close()

	// Wait for receiver to finish (with timeout).
	select {
	case <-receiveDone:
	case <-time.After(10 * time.Second):
		t.Log("MEASURE: receiver did not finish within timeout, proceeding with partial data")
	}

	receiveMu.Lock()
	capturedLen := len(receivedData)
	captured := make([]byte, capturedLen)
	copy(captured, receivedData)
	receiveMu.Unlock()

	t.Logf("MEASURE: sent %d video frames, sender wrote %d bytes, receiver got %d bytes", videoFramesSent, sentBytes, capturedLen)

	require.Greater(t, capturedLen, 0, "receiver should have captured some data")

	// Write to temp file and validate with ffprobe.
	dir := t.TempDir()
	path := filepath.Join(dir, "srt_output.ts")
	require.NoError(t, os.WriteFile(path, captured, 0o644))

	// ffprobe: validate the captured TS is parseable.
	testutil.RequireNoErrors(t, path)

	result := testutil.ProbeFile(t, path)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in SRT-captured TS")
	t.Logf("MEASURE: ffprobe codec=%s, resolution=%dx%d", vs.CodecName, vs.Width, vs.Height)
	require.Equal(t, "h264", vs.CodecName)

	// Check frame count.
	probeFrames := testutil.ProbeVideoFrames(t, path)
	t.Logf("MEASURE: sent %d video frames, receiver got %d bytes, ffprobe frames=%d", videoFramesSent, capturedLen, len(probeFrames))
	require.Greater(t, len(probeFrames), 50, "ffprobe should find > 50 video frames from 90 encoded")
}

// TestSRTOutputReconnectionIDRGating tests the SRT output adapter's IDR gating behavior
// after reconnection. This test verifies two things:
//
// 1. The ContainsKeyframe function correctly identifies keyframe vs non-keyframe TS data
// 2. After an SRT reconnection, the SRTCaller adapter gates (drops) non-keyframe TS data
//    until a keyframe arrives, ensuring the remote decoder can cleanly start decoding.
//
// We use the SRTCaller with a mock connection to control reconnection timing precisely.
func TestSRTOutputReconnectionIDRGating(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Encode 90 frames with 1s GOP at 30fps. Keyframes at frames 0, 30, 60.
	frames := encodeVideoFrames(t, 320, 240, 30, 1, 500_000, 90, 90000)
	require.GreaterOrEqual(t, len(frames), 60, "encoder should produce at least 60 frames")

	// Mux all frames to TS to get the raw TS bytes with proper adaptation fields.
	muxer := output.NewTSMuxer()
	type tsChunk struct {
		data       []byte
		isKeyframe bool
		frameIdx   int
	}
	var chunks []tsChunk
	currentFrameIdx := 0
	muxer.SetOutput(func(data []byte) {
		cp := make([]byte, len(data))
		copy(cp, data)
		isKF := false
		if currentFrameIdx < len(frames) {
			isKF = frames[currentFrameIdx].IsKeyframe
		}
		chunks = append(chunks, tsChunk{
			data:       cp,
			isKeyframe: isKF,
			frameIdx:   currentFrameIdx,
		})
	})

	for _, frame := range frames {
		err := muxer.WriteVideo(frame)
		require.NoError(t, err)
		currentFrameIdx++
	}

	t.Logf("MEASURE: generated %d TS chunks from %d video frames", len(chunks), len(frames))

	// Verify ContainsKeyframe identifies keyframes correctly.
	keyframeChunks := 0
	nonKeyframeChunks := 0
	for _, chunk := range chunks {
		if output.ContainsKeyframe(chunk.data) {
			keyframeChunks++
		} else {
			nonKeyframeChunks++
		}
	}
	t.Logf("MEASURE: chunks with RAI (keyframe)=%d, without RAI=%d", keyframeChunks, nonKeyframeChunks)

	require.Greater(t, keyframeChunks, 0,
		"ContainsKeyframe should find at least one keyframe in the TS data")
	require.Greater(t, nonKeyframeChunks, 0,
		"should have non-keyframe chunks to test IDR gating")

	// Verify that keyframe chunks match the encoder's keyframe positions.
	for _, chunk := range chunks {
		hasRAI := output.ContainsKeyframe(chunk.data)
		t.Logf("MEASURE: chunk frameIdx=%d, encoder_keyframe=%v, TS_RAI=%v",
			chunk.frameIdx, chunk.isKeyframe, hasRAI)
		if chunk.isKeyframe {
			require.True(t, hasRAI,
				"chunk for encoder keyframe at frameIdx=%d should have RAI in TS adaptation field",
				chunk.frameIdx)
		}
	}

	// Test the SRTCaller's IDR gating with a mock connection.
	// After reconnection, the caller should drop P-frame writes until seeing a keyframe.
	var mu sync.Mutex
	var writtenData [][]byte
	var writeErr error
	connectCount := 0

	caller := output.NewSRTCaller(output.SRTCallerConfig{
		Address:        "127.0.0.1",
		Port:           9999,
		RingBufferSize: 4 * 1024 * 1024,
	})

	caller.SetConnectFn(func(ctx context.Context, config output.SRTCallerConfig) (output.SrtConn, error) {
		mu.Lock()
		connectCount++
		mu.Unlock()
		return &mockConn{
			writeFn: func(data []byte) (int, error) {
				mu.Lock()
				defer mu.Unlock()
				if writeErr != nil {
					return 0, writeErr
				}
				cp := make([]byte, len(data))
				copy(cp, data)
				writtenData = append(writtenData, cp)
				return len(data), nil
			},
		}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := caller.Start(ctx)
	require.NoError(t, err)
	defer caller.Close()

	// Feed the first GOP (frames 0-29) normally.
	for _, chunk := range chunks {
		if chunk.frameIdx >= 30 {
			break
		}
		_, writeErr2 := caller.Write(chunk.data)
		require.NoError(t, writeErr2)
	}

	mu.Lock()
	preDisconnectWrites := len(writtenData)
	mu.Unlock()
	t.Logf("MEASURE: writes before disconnect=%d", preDisconnectWrites)

	// Simulate a write failure to trigger reconnection.
	mu.Lock()
	writeErr = fmt.Errorf("simulated disconnect")
	mu.Unlock()

	// Write one more chunk to trigger the reconnection.
	_, _ = caller.Write(chunks[30].data)

	// Allow reconnection to happen. The mock connect function resolves immediately
	// but the reconnect loop has a backoff delay.
	mu.Lock()
	writeErr = nil // Clear error so reconnection succeeds.
	writtenData = nil // Reset to track post-reconnect writes only.
	mu.Unlock()

	// Wait for reconnect.
	time.Sleep(2 * time.Second)

	// Now feed P-frames (non-keyframe chunks) followed by a keyframe.
	// The IDR gate should drop the P-frames.
	pFramesFed := 0
	for _, chunk := range chunks {
		if chunk.frameIdx >= 31 && chunk.frameIdx < 60 && !chunk.isKeyframe {
			_, _ = caller.Write(chunk.data)
			pFramesFed++
			if pFramesFed >= 5 {
				break
			}
		}
	}
	t.Logf("MEASURE: fed %d P-frame chunks after reconnect (should be gated)", pFramesFed)

	mu.Lock()
	writesAfterPFrames := len(writtenData)
	mu.Unlock()

	// Now feed a keyframe chunk (the 2nd GOP keyframe).
	for _, chunk := range chunks {
		if chunk.isKeyframe && chunk.frameIdx >= 30 {
			_, _ = caller.Write(chunk.data)
			t.Logf("MEASURE: fed keyframe at frameIdx=%d", chunk.frameIdx)
			break
		}
	}

	// Feed some more chunks after the keyframe.
	postKeyCount := 0
	for _, chunk := range chunks {
		if chunk.frameIdx > 30 && chunk.frameIdx <= 40 {
			_, _ = caller.Write(chunk.data)
			postKeyCount++
		}
	}
	t.Logf("MEASURE: fed %d chunks after keyframe", postKeyCount)

	mu.Lock()
	writesTotal := len(writtenData)
	// Check if any P-frames leaked through before the keyframe.
	firstWriteIsKeyframe := false
	if len(writtenData) > 0 {
		firstWriteIsKeyframe = output.ContainsKeyframe(writtenData[0])
	}
	mu.Unlock()

	t.Logf("MEASURE: frames after reconnect, total_writes=%d, writes_after_pframes_only=%d, first_is_keyframe=%v",
		writesTotal, writesAfterPFrames, firstWriteIsKeyframe)

	// The IDR gate should have dropped the P-frames written after reconnect.
	// The P-frames fed should not have reached the connection.
	t.Logf("MEASURE: writes_after_pframes_only=%d (expected 0 if IDR gate works)", writesAfterPFrames)

	// After feeding the keyframe, writes should resume.
	require.Greater(t, writesTotal, 0,
		"some data should have been written after feeding keyframe post-reconnect")

	// Verify the first write after reconnect contains a keyframe.
	require.True(t, firstWriteIsKeyframe,
		"first write after SRT reconnection should contain a keyframe (RAI), IDR gating may be broken")
}

// mockConn implements srtConn for testing.
type mockConn struct {
	writeFn func(data []byte) (int, error)
}

func (m *mockConn) Write(data []byte) (int, error) {
	if m.writeFn != nil {
		return m.writeFn(data)
	}
	return len(data), nil
}

func (m *mockConn) Close() {}

// TestSRTOutputRingBufferOverflow tests the ring buffer's overflow behavior.
func TestSRTOutputRingBufferOverflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Create a small ring buffer (1024 bytes = ~5 TS packets).
	rb := output.NewTestRingBuffer(1024)

	// Build valid TS-aligned data: 30 complete packets (5640 bytes).
	var bigData []byte
	for i := 0; i < 30; i++ {
		pkt := make([]byte, 188)
		pkt[0] = 0x47 // Sync byte
		pkt[1] = 0x01
		pkt[2] = 0x00
		pkt[3] = 0x10 | byte(i%16)
		// Fill with identifiable data.
		for j := 4; j < 188; j++ {
			pkt[j] = byte(i)
		}
		bigData = append(bigData, pkt...)
	}

	totalWritten := len(bigData) // 5640 bytes
	n, err := rb.Write(bigData)

	t.Logf("MEASURE: buffer capacity=%d, written=%d, write_returned_n=%d, err=%v", 1024, totalWritten, n, err)

	// Write should succeed (ring buffer never errors).
	require.NoError(t, err, "ring buffer write should not error")
	require.Equal(t, totalWritten, n, "ring buffer should report full write")

	// Check overflow flag BEFORE ReadAll (which clears it).
	overflowed := rb.Overflowed()
	t.Logf("MEASURE: overflowed=%v (before ReadAll)", overflowed)
	require.True(t, overflowed, "ring buffer should have overflowed since %d > 1024", totalWritten)

	// Read back.
	readback := rb.ReadAll()
	readable := len(readback)
	t.Logf("MEASURE: buffer capacity=%d, written=%d, readable=%d", 1024, totalWritten, readable)

	// Readable data should be <= capacity.
	require.LessOrEqual(t, readable, 1024,
		"readable data should not exceed buffer capacity")

	// Readable data should be a multiple of 188 (TS packet alignment).
	t.Logf("MEASURE: readable=%d, is_ts_aligned=%v", readable, readable%188 == 0)
	require.Equal(t, 0, readable%188,
		"ring buffer should return TS-packet-aligned data after overflow, got %d bytes (remainder %d)", readable, readable%188)

	// Verify the data starts with a sync byte.
	if readable > 0 {
		require.Equal(t, byte(0x47), readback[0],
			"ring buffer read data should start with TS sync byte 0x47, got 0x%02X", readback[0])
	}

	// Verify all 188-byte boundaries have sync bytes.
	for i := 0; i+188 <= readable; i += 188 {
		require.Equal(t, byte(0x47), readback[i],
			"packet at offset %d should start with sync byte 0x47, got 0x%02X", i, readback[i])
	}

	// After ReadAll, buffer should be empty and overflow cleared.
	require.Equal(t, 0, rb.Len(), "buffer should be empty after ReadAll")
	require.False(t, rb.Overflowed(), "overflow flag should be cleared after ReadAll")
}

// TestSRTOutputRingBufferOverflowNoPanic tests that massive writes don't panic.
func TestSRTOutputRingBufferOverflowNoPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Tiny buffer (512 bytes).
	rb := output.NewTestRingBuffer(512)

	// Massive write that exceeds capacity by 10x.
	var massiveData []byte
	for i := 0; i < 30; i++ {
		pkt := make([]byte, 188)
		pkt[0] = 0x47
		pkt[1] = byte(i >> 8)
		pkt[2] = byte(i)
		pkt[3] = 0x10
		for j := 4; j < 188; j++ {
			pkt[j] = byte(i)
		}
		massiveData = append(massiveData, pkt...)
	}

	require.NotPanics(t, func() {
		n, writeErr := rb.Write(massiveData)
		t.Logf("MEASURE: massive write of %d bytes to 512-byte buffer: n=%d, err=%v", len(massiveData), n, writeErr)
		require.NoError(t, writeErr)
		require.Equal(t, len(massiveData), n)
	}, "ring buffer should not panic on massive overflow")

	readback := rb.ReadAll()
	t.Logf("MEASURE: after massive write, readable=%d bytes", len(readback))

	// Should have data and it should start with sync byte.
	if len(readback) > 0 {
		require.Equal(t, byte(0x47), readback[0],
			"readback should start with TS sync byte")
	}
}

// TestSRTOutputRingBufferMultipleWriteOverflow writes in multiple small chunks
// to test overflow behavior across incremental writes.
func TestSRTOutputRingBufferMultipleWriteOverflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Buffer holds ~5 TS packets (940 bytes fits in 1024).
	rb := output.NewTestRingBuffer(1024)

	// Write 20 individual TS packets, each 188 bytes.
	for i := 0; i < 20; i++ {
		pkt := make([]byte, 188)
		pkt[0] = 0x47
		pkt[1] = 0x01
		pkt[2] = 0x00
		pkt[3] = 0x10 | byte(i%16)
		for j := 4; j < 188; j++ {
			pkt[j] = byte(i)
		}
		n, writeErr := rb.Write(pkt)
		require.NoError(t, writeErr, "write %d", i)
		require.Equal(t, 188, n, "write %d should report 188 bytes", i)
	}

	totalWritten := 20 * 188 // 3760 bytes into 1024-byte buffer.
	overflowed := rb.Overflowed()

	readback := rb.ReadAll()
	readable := len(readback)
	t.Logf("MEASURE: capacity=1024, total_written=%d (20 packets), readable=%d, overflowed=%v",
		totalWritten, readable, overflowed)

	// Should have overflowed.
	require.True(t, overflowed, "20 packets (3760 bytes) into 1024-byte buffer should overflow")

	// Data should be TS-aligned.
	if readable > 0 {
		require.Equal(t, 0, readable%188,
			"readable data should be TS-packet-aligned, got %d bytes (remainder %d)", readable, readable%188)
		require.Equal(t, byte(0x47), readback[0],
			"first byte should be sync byte 0x47")
	}

	// The most recent packets should be present (they're the ones not overwritten).
	// With 1024 bytes, we can hold 5 complete TS packets (940 bytes).
	maxPackets := 1024 / 188 // 5
	actualPackets := readable / 188
	t.Logf("MEASURE: expected max ~%d packets in buffer, got %d packets", maxPackets, actualPackets)
	require.LessOrEqual(t, actualPackets, maxPackets+1,
		"should have at most %d packets, got %d", maxPackets+1, actualPackets)

	// Verify we got the most recent data (last packet should have fill byte 19).
	if actualPackets > 0 {
		lastPkt := readback[(actualPackets-1)*188 : actualPackets*188]
		lastFillByte := lastPkt[4] // first data byte after header
		t.Logf("MEASURE: last packet fill byte=%d (expected 19 if most recent data preserved)", lastFillByte)
		// The last packet written was packet 19, so if the ring buffer preserves
		// the most recent data, the last packet's fill byte should be 19.
		require.Equal(t, byte(19), lastFillByte,
			"ring buffer should preserve most recent data; last packet fill byte should be 19, got %d", lastFillByte)
	}
}
