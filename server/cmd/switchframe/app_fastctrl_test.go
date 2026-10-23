package main

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/fastctrl"
)

// A browser measures RTT and clock offset from the pong to its ping. The
// pong has to reach the control track, the only path back to the browser;
// a pong left anywhere else leaves both at zero in the redundancy pane.
func TestHandleFastPingAnswersOnControlTrack(t *testing.T) {
	a := &App{controlPub: control.NewChannelPublisher(4)}
	a.initFastControl()

	ping := []byte{fastctrl.MsgPing, 0, 0, 0, 42}
	before := time.Now().UnixMicro()
	require.NoError(t, a.fastCtrl.Dispatch(ping))

	select {
	case pong := <-a.controlPub.Ch():
		require.Len(t, pong, 13)
		require.Equal(t, fastctrl.MsgPong, pong[0])
		require.Equal(t, uint32(42), binary.BigEndian.Uint32(pong[1:5]))
		require.GreaterOrEqual(t, int64(binary.BigEndian.Uint64(pong[5:13])), before)
	default:
		t.Fatal("no pong on the control track")
	}
	require.NotZero(t, a.fastCtrl.LastPongSentUs())
}
