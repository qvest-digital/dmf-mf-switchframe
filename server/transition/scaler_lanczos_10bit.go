package transition

import (
	"encoding/binary"
	"fmt"
	"sync"
)

// ScaleYUV422P10WithQuality scales a YUV422P10LE frame using the selected algorithm.
func ScaleYUV422P10WithQuality(src []byte, srcW, srcH int, dst []byte, dstW, dstH int, quality ScaleQuality) error {
	if srcW == dstW && srcH == dstH {
		copy(dst, src)
		return nil
	}
	switch quality {
	case ScaleQualityHigh:
		return ScaleYUV422P10Lanczos(src, srcW, srcH, dst, dstW, dstH)
	default:
		return ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH)
	}
}

// ScaleYUV422P10Lanczos scales a YUV422P10LE frame using Lanczos-3 interpolation.
// Each plane is scaled at its native resolution: full for Y, half-width for Cb/Cr.
// SIMD optimization is deferred; this is a pure Go implementation.
func ScaleYUV422P10Lanczos(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) error {
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return fmt.Errorf("invalid dimensions: src=%dx%d dst=%dx%d", srcW, srcH, dstW, dstH)
	}
	if srcW%2 != 0 {
		return fmt.Errorf("src width must be even for YUV422: got %d", srcW)
	}
	if dstW%2 != 0 {
		return fmt.Errorf("dst width must be even for YUV422: got %d", dstW)
	}

	srcFrameSize := srcW * srcH * 4
	dstFrameSize := dstW * dstH * 4
	if len(src) < srcFrameSize {
		return fmt.Errorf("src buffer too small: need %d, got %d", srcFrameSize, len(src))
	}
	if len(dst) < dstFrameSize {
		return fmt.Errorf("dst buffer too small: need %d, got %d", dstFrameSize, len(dst))
	}

	srcYSize := srcW * srcH * 2
	dstYSize := dstW * dstH * 2

	srcChromaW := srcW / 2
	srcChromaPlaneSize := srcChromaW * srcH * 2
	dstChromaW := dstW / 2
	dstChromaPlaneSize := dstChromaW * dstH * 2

	srcCbOff := srcYSize
	dstCbOff := dstYSize
	srcCrOff := srcCbOff + srcChromaPlaneSize
	dstCrOff := dstCbOff + dstChromaPlaneSize

	// Scale all three planes concurrently
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scalePlaneLanczos10(
			src[srcCbOff:srcCbOff+srcChromaPlaneSize], srcChromaW, srcH,
			dst[dstCbOff:dstCbOff+dstChromaPlaneSize], dstChromaW, dstH,
		)
	}()
	go func() {
		defer wg.Done()
		scalePlaneLanczos10(
			src[srcCrOff:srcCrOff+srcChromaPlaneSize], srcChromaW, srcH,
			dst[dstCrOff:dstCrOff+dstChromaPlaneSize], dstChromaW, dstH,
		)
	}()

	// Y plane on calling goroutine
	scalePlaneLanczos10(
		src[:srcYSize], srcW, srcH,
		dst[:dstYSize], dstW, dstH,
	)
	wg.Wait()
	return nil
}

// scalePlaneLanczos10 performs Lanczos-3 interpolation on a single 10-bit plane.
// Uses separable filtering: horizontal pass into float32 intermediate, then vertical.
func scalePlaneLanczos10(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	if srcW == dstW && srcH == dstH {
		copy(dst, src)
		return
	}

	if srcW == 1 && srcH == 1 {
		val := binary.LittleEndian.Uint16(src[:2])
		for i := 0; i < dstW*dstH; i++ {
			binary.LittleEndian.PutUint16(dst[i*2:], val)
		}
		return
	}

	hKernel := getLanczosKernel(srcW, dstW)
	vKernel := getLanczosKernel(srcH, dstH)

	// Float32 intermediate: dstW * srcH
	temp := getLanczosTempBuf(dstW * srcH)
	defer putLanczosTempBuf(temp)

	// Horizontal pass
	for y := 0; y < srcH; y++ {
		srcRow := src[y*srcW*2 : (y+1)*srcW*2]
		dstRow := temp[y*dstW : (y+1)*dstW]
		for dx := 0; dx < dstW; dx++ {
			off := int(hKernel.offsets[dx])
			wBase := dx * hKernel.maxTaps
			var sum float32
			for t := 0; t < hKernel.maxTaps; t++ {
				w := hKernel.weights[wBase+t]
				if w == 0 {
					continue
				}
				sx := off + t
				if sx >= srcW {
					sx = srcW - 1
				}
				sum += w * float32(binary.LittleEndian.Uint16(srcRow[sx*2:]))
			}
			dstRow[dx] = sum
		}
	}

	// Vertical pass
	for dy := 0; dy < dstH; dy++ {
		off := int(vKernel.offsets[dy])
		wBase := dy * vKernel.maxTaps
		dstRow := dst[dy*dstW*2 : (dy+1)*dstW*2]
		for dx := 0; dx < dstW; dx++ {
			var sum float32
			for t := 0; t < vKernel.maxTaps; t++ {
				w := vKernel.weights[wBase+t]
				if w == 0 {
					continue
				}
				sy := off + t
				if sy >= srcH {
					sy = srcH - 1
				}
				sum += w * temp[sy*dstW+dx]
			}
			val := int(sum + 0.5)
			if val < 0 {
				val = 0
			} else if val > 1023 {
				val = 1023
			}
			binary.LittleEndian.PutUint16(dstRow[dx*2:], uint16(val))
		}
	}
}
