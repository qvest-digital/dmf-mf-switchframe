//go:build cgo && cuda

package gpu

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/mxl"
)

// The CUDA v210 kernels carry any even width as the CPU converters in mxl
// do, partial last group and zeroed line padding included: on the lossless
// P210 path and on the NV12 path, the GPU's output is byte for byte the
// CPU's.

var gpuV210Widths = []int{2, 4, 10, 1280, 1918, 1920}

// rampP10 is a YUV422P10LE frame whose samples all differ from their
// neighbours.
func rampP10(w, h int) []byte {
	out := make([]byte, w*h*4)
	put := func(i, v int) { binary.LittleEndian.PutUint16(out[2*i:], uint16(v&0x3FF)) }
	for i := 0; i < w*h; i++ {
		put(i, 64+i*7)
	}
	for i := 0; i < w/2*h; i++ {
		put(w*h+i, 200+i*5)
		put(w*h+w/2*h+i, 700+i*3)
	}
	return out
}

func TestV210AnyWidth_P210MatchesCPU(t *testing.T) {
	ctx := testContext(t)
	const h = 4
	for _, w := range gpuV210Widths {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			frame := rampP10(w, h)
			v210, err := mxl.YUV422P10ToV210(frame, w, h)
			require.NoError(t, err)

			pool, err := NewP210FramePool(ctx, w, h, 1)
			require.NoError(t, err)
			defer pool.Close()
			f, err := pool.Acquire()
			require.NoError(t, err)
			defer f.Release()

			require.NoError(t, UploadV210ToP210(ctx, f, v210, w, h))
			got := make([]byte, p210DownloadSize(w, h))
			require.NoError(t, Download10(ctx, f, got, w, h))
			require.Equal(t, frame, got, "V210 -> P210 differs from the CPU unpack")

			back := make([]byte, len(v210))
			for i := range back {
				back[i] = 0xEE // what a reused buffer held before
			}
			require.NoError(t, DownloadP210ToV210(ctx, back, f, w, h))
			require.Equal(t, v210, back, "P210 -> V210 differs from the CPU pack")
		})
	}
}

func TestV210AnyWidth_NV12MatchesCPU(t *testing.T) {
	ctx := testContext(t)
	const h = 4
	for _, w := range gpuV210Widths {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			yuv := make([]byte, w*h+2*(w/2)*(h/2))
			for i := range yuv {
				yuv[i] = byte(16 + i*7)
			}
			v210, err := mxl.YUV420pToV210(yuv, w, h)
			require.NoError(t, err)

			pool, err := NewFramePool(ctx, w, h, 1)
			require.NoError(t, err)
			defer pool.Close()
			f, err := pool.Acquire()
			require.NoError(t, err)
			defer f.Release()

			// V210 -> NV12: 10 to 8 bits by >> 2, chroma rows averaged, as
			// the CPU unpack does.
			require.NoError(t, UploadV210(ctx, f, v210, w, h))
			got := make([]byte, len(yuv))
			require.NoError(t, Download(ctx, got, f, w, h))
			want, err := mxl.V210ToYUV420p(v210, w, h)
			require.NoError(t, err)
			require.Equal(t, want, got, "V210 -> NV12 differs from the CPU unpack")

			// NV12 -> V210 widens 8 to 10 bits by << 2 where the CPU
			// replicates the top bits: the reference is the CPU's 10-bit
			// pack of the frame widened that way.
			p10 := make([]byte, w*h*4)
			cw := w / 2
			for i := 0; i < w*h; i++ {
				binary.LittleEndian.PutUint16(p10[2*i:], uint16(got[i])<<2)
			}
			for row := 0; row < h; row++ {
				for i := 0; i < cw; i++ {
					cb, cr := got[w*h+(row/2)*cw+i], got[w*h+cw*(h/2)+(row/2)*cw+i]
					binary.LittleEndian.PutUint16(p10[2*(w*h+row*cw+i):], uint16(cb)<<2)
					binary.LittleEndian.PutUint16(p10[2*(w*h+cw*h+row*cw+i):], uint16(cr)<<2)
				}
			}
			wantV210, err := mxl.YUV422P10ToV210(p10, w, h)
			require.NoError(t, err)
			back := make([]byte, len(v210))
			require.NoError(t, DownloadV210(ctx, back, f, w, h))
			require.Equal(t, wantV210, back, "NV12 -> V210 differs from the CPU pack")
		})
	}
}
