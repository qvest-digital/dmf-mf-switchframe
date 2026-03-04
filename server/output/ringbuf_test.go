package output

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRingBuffer_NewBuffer(t *testing.T) {
	buf := newRingBuffer(1024)
	require.NotNil(t, buf)
	require.Equal(t, 0, buf.Len())
	require.False(t, buf.Overflowed())
}

func TestRingBuffer_WriteAndRead(t *testing.T) {
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
	buf := newRingBuffer(1024)
	buf.Write([]byte("hello "))
	buf.Write([]byte("world"))
	out := buf.ReadAll()
	require.Equal(t, []byte("hello world"), out)
}

func TestRingBuffer_Overflow(t *testing.T) {
	buf := newRingBuffer(10)
	data := []byte("1234567890abcdef") // 16 > 10
	buf.Write(data)
	require.True(t, buf.Overflowed())
	out := buf.ReadAll()
	require.LessOrEqual(t, len(out), 10)
}

func TestRingBuffer_Reset(t *testing.T) {
	buf := newRingBuffer(1024)
	buf.Write([]byte("data"))
	buf.Reset()
	require.Equal(t, 0, buf.Len())
	require.False(t, buf.Overflowed())
}

func TestRingBuffer_ReadAllClearsOverflow(t *testing.T) {
	buf := newRingBuffer(10)
	buf.Write([]byte("1234567890abcdef"))
	require.True(t, buf.Overflowed())
	buf.ReadAll()
	require.False(t, buf.Overflowed())
}

func TestRingBuffer_EmptyRead(t *testing.T) {
	buf := newRingBuffer(1024)
	out := buf.ReadAll()
	require.Nil(t, out)
}

func TestRingBuffer_ExactCapacity(t *testing.T) {
	buf := newRingBuffer(5)
	buf.Write([]byte("12345"))
	require.False(t, buf.Overflowed())
	require.Equal(t, 5, buf.Len())
	out := buf.ReadAll()
	require.Equal(t, []byte("12345"), out)
}

func TestRingBuffer_WrapAround(t *testing.T) {
	buf := newRingBuffer(8)
	buf.Write([]byte("AAAAA"))
	buf.ReadAll()
	buf.Write([]byte("BBBBBB"))
	require.Equal(t, 6, buf.Len())
	out := buf.ReadAll()
	require.Equal(t, []byte("BBBBBB"), out)
}
