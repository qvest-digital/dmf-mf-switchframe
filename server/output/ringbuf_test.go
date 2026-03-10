package output

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRingBuffer_NewBuffer(t *testing.T) {
	t.Parallel()
	buf := newRingBuffer(1024)
	require.NotNil(t, buf)
	require.Equal(t, 0, buf.Len())
	require.False(t, buf.Overflowed())
}

func TestRingBuffer_WriteAndRead(t *testing.T) {
	t.Parallel()
	buf := newRingBuffer(1024)
	data := []byte("hello world")
	n, err := buf.Write(data)
	require.NoError(t, err)
	require.Equal(t, len(data), n)
	require.Equal(t, len(data), buf.Len())
	out := buf.ReadAll()
	require.Equal(t, data, out)
	require.Equal(t, 0, buf.Len())
}

func TestRingBuffer_MultipleWrites(t *testing.T) {
	t.Parallel()
	buf := newRingBuffer(1024)
	_, _ = buf.Write([]byte("hello "))
	_, _ = buf.Write([]byte("world"))
	out := buf.ReadAll()
	require.Equal(t, []byte("hello world"), out)
}

func TestRingBuffer_Overflow(t *testing.T) {
	t.Parallel()
	buf := newRingBuffer(10)
	data := []byte("1234567890abcdef") // 16 > 10
	_, _ = buf.Write(data)
	require.True(t, buf.Overflowed())
	out := buf.ReadAll()
	require.LessOrEqual(t, len(out), 10)
}

func TestRingBuffer_Reset(t *testing.T) {
	t.Parallel()
	buf := newRingBuffer(1024)
	_, _ = buf.Write([]byte("data"))
	buf.Reset()
	require.Equal(t, 0, buf.Len())
	require.False(t, buf.Overflowed())
}

func TestRingBuffer_ReadAllClearsOverflow(t *testing.T) {
	t.Parallel()
	buf := newRingBuffer(10)
	_, _ = buf.Write([]byte("1234567890abcdef"))
	require.True(t, buf.Overflowed())
	buf.ReadAll()
	require.False(t, buf.Overflowed())
}

func TestRingBuffer_EmptyRead(t *testing.T) {
	t.Parallel()
	buf := newRingBuffer(1024)
	out := buf.ReadAll()
	require.Nil(t, out)
}

func TestRingBuffer_ExactCapacity(t *testing.T) {
	t.Parallel()
	buf := newRingBuffer(5)
	_, _ = buf.Write([]byte("12345"))
	require.False(t, buf.Overflowed())
	require.Equal(t, 5, buf.Len())
	out := buf.ReadAll()
	require.Equal(t, []byte("12345"), out)
}

func TestRingBuffer_WrapAround(t *testing.T) {
	t.Parallel()
	buf := newRingBuffer(8)
	_, _ = buf.Write([]byte("AAAAA"))
	buf.ReadAll()
	_, _ = buf.Write([]byte("BBBBBB"))
	require.Equal(t, 6, buf.Len())
	out := buf.ReadAll()
	require.Equal(t, []byte("BBBBBB"), out)
}

// makeTaggedTSPacket creates a 188-byte TS packet with sync byte 0x47 and a
// tag byte at position 1 for identification.
func makeTaggedTSPacket(tag byte) []byte {
	pkt := make([]byte, 188)
	pkt[0] = 0x47 // TS sync byte
	pkt[1] = tag
	return pkt
}

func TestRingBuffer_OverflowTSPacketAlignment(t *testing.T) {
	t.Parallel()

	// Use a capacity that holds exactly 5 TS packets (940 bytes).
	// This ensures the buffer is TS-aligned initially.
	capacity := 188 * 5 // 940 bytes
	buf := newRingBuffer(capacity)

	// Fill the buffer with 5 TS packets (tags 1-5). Buffer is now full.
	for i := byte(1); i <= 5; i++ {
		_, _ = buf.Write(makeTaggedTSPacket(i))
	}
	require.False(t, buf.Overflowed(), "buffer should not overflow when exactly full")
	require.Equal(t, capacity, buf.Len())

	// Write a partial-packet-sized chunk to cause overflow by a non-188-aligned
	// amount. Writing 100 bytes overflows by 100 bytes, which is not a multiple
	// of 188. Without alignment fix, readPos lands 100 bytes into packet 1
	// (mid-packet).
	overflow := make([]byte, 100)
	for i := range overflow {
		overflow[i] = 0xAA
	}
	_, _ = buf.Write(overflow)
	require.True(t, buf.Overflowed(), "should overflow")

	// ReadAll must return data that starts at a TS packet boundary.
	// After the fix, readPos should be rounded up to the next 188-byte
	// boundary, so the first readable byte should be a TS sync byte (0x47).
	out := buf.ReadAll()
	require.NotEmpty(t, out, "should have data after overflow")
	require.Equal(t, byte(0x47), out[0],
		"first byte after overflow must be TS sync byte 0x47, got 0x%02X (readPos not TS-aligned)", out[0])
	require.Equal(t, 0, len(out)%188,
		"readable length must be a multiple of 188 bytes, got %d", len(out))
}

func TestRingBuffer_OverflowTSAlignmentMultipleOverflows(t *testing.T) {
	t.Parallel()

	// Buffer holds 10 TS packets.
	capacity := 188 * 10
	buf := newRingBuffer(capacity)

	// Fill completely with packets tagged 1-10.
	for i := byte(1); i <= 10; i++ {
		_, _ = buf.Write(makeTaggedTSPacket(i))
	}

	// Cause overflow with 3 full packets + 50 extra bytes.
	// This overflows by 3*188+50 = 614 bytes. Without alignment, readPos
	// would land 50 bytes into a packet.
	for i := byte(11); i <= 13; i++ {
		_, _ = buf.Write(makeTaggedTSPacket(i))
	}
	extra := make([]byte, 50)
	extra[0] = 0xFF // not a sync byte
	_, _ = buf.Write(extra)

	require.True(t, buf.Overflowed())

	out := buf.ReadAll()
	require.NotEmpty(t, out)
	require.Equal(t, byte(0x47), out[0],
		"first byte must be TS sync byte after multi-write overflow, got 0x%02X", out[0])
	require.Equal(t, 0, len(out)%188,
		"readable length must be a multiple of 188, got %d", len(out))
}

func TestRingBuffer_OverflowTSAlignmentAlreadyAligned(t *testing.T) {
	t.Parallel()

	// When overflow is exactly a multiple of 188, readPos should already
	// be aligned and no extra adjustment is needed.
	capacity := 188 * 5
	buf := newRingBuffer(capacity)

	for i := byte(1); i <= 5; i++ {
		_, _ = buf.Write(makeTaggedTSPacket(i))
	}

	// Overflow by exactly 1 TS packet (188 bytes) — already aligned.
	_, _ = buf.Write(makeTaggedTSPacket(6))
	require.True(t, buf.Overflowed())

	out := buf.ReadAll()
	require.NotEmpty(t, out)
	require.Equal(t, byte(0x47), out[0],
		"first byte must be TS sync byte when overflow is packet-aligned, got 0x%02X", out[0])
	require.Equal(t, 0, len(out)%188,
		"readable length must be a multiple of 188, got %d", len(out))
}
