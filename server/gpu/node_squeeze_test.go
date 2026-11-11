//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeSqueezeState struct {
	rect Rect
	fill *GPUFrame
}

func (f *fakeSqueezeState) Squeeze(width, height int) (Rect, string, bool) {
	return f.rect, "cam2", true
}

func (f *fakeSqueezeState) GPUFill(string) *GPUFrame {
	f.fill.Ref()
	return f.fill
}

// An 8-bit background on a 10-bit program would be scaled by the NV12
// kernel into a P210 frame; it is drawn black instead.
func TestGPUSqueezeNode_MismatchedBackgroundFormatDrawsBlack(t *testing.T) {
	ctx := testContext(t)
	const w, h = 16, 16
	p210, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer p210.Close()
	nv12, err := NewFramePool(ctx, w, h, 1)
	require.NoError(t, err)
	defer nv12.Close()

	fill, err := nv12.Acquire()
	require.NoError(t, err)
	defer fill.Release()
	require.NoError(t, GPUFillColorDispatch(ctx, fill, YUVColor{Y: 200, Cb: 100, Cr: 150}))

	frame, err := p210.Acquire()
	require.NoError(t, err)
	defer frame.Release()
	require.NoError(t, GPUFillColorDispatch(ctx, frame, YUVColor{Y: 235, Cb: 128, Cr: 128}))

	n := NewGPUSqueezeNode(ctx, p210, &fakeSqueezeState{rect: Rect{X: 8, Y: 8, W: 8, H: 8}, fill: fill})
	require.NoError(t, n.ProcessGPU(frame))

	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))
	require.Equal(t, uint16(64), binary.LittleEndian.Uint16(buf), "black around the program")
	require.Equal(t, uint16(940), binary.LittleEndian.Uint16(buf[(12*w+12)*2:]), "program inside the rect")
}
