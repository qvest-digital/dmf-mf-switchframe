package fastctrl

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildPong(t *testing.T) {
	ptpTime := int64(1_700_000_000_000_000)
	seq := uint32(42)
	pong := BuildPong(ptpTime, seq)

	require.Len(t, pong, 13, "pong should be 13 bytes: 1 type + 4 seq + 8 timestamp")
	require.Equal(t, byte(MsgPong), pong[0])
	gotSeq := binary.BigEndian.Uint32(pong[1:5])
	require.Equal(t, seq, gotSeq, "echoed sequence number should match")
	ts := binary.BigEndian.Uint64(pong[5:13])
	require.Equal(t, uint64(ptpTime), ts, "PTP timestamp should match")
}

func TestBuildPong_ZeroSeq(t *testing.T) {
	ptpTime := int64(1_600_000_000_000_000)
	pong := BuildPong(ptpTime, 0)

	require.Len(t, pong, 13)
	require.Equal(t, byte(MsgPong), pong[0])
	require.Equal(t, uint32(0), binary.BigEndian.Uint32(pong[1:5]))
	require.Equal(t, uint64(ptpTime), binary.BigEndian.Uint64(pong[5:13]))
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
