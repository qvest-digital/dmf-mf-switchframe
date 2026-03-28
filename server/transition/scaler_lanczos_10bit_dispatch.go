package transition

import (
	"encoding/binary"
	"fmt"
	"sync"
	"unsafe"
)

// scaleLanczos10Dispatch is the SIMD-accelerated 10-bit Lanczos-3 scaler dispatch.
// Uses SIMD-accelerated horizontal and vertical row kernels.
func scaleLanczos10Dispatch(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) error {
	if srcW == dstW && srcH == dstH {
		copy(dst, src)
		return nil
	}
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
		scalePlaneLanczos10SIMD(
			src[srcCbOff:srcCbOff+srcChromaPlaneSize], srcChromaW, srcH,
			dst[dstCbOff:dstCbOff+dstChromaPlaneSize], dstChromaW, dstH,
		)
	}()
	go func() {
		defer wg.Done()
		scalePlaneLanczos10SIMD(
			src[srcCrOff:srcCrOff+srcChromaPlaneSize], srcChromaW, srcH,
			dst[dstCrOff:dstCrOff+dstChromaPlaneSize], dstChromaW, dstH,
		)
	}()

	// Y plane on calling goroutine
	scalePlaneLanczos10SIMD(
		src[:srcYSize], srcW, srcH,
		dst[:dstYSize], dstW, dstH,
	)
	wg.Wait()
	return nil
}

// scalePlaneLanczos10SIMD performs Lanczos-3 interpolation on a single 10-bit plane
// using SIMD-accelerated horizontal and vertical row kernels.
func scalePlaneLanczos10SIMD(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
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

	// Horizontal pass: SIMD-accelerated row kernel
	for y := 0; y < srcH; y++ {
		srcRow := src[y*srcW*2 : (y+1)*srcW*2]
		dstRow := temp[y*dstW : (y+1)*dstW]
		lanczosHoriz10Row(dstRow, srcRow, srcW, hKernel.offsets, hKernel.weights, hKernel.maxTaps)
	}

	// Vertical pass: SIMD-accelerated row kernel outputting uint16 LE
	for dy := 0; dy < dstH; dy++ {
		off := vKernel.offsets[dy]
		wBase := dy * vKernel.maxTaps
		dstRow := dst[dy*dstW*2 : (dy+1)*dstW*2]
		lanczosVert10Row(dstRow, temp, dstW, off, vKernel.weights[wBase:wBase+vKernel.maxTaps], vKernel.maxTaps)
	}
}

// lanczosHoriz10RowGo is the Go reference for horizontal Lanczos on 10-bit uint16 source.
// Reads uint16 LE samples from src, writes float32 to dst.
func lanczosHoriz10RowGo(dst []float32, src []byte, srcW int, offsets []int32, weights []float32, maxTaps int) {
	dstW := len(dst)
	if dstW == 0 {
		return
	}

	for d := 0; d < dstW; d++ {
		off := int(offsets[d])
		wBase := d * maxTaps
		var sum float32
		for t := 0; t < maxTaps; t++ {
			w := weights[wBase+t]
			if w == 0 {
				continue
			}
			sx := off + t
			if sx >= srcW {
				sx = srcW - 1
			}
			if sx < 0 {
				sx = 0
			}
			sum += w * float32(binary.LittleEndian.Uint16(src[sx*2:]))
		}
		dst[d] = sum
	}
}

// lanczosVert10RowGo is the Go reference for vertical Lanczos outputting uint16 LE.
// Reads float32 from temp intermediate, writes uint16 LE to dst, clamped to [0, 1023].
func lanczosVert10RowGo(dst []byte, temp []float32, tempStride int, startRow int32, weights []float32, maxTaps int) {
	dstW := len(dst) / 2
	if dstW == 0 {
		return
	}

	dstS := unsafe.Slice(&dst[0], dstW*2)

	type rowInfo struct {
		base   int
		weight float32
	}
	var rowsBuf [16]rowInfo
	rows := rowsBuf[:0]
	for t := 0; t < maxTaps; t++ {
		w := weights[t]
		if w == 0 {
			continue
		}
		row := int(startRow) + t
		rows = append(rows, rowInfo{base: row * tempStride, weight: w})
	}

	nRows := len(rows)
	if nRows == 0 {
		for d := 0; d < dstW; d++ {
			binary.LittleEndian.PutUint16(dstS[d*2:], 0)
		}
		return
	}

	for d := 0; d < dstW; d++ {
		var acc float32
		for i := 0; i < nRows; i++ {
			acc += rows[i].weight * temp[rows[i].base+d]
		}
		val := int32(acc + 0.5)
		if val < 0 {
			val = 0
		} else if val > 1023 {
			val = 1023
		}
		binary.LittleEndian.PutUint16(dstS[d*2:], uint16(val))
	}
}
