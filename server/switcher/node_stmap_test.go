package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/stmap"
)

func TestSTMapProgramNode_Name(t *testing.T) {
	n := &stmapProgramNode{}
	require.Equal(t, "stmap-program", n.Name())
}

func TestSTMapProgramNode_Configure(t *testing.T) {
	n := &stmapProgramNode{}
	err := n.Configure(PipelineFormat{Width: 1920, Height: 1080})
	require.NoError(t, err)
	require.Len(t, n.buf, 1920*1080*3/2, "buffer should be allocated for full YUV420 frame")
}

func TestSTMapProgramNode_Inactive_WhenNoRegistry(t *testing.T) {
	n := &stmapProgramNode{}
	require.False(t, n.Active(), "node should be inactive when registry is nil")
}

func TestSTMapProgramNode_Inactive_WhenNoMap(t *testing.T) {
	reg := stmap.NewRegistry()
	n := &stmapProgramNode{registry: reg}
	require.False(t, n.Active(), "node should be inactive when no program map assigned")
}

func TestSTMapProgramNode_Active_WhenMapAssigned(t *testing.T) {
	reg := stmap.NewRegistry()
	m := stmap.Identity(8, 8)
	m.Name = "test"
	require.NoError(t, reg.Store(m))
	require.NoError(t, reg.AssignProgram("test"))

	n := &stmapProgramNode{registry: reg}
	require.True(t, n.Active(), "node should be active when program map is assigned")
}

func TestSTMapProgramNode_Process_StaticMap(t *testing.T) {
	const w, h = 8, 8
	frameSize := w * h * 3 / 2

	reg := stmap.NewRegistry()
	// Identity map: output should match input (within rounding tolerance)
	m := stmap.Identity(w, h)
	m.Name = "identity"
	require.NoError(t, reg.Store(m))
	require.NoError(t, reg.AssignProgram("identity"))

	n := &stmapProgramNode{registry: reg}
	require.NoError(t, n.Configure(PipelineFormat{Width: w, Height: h}))

	// Build a test frame with known Y/Cb/Cr values.
	yuv := make([]byte, frameSize)
	for i := 0; i < w*h; i++ {
		yuv[i] = byte(i % 256) // Y: 0-63 cycling
	}
	cbOff := w * h
	crOff := cbOff + (w/2)*(h/2)
	for i := 0; i < (w/2)*(h/2); i++ {
		yuv[cbOff+i] = 128 + byte(i%16)
		yuv[crOff+i] = 64 + byte(i%16)
	}

	src := &ProcessingFrame{
		YUV:    make([]byte, frameSize),
		Width:  w,
		Height: h,
	}
	copy(src.YUV, yuv)

	result := n.Process(nil, src)
	require.Equal(t, src, result, "process should return src (in-place)")

	// Identity map: every pixel should match within +-1 due to bilinear rounding.
	for i := 0; i < frameSize; i++ {
		diff := int(result.YUV[i]) - int(yuv[i])
		if diff < -1 || diff > 1 {
			t.Fatalf("pixel %d: got %d, want %d (±1)", i, result.YUV[i], yuv[i])
		}
	}
}

func TestSTMapProgramNode_Process_AnimatedMap(t *testing.T) {
	const w, h = 4, 4
	frameSize := w * h * 3 / 2

	// Build 3 animated frames, all identity maps.
	frames := make([]*stmap.STMap, 3)
	for i := range frames {
		frames[i] = stmap.Identity(w, h)
	}
	anim := stmap.NewAnimatedSTMap("anim", frames, 30)

	reg := stmap.NewRegistry()
	require.NoError(t, reg.StoreAnimated(anim))
	require.NoError(t, reg.AssignProgram("anim"))

	n := &stmapProgramNode{registry: reg}
	require.NoError(t, n.Configure(PipelineFormat{Width: w, Height: h}))

	// Build test frame.
	yuv := make([]byte, frameSize)
	for i := range yuv {
		yuv[i] = byte(100 + i%50)
	}

	// Process multiple times to verify frame advancement.
	for iter := 0; iter < 5; iter++ {
		src := &ProcessingFrame{
			YUV:    make([]byte, frameSize),
			Width:  w,
			Height: h,
		}
		copy(src.YUV, yuv)

		result := n.Process(nil, src)
		require.Equal(t, src, result, "process should return src")

		// With identity frames, output should be ±1 of input.
		for i := 0; i < frameSize; i++ {
			diff := int(result.YUV[i]) - int(yuv[i])
			if diff < -1 || diff > 1 {
				t.Fatalf("iter %d pixel %d: got %d, want %d (±1)", iter, i, result.YUV[i], yuv[i])
			}
		}
	}
}

func TestSTMapProgramNode_Process_NoMap_Passthrough(t *testing.T) {
	// When no map is assigned, Process should be a no-op passthrough.
	reg := stmap.NewRegistry()
	n := &stmapProgramNode{registry: reg}
	require.NoError(t, n.Configure(PipelineFormat{Width: 8, Height: 8}))

	yuv := make([]byte, 8*8*3/2)
	for i := range yuv {
		yuv[i] = byte(i % 256)
	}
	original := make([]byte, len(yuv))
	copy(original, yuv)

	src := &ProcessingFrame{
		YUV:    yuv,
		Width:  8,
		Height: 8,
	}

	result := n.Process(nil, src)
	require.Equal(t, src, result)
	require.Equal(t, original, result.YUV, "frame should be unmodified when no map assigned")
}

func TestSTMapProgramNode_ErrAndClose(t *testing.T) {
	n := &stmapProgramNode{}
	require.NoError(t, n.Err())
	require.NoError(t, n.Close())
}

func TestSTMapProgramNode_Latency(t *testing.T) {
	n := &stmapProgramNode{}
	require.Greater(t, n.Latency().Nanoseconds(), int64(0))
}

func TestAnimatedSTMap_ProcessorAt(t *testing.T) {
	// Verify ProcessorAt lazily creates and caches processors.
	frames := make([]*stmap.STMap, 3)
	for i := range frames {
		frames[i] = stmap.Identity(4, 4)
	}
	anim := stmap.NewAnimatedSTMap("test", frames, 30)

	// First call should create the processor.
	p0 := anim.ProcessorAt(0)
	require.NotNil(t, p0)
	require.True(t, p0.Active())

	// Second call should return the same cached processor.
	p0Again := anim.ProcessorAt(0)
	require.Equal(t, p0, p0Again, "should return cached processor")

	// Different index should create a different processor (different pointer).
	p1 := anim.ProcessorAt(1)
	require.NotNil(t, p1)
	require.False(t, p0 == p1, "different frame indices should yield different processor pointers")

	// Wrapping should work — index 3 % 3 == 0, same pointer as p0.
	p3 := anim.ProcessorAt(3)
	require.True(t, p0 == p3, "wrapping index should return same processor pointer as index 0")
}

func TestAnimatedSTMap_AdvanceIndex(t *testing.T) {
	frames := make([]*stmap.STMap, 3)
	for i := range frames {
		frames[i] = stmap.Identity(4, 4)
	}
	anim := stmap.NewAnimatedSTMap("test", frames, 30)

	// AdvanceIndex should cycle through 1, 2, 0, 1, 2, ...
	require.Equal(t, 1, anim.AdvanceIndex()) // index goes 0->1
	require.Equal(t, 2, anim.AdvanceIndex()) // index goes 1->2
	require.Equal(t, 0, anim.AdvanceIndex()) // index goes 2->3, 3%3=0
	require.Equal(t, 1, anim.AdvanceIndex()) // index goes 3->4, 4%3=1
}
