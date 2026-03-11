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

func TestRawSinkNode_ProcessZeroCopy(t *testing.T) {
	var sink atomic.Pointer[RawVideoSink]
	var receivedYUV []byte
	var receivedPTS int64
	var called bool
	fn := RawVideoSink(func(pf *ProcessingFrame) {
		called = true
		receivedYUV = pf.YUV
		receivedPTS = pf.PTS
	})
	sink.Store(&fn)

	n := &rawSinkNode{sink: &sink, name: "raw-sink-test"}

	srcYUV := make([]byte, 8*8*3/2)
	srcYUV[0] = 0xAA
	pf := &ProcessingFrame{
		YUV:    srcYUV,
		Width:  8,
		Height: 8,
		PTS:    1000,
	}
	pf.SetRefs(1) // pipeline frames are refcounted

	out := n.Process(nil, pf)
	require.Same(t, pf, out, "Process should return src (passthrough)")

	require.True(t, called, "sink should have been called")
	require.Equal(t, int64(1000), receivedPTS)

	// Zero-copy: sink received the same YUV buffer, not a deep copy
	require.Same(t, &srcYUV[0], &receivedYUV[0], "sink should receive same YUV buffer (zero-copy)")
	// Frame stays alive for the pipeline (refs=1 after Ref+ReleaseYUV cycle)
	require.NotNil(t, out.YUV)
	require.Equal(t, int32(1), pf.Refs())
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
