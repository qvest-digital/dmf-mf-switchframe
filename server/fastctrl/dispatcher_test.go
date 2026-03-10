package fastctrl

import (
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
