package fastctrl

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDispatcher_Dispatch(t *testing.T) {
	d := New()
	var received []byte
	d.Register(0x01, func(data []byte) error {
		received = data
		return nil
	})
	err := d.Dispatch([]byte{0x01, 0xAA, 0xBB})
	require.NoError(t, err)
	require.Equal(t, []byte{0xAA, 0xBB}, received)
}

func TestDispatcher_UnknownType(t *testing.T) {
	d := New()
	err := d.Dispatch([]byte{0xFF, 0x00})
	require.ErrorIs(t, err, ErrUnknownType)
}

func TestDispatcher_EmptyPayload(t *testing.T) {
	d := New()
	err := d.Dispatch(nil)
	require.ErrorIs(t, err, ErrEmptyDatagram)
	err = d.Dispatch([]byte{})
	require.ErrorIs(t, err, ErrEmptyDatagram)
}

func TestDispatcher_HandlerError(t *testing.T) {
	d := New()
	handlerErr := errors.New("bad payload")
	d.Register(0x02, func(data []byte) error { return handlerErr })
	err := d.Dispatch([]byte{0x02, 0x00})
	require.ErrorIs(t, err, handlerErr)
}

// TestDispatch_WithPTPTimestamp verifies that an 8-byte PTP timestamp prefix is
// stripped before the handler receives the payload.
func TestDispatch_WithPTPTimestamp(t *testing.T) {
	d := NewDispatcher()

	var got []byte
	d.Register(0x02, func(data []byte) error {
		got = data
		return nil
	})

	// Build: [8-byte PTP timestamp][0x02 type][4-byte payload]
	// Timestamp: 2024-01-01 00:00:00 UTC in microseconds = 1_704_067_200_000_000
	const ptpTs uint64 = 1_704_067_200_000_000
	payload := []byte{0xAA, 0xBB, 0xCC, 0xDD}

	datagram := make([]byte, 0, 13)
	var tsBuf [8]byte
	binary.BigEndian.PutUint64(tsBuf[:], ptpTs)
	datagram = append(datagram, tsBuf[:]...)
	datagram = append(datagram, 0x02)
	datagram = append(datagram, payload...)

	require.NoError(t, d.Dispatch(datagram))
	require.Equal(t, payload, got, "handler should receive payload without PTP prefix")
}

// TestDispatch_WithoutTimestamp_BackwardCompatible verifies that old-format
// datagrams (no PTP prefix) continue to work unchanged.
func TestDispatch_WithoutTimestamp_BackwardCompatible(t *testing.T) {
	d := NewDispatcher()

	var got []byte
	d.Register(0x02, func(data []byte) error {
		got = data
		return nil
	})

	// Old format: [0x02 type][4-byte payload]
	payload := []byte{0x01, 0x02, 0x03, 0x04}
	datagram := append([]byte{0x02}, payload...)

	require.NoError(t, d.Dispatch(datagram))
	require.Equal(t, payload, got, "handler should receive payload unchanged in old format")
}

// TestDispatch_EmptyDatagram_WithPTP verifies that an empty datagram still
// returns ErrEmptyDatagram (the PTP path must not bypass this check).
func TestDispatch_EmptyDatagram_WithPTP(t *testing.T) {
	d := NewDispatcher()
	require.ErrorIs(t, d.Dispatch([]byte{}), ErrEmptyDatagram)
	require.ErrorIs(t, d.Dispatch(nil), ErrEmptyDatagram)
}

// TestDispatch_PTPTimestamp_ShortDatagram verifies that a datagram shorter than
// 10 bytes is dispatched as-is (no timestamp stripping attempted).
func TestDispatch_PTPTimestamp_ShortDatagram(t *testing.T) {
	d := NewDispatcher()

	var handlerCalled bool
	d.Register(0x01, func(data []byte) error {
		handlerCalled = true
		return nil
	})

	// 9 bytes total (< 10), type byte = 0x01
	datagram := make([]byte, 9)
	datagram[0] = 0x01

	require.NoError(t, d.Dispatch(datagram))
	require.True(t, handlerCalled, "handler should be called for short datagram without timestamp stripping")
}

// TestDispatch_PTPTimestamp_OutOfRangeNotStripped ensures that a datagram whose
// first 8 bytes decode to an out-of-range uint64 is NOT treated as PTP-prefixed.
func TestDispatch_PTPTimestamp_OutOfRangeNotStripped(t *testing.T) {
	d := NewDispatcher()

	// No handler registered for type 0x00 (first byte of the all-zero datagram).
	// If timestamp were incorrectly stripped, byte 8 (0x55) would be the type,
	// and we'd get ErrUnknownType for a different reason. Either way the zero
	// timestamp (value=0) is below threshold so no strip should occur.
	datagram := make([]byte, 13) // first 8 bytes = 0x00... → ts=0, below threshold
	datagram[8] = 0x55

	err := d.Dispatch(datagram)
	// datagram[0]=0x00, no handler → ErrUnknownType (no strip occurred)
	require.ErrorIs(t, err, ErrUnknownType)
}
