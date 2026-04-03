package output

import (
	"context"

	"github.com/zsiec/switchframe/server/internal/mpegts"
)

// SrtConn is the exported version of srtConn for external test packages.
type SrtConn = srtConn

// SetConnectFn sets the connection function on an SRTCaller for testing.
// This allows tests to inject mock connections or use real SRT connections.
func (c *SRTCaller) SetConnectFn(fn func(ctx context.Context, config SRTCallerConfig) (SrtConn, error)) {
	c.connectFn = fn
}

// TestRingBuffer provides access to the internal ring buffer for correctness tests.
// This is a thin wrapper around the unexported ringBuffer type.
type TestRingBuffer struct {
	inner *ringBuffer
}

// NewTestRingBuffer creates a ring buffer with the given capacity for testing.
func NewTestRingBuffer(capacity int) *TestRingBuffer {
	return &TestRingBuffer{inner: newRingBuffer(capacity)}
}

// Write writes data to the ring buffer.
func (r *TestRingBuffer) Write(p []byte) (int, error) {
	return r.inner.Write(p)
}

// ReadAll reads all available data from the ring buffer.
func (r *TestRingBuffer) ReadAll() []byte {
	return r.inner.ReadAll()
}

// Len returns the number of unread bytes.
func (r *TestRingBuffer) Len() int {
	return r.inner.Len()
}

// Overflowed returns whether the buffer has overflowed since last ReadAll.
func (r *TestRingBuffer) Overflowed() bool {
	return r.inner.Overflowed()
}

// Reset clears the buffer.
func (r *TestRingBuffer) Reset() {
	r.inner.Reset()
}

// ContainsKeyframe exposes mpegts.ContainsKeyframe for testing.
func ContainsKeyframe(data []byte) bool {
	return mpegts.ContainsKeyframe(data)
}
