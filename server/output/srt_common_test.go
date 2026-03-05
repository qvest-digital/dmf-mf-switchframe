package output

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingConn records individual write sizes to verify chunking.
type recordingConn struct {
	mu         sync.Mutex
	writeSizes []int
	totalBytes int
	maxWrite   int // if >0, reject writes above this size
}

func (r *recordingConn) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxWrite > 0 && len(data) > r.maxWrite {
		return 0, fmt.Errorf("payload size %d exceeds maximum %d for live mode", len(data), r.maxWrite)
	}
	r.writeSizes = append(r.writeSizes, len(data))
	r.totalBytes += len(data)
	return len(data), nil
}

func (r *recordingConn) Close() {}

func TestChunkedConn_SmallWrite(t *testing.T) {
	inner := &recordingConn{}
	cc := &chunkedConn{inner: inner}

	// A single TS packet (188 bytes) should pass through as-is.
	data := make([]byte, 188)
	n, err := cc.Write(data)
	require.NoError(t, err)
	assert.Equal(t, 188, n)
	assert.Equal(t, []int{188}, inner.writeSizes)
}

func TestChunkedConn_ExactMaxPayload(t *testing.T) {
	inner := &recordingConn{}
	cc := &chunkedConn{inner: inner}

	// Exactly 1316 bytes (7 TS packets) — single write.
	data := make([]byte, srtLiveMaxPayload)
	n, err := cc.Write(data)
	require.NoError(t, err)
	assert.Equal(t, srtLiveMaxPayload, n)
	assert.Equal(t, []int{srtLiveMaxPayload}, inner.writeSizes)
}

func TestChunkedConn_LargeWriteChunked(t *testing.T) {
	inner := &recordingConn{}
	cc := &chunkedConn{inner: inner}

	// 20 TS packets = 3760 bytes → should be split into 7+7+6 = 1316+1316+1128
	data := make([]byte, 20*188)
	n, err := cc.Write(data)
	require.NoError(t, err)
	assert.Equal(t, 20*188, n)
	assert.Equal(t, []int{1316, 1316, 6 * 188}, inner.writeSizes)
}

func TestChunkedConn_VeryLargeKeyframe(t *testing.T) {
	inner := &recordingConn{}
	cc := &chunkedConn{inner: inner}

	// Simulate a large keyframe (66176 bytes from the logs) — ~352 TS packets.
	size := 66176
	data := make([]byte, size)
	n, err := cc.Write(data)
	require.NoError(t, err)
	assert.Equal(t, size, n)

	// Every chunk should be ≤ srtLiveMaxPayload
	for i, sz := range inner.writeSizes {
		assert.LessOrEqual(t, sz, srtLiveMaxPayload, "chunk %d too large: %d", i, sz)
	}
	assert.Equal(t, size, inner.totalBytes)
}

func TestChunkedConn_InnerEnforcesLimit(t *testing.T) {
	// Inner connection rejects writes > 1316 (simulates real SRT live mode).
	inner := &recordingConn{maxWrite: srtLiveMaxPayload}
	cc := &chunkedConn{inner: inner}

	// Without chunking this would fail. With chunking it should succeed.
	data := make([]byte, 14852) // payload size from the logs
	n, err := cc.Write(data)
	require.NoError(t, err)
	assert.Equal(t, 14852, n)

	for i, sz := range inner.writeSizes {
		assert.LessOrEqual(t, sz, srtLiveMaxPayload, "chunk %d too large: %d", i, sz)
	}
}

func TestChunkedConn_EmptyWrite(t *testing.T) {
	inner := &recordingConn{}
	cc := &chunkedConn{inner: inner}

	n, err := cc.Write(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Empty(t, inner.writeSizes)
}
