package switcher

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRawSinkNode_InactiveWhenNilSink(t *testing.T) {
	var sink atomic.Pointer[RawVideoSink]
	n := &rawSinkNode{sink: &sink, name: "raw-sink-test"}
	require.False(t, n.Active())
	require.Equal(t, "raw-sink-test", n.Name())
	require.Nil(t, n.Err())
	require.True(t, n.Latency() >= 0)
	require.NoError(t, n.Close())
}

func TestRawSinkNode_ActiveWhenSinkSet(t *testing.T) {
	var sink atomic.Pointer[RawVideoSink]
	fn := RawVideoSink(func(pf *ProcessingFrame) {})
	sink.Store(&fn)

	n := &rawSinkNode{sink: &sink, name: "raw-sink-test"}
	require.True(t, n.Active())
}

func TestRawSinkNode_ProcessDeepCopies(t *testing.T) {
	var sink atomic.Pointer[RawVideoSink]
	var receivedByte byte
	var receivedPTS int64
	var called bool
	fn := RawVideoSink(func(pf *ProcessingFrame) {
		called = true
		receivedByte = pf.YUV[0]
		receivedPTS = pf.PTS
	})
	sink.Store(&fn)

	n := &rawSinkNode{sink: &sink, name: "raw-sink-test"}

	pf := &ProcessingFrame{
		YUV:    make([]byte, 8*8*3/2),
		Width:  8,
		Height: 8,
		PTS:    1000,
	}
	pf.YUV[0] = 0xAA

	out := n.Process(nil, pf)
	require.Same(t, pf, out, "Process should return src (passthrough)")

	require.True(t, called, "sink should have been called")
	require.Equal(t, byte(0xAA), receivedByte, "sink should receive correct data via deep copy")
	require.Equal(t, int64(1000), receivedPTS)

	// Verify deep copy — modifying original should not have affected received data
	pf.YUV[0] = 0xBB
	require.Equal(t, byte(0xAA), receivedByte, "sink should have received a deep copy")
}

func TestRawSinkNode_ProcessSkipsNilSink(t *testing.T) {
	var sink atomic.Pointer[RawVideoSink]
	n := &rawSinkNode{sink: &sink, name: "raw-sink-test"}

	pf := &ProcessingFrame{
		YUV:    make([]byte, 8*8*3/2),
		Width:  8,
		Height: 8,
	}

	out := n.Process(nil, pf)
	require.Same(t, pf, out)
}
