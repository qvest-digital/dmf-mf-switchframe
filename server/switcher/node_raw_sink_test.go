package switcher

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRawSinkNode_AlwaysActive(t *testing.T) {
	var sink atomic.Pointer[RawVideoSink]
	n := &rawSinkNode{sink: &sink, name: "raw-sink-test"}
	require.True(t, n.Active()) // Always active; Process checks sink atomically
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
	var received *ProcessingFrame
	fn := RawVideoSink(func(pf *ProcessingFrame) {
		received = pf
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

	require.NotNil(t, received, "sink should have been called")
	require.Equal(t, byte(0xAA), received.YUV[0])

	// Verify deep copy — modifying original should not affect received
	pf.YUV[0] = 0xBB
	require.Equal(t, byte(0xAA), received.YUV[0], "sink should receive a deep copy")
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
