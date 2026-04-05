package fastctrl

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildPong(t *testing.T) {
	ptpTime := int64(1_700_000_000_000_000)
	pong := BuildPong(ptpTime)

	require.Len(t, pong, 9, "pong should be 9 bytes: 1 type + 8 timestamp")
	require.Equal(t, byte(MsgPong), pong[0])
	ts := binary.BigEndian.Uint64(pong[1:9])
	require.Equal(t, uint64(ptpTime), ts)
}

func TestParsePing(t *testing.T) {
	// Ping payload is empty (just the type byte, which is stripped by dispatcher)
	data := []byte{} // dispatcher strips type byte before calling handler
	seq, err := ParsePing(data)
	require.NoError(t, err)
	require.Equal(t, uint32(0), seq)
}

func TestParsePing_WithSequence(t *testing.T) {
	// Ping can optionally carry a 4-byte sequence number for correlation
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, 42)
	seq, err := ParsePing(data)
	require.NoError(t, err)
	require.Equal(t, uint32(42), seq)
}
