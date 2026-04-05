package fastctrl

import "encoding/binary"

const (
	MsgPing byte = 0xFE
	MsgPong byte = 0xFF
)

// BuildPong creates a pong datagram with the engine's PTP timestamp.
// Format: [0xFF][8 bytes: PTP µs big-endian]
func BuildPong(ptpMicroseconds int64) []byte {
	buf := make([]byte, 9)
	buf[0] = MsgPong
	binary.BigEndian.PutUint64(buf[1:9], uint64(ptpMicroseconds))
	return buf
}

// ParsePing extracts an optional sequence number from a ping datagram.
// The type byte (0xFE) is already stripped by the dispatcher.
// Format: [] (empty) or [4 bytes: sequence number big-endian]
func ParsePing(data []byte) (seq uint32, err error) {
	if len(data) >= 4 {
		seq = binary.BigEndian.Uint32(data[:4])
	}
	return seq, nil
}
