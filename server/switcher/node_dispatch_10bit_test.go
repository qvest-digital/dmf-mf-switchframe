package switcher

import (
	"encoding/binary"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/stmap"
)

// --- stmap node 10-bit dispatch ---

func TestStmapNode_10bit_Dispatch(t *testing.T) {
	// Create an identity ST map -- every pixel maps to itself.
	w, h := 16, 8
	identityMap := stmap.Identity(w, h)
	reg := stmap.NewRegistry()
	require.NoError(t, reg.Store(identityMap))
	require.NoError(t, reg.AssignProgram("identity"))

	node := &stmapProgramNode{registry: reg}
	err := node.Configure(PipelineFormat{
		Width: w, Height: h,
		Color: PipelineColorConfig{ColorFormat: ColorYUV422_10bit},
	})
	require.NoError(t, err)
	require.True(t, node.Active())

	// Create a 10-bit frame with a known Y gradient.
	frameSize := ColorYUV422_10bit.FrameSize(w, h)
	data := make([]byte, frameSize)
	ySize := w * h
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(i%1024))
	}
	// Fill chroma with neutral
	for i := ySize * 2; i < frameSize; i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 512)
	}

	frame := &ProcessingFrame{
		Data:   data,
		Width:  w,
		Height: h,
		Format: ColorYUV422_10bit,
	}

	out := node.Process(nil, frame)
	require.Same(t, frame, out, "stmap should return src (in-place)")

	// Identity map should preserve the Y gradient.
	for i := 0; i < minInt(10, ySize); i++ {
		got := binary.LittleEndian.Uint16(out.Data[i*2:])
		// Identity may have small rounding from bilinear, allow +/-1
		expected := uint16(i % 1024)
		diff := int(got) - int(expected)
		if diff < -1 || diff > 1 {
			t.Errorf("Y[%d]: got %d, want %d (+/-1)", i, got, expected)
		}
	}
}

func TestStmapNode_10bit_BufferSized(t *testing.T) {
	w, h := 32, 16
	reg := stmap.NewRegistry()
	node := &stmapProgramNode{registry: reg}

	err := node.Configure(PipelineFormat{
		Width: w, Height: h,
		Color: PipelineColorConfig{ColorFormat: ColorYUV422_10bit},
	})
	require.NoError(t, err)

	// Buffer should be sized for 10-bit frame
	require.Equal(t, ColorYUV422_10bit.FrameSize(w, h), len(node.buf))
}

func TestStmapNode_8bit_StillWorks(t *testing.T) {
	w, h := 16, 8
	identityMap := stmap.Identity(w, h)
	reg := stmap.NewRegistry()
	require.NoError(t, reg.Store(identityMap))
	require.NoError(t, reg.AssignProgram("identity"))

	node := &stmapProgramNode{registry: reg}
	err := node.Configure(PipelineFormat{
		Width: w, Height: h,
		Color: PipelineColorConfig{ColorFormat: ColorYUV420_8bit},
	})
	require.NoError(t, err)

	frameSize := ColorYUV420_8bit.FrameSize(w, h)
	data := make([]byte, frameSize)
	for i := 0; i < w*h; i++ {
		data[i] = byte(i % 256)
	}

	frame := &ProcessingFrame{
		Data:   data,
		Width:  w,
		Height: h,
		Format: ColorYUV420_8bit,
	}

	out := node.Process(nil, frame)
	require.Same(t, frame, out)
}

// --- raw sink node 10-bit ---

func TestRawSinkNode_10bit_FramePassthrough(t *testing.T) {
	var sink atomic.Pointer[RawVideoSink]
	var receivedFormat PipelineColorFormat
	var receivedDataLen int
	fn := RawVideoSink(func(pf *ProcessingFrame) {
		receivedFormat = pf.Format
		receivedDataLen = len(pf.Data)
	})
	sink.Store(&fn)

	node := &rawSinkNode{sink: &sink, name: "raw-sink-10bit"}

	w, h := 16, 8
	frameSize := ColorYUV422_10bit.FrameSize(w, h)
	data := make([]byte, frameSize)
	for i := range data {
		data[i] = byte(i & 0xFF)
	}

	frame := &ProcessingFrame{
		Data:   data,
		Width:  w,
		Height: h,
		Format: ColorYUV422_10bit,
	}
	frame.SetRefs(1)

	out := node.Process(nil, frame)
	require.Same(t, frame, out)
	require.Equal(t, ColorYUV422_10bit, receivedFormat)
	require.Equal(t, frameSize, receivedDataLen)
}

// --- compositor node 10-bit passthrough ---

func TestCompositorNode_10bit_NilInactive(t *testing.T) {
	// compositorNode with nil compositor should not be Active()
	node := &compositorNode{compositor: nil}
	require.False(t, node.Active())
}

// --- upstream key node 10-bit passthrough ---

func TestUpstreamKeyNode_10bit_NilInactive(t *testing.T) {
	// Upstream key with nil bridge should not be Active()
	node := &upstreamKeyNode{bridge: nil}
	require.False(t, node.Active())
}

// --- encode node format awareness ---

func TestEncodeNode_10bit_Format_Configure(t *testing.T) {
	// encodeNode should accept 10-bit format in Configure without error
	node := &encodeNode{
		codecs:          nil, // not needed for Configure
		encodeNilCount:  &atomic.Int64{},
		encodeDropCount: &atomic.Int64{},
	}

	err := node.Configure(PipelineFormat{
		Width: 1920, Height: 1080,
		Color: PipelineColorConfig{ColorFormat: ColorYUV422_10bit, Codec: CodecHEVC},
	})
	require.NoError(t, err)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
