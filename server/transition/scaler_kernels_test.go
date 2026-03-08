package transition

import (
	"testing"
)

// --- scaleBilinearRow tests ---

func TestScaleBilinearRow_Identity(t *testing.T) {
	// 1:1 mapping (srcW == dstW), fy=0 (on exact row boundary)
	// Should just copy the row.
	srcW, dstW := 8, 8
	row0 := []byte{10, 20, 30, 40, 50, 60, 70, 80}
	row1 := []byte{90, 100, 110, 120, 130, 140, 150, 160}
	dst := make([]byte, dstW)

	// Precompute xCoords for 1:1 mapping
	xCoords := make([]int64, dstW)
	dstWm1 := int64(dstW - 1)
	srcWm1 := int64(srcW - 1)
	for dx := 0; dx < dstW; dx++ {
		xCoords[dx] = (int64(dx) * srcWm1 << 16) / dstWm1
	}

	scaleBilinearRow(&dst[0], &row0[0], &row1[0], srcW, dstW, &xCoords[0], 0)

	// With fy=0, result should be row0 values exactly
	for i, want := range row0 {
		if dst[i] != want {
			t.Errorf("dst[%d] = %d, want %d", i, dst[i], want)
		}
	}
}

func TestScaleBilinearRow_BottomRow(t *testing.T) {
	// 1:1 mapping, fy=65536 (fully on row1)
	srcW, dstW := 4, 4
	row0 := []byte{0, 0, 0, 0}
	row1 := []byte{100, 200, 50, 150}
	dst := make([]byte, dstW)

	xCoords := make([]int64, dstW)
	dstWm1 := int64(dstW - 1)
	srcWm1 := int64(srcW - 1)
	for dx := 0; dx < dstW; dx++ {
		xCoords[dx] = (int64(dx) * srcWm1 << 16) / dstWm1
	}

	// fy = 65535 (max fraction, nearly all row1)
	scaleBilinearRow(&dst[0], &row0[0], &row1[0], srcW, dstW, &xCoords[0], 65535)

	// Should be very close to row1 values
	for i := range dst {
		diff := int(dst[i]) - int(row1[i])
		if diff < -1 || diff > 1 {
			t.Errorf("dst[%d] = %d, want ~%d", i, dst[i], row1[i])
		}
	}
}

func TestScaleBilinearRow_2xUpscale(t *testing.T) {
	// 4 pixels → 8 pixels, fy=0
	srcW, dstW := 4, 8
	row0 := []byte{0, 100, 200, 50}
	row1 := []byte{0, 100, 200, 50} // Same as row0 for simplicity
	dst := make([]byte, dstW)

	xCoords := make([]int64, dstW)
	dstWm1 := int64(dstW - 1)
	srcWm1 := int64(srcW - 1)
	for dx := 0; dx < dstW; dx++ {
		xCoords[dx] = (int64(dx) * srcWm1 << 16) / dstWm1
	}

	scaleBilinearRow(&dst[0], &row0[0], &row1[0], srcW, dstW, &xCoords[0], 0)

	// Corners must match
	if dst[0] != 0 {
		t.Errorf("dst[0] = %d, want 0", dst[0])
	}
	if dst[dstW-1] != 50 {
		t.Errorf("dst[%d] = %d, want 50", dstW-1, dst[dstW-1])
	}

	// Intermediate values should be interpolated
	for i, v := range dst {
		if v > 200 {
			t.Errorf("dst[%d] = %d, exceeds max source value 200", i, v)
		}
	}
}

func TestScaleBilinearRow_SinglePixel(t *testing.T) {
	srcW, dstW := 4, 1
	row0 := []byte{100, 150, 200, 250}
	row1 := []byte{0, 50, 100, 150}
	dst := make([]byte, 1)

	xCoords := []int64{0} // maps to src x=0

	scaleBilinearRow(&dst[0], &row0[0], &row1[0], srcW, dstW, &xCoords[0], 0)

	if dst[0] != 100 {
		t.Errorf("dst[0] = %d, want 100", dst[0])
	}
}

func TestScaleBilinearRow_VariousDstWidths(t *testing.T) {
	srcW := 16
	row0 := make([]byte, srcW)
	row1 := make([]byte, srcW)
	for i := range row0 {
		row0[i] = byte(i * 16)
		row1[i] = byte(i * 16)
	}

	for _, dstW := range []int{1, 15, 16, 17, 32, 1920} {
		t.Run("", func(t *testing.T) {
			dst := make([]byte, dstW)
			xCoords := make([]int64, dstW)
			dstWm1 := int64(dstW - 1)
			srcWm1 := int64(srcW - 1)
			if dstWm1 > 0 {
				for dx := 0; dx < dstW; dx++ {
					xCoords[dx] = (int64(dx) * srcWm1 << 16) / dstWm1
				}
			}

			scaleBilinearRow(&dst[0], &row0[0], &row1[0], srcW, dstW, &xCoords[0], 0)

			// First pixel should match row0[0]
			if dst[0] != row0[0] {
				t.Errorf("dstW=%d: dst[0] = %d, want %d", dstW, dst[0], row0[0])
			}
			// Last pixel should match row0[srcW-1]
			if dstWm1 > 0 {
				if dst[dstW-1] != row0[srcW-1] {
					t.Errorf("dstW=%d: dst[%d] = %d, want %d", dstW, dstW-1, dst[dstW-1], row0[srcW-1])
				}
			}
		})
	}
}

func TestScaleBilinearRow_MidpointFy(t *testing.T) {
	// fy = 32768 (0.5), row0=0, row1=100 → should get ~50
	srcW, dstW := 4, 4
	row0 := []byte{0, 0, 0, 0}
	row1 := []byte{100, 100, 100, 100}
	dst := make([]byte, dstW)

	xCoords := make([]int64, dstW)
	for dx := 0; dx < dstW; dx++ {
		xCoords[dx] = int64(dx) << 16
	}

	scaleBilinearRow(&dst[0], &row0[0], &row1[0], srcW, dstW, &xCoords[0], 32768)

	for i := range dst {
		if dst[i] != 50 {
			t.Errorf("dst[%d] = %d, want 50", i, dst[i])
		}
	}
}

// --- Cross-validation: kernel row-by-row vs full scalePlane ---

func TestScaleBilinearRow_CrossValidation(t *testing.T) {
	srcW, srcH := 8, 8
	dstW, dstH := 16, 16

	src := make([]byte, srcW*srcH)
	for i := range src {
		src[i] = byte((i * 31) % 256)
	}

	// Reference: full scalePlane
	refDst := make([]byte, dstW*dstH)
	scalePlane(src, srcW, srcH, refDst, dstW, dstH)

	// Kernel-based: reconstruct row by row
	kernelDst := make([]byte, dstW*dstH)

	dstWm1 := int64(dstW - 1)
	dstHm1 := int64(dstH - 1)
	srcWm1 := int64(srcW - 1)
	srcHm1 := int64(srcH - 1)

	xCoords := make([]int64, dstW)
	if dstWm1 > 0 {
		for dx := 0; dx < dstW; dx++ {
			xCoords[dx] = (int64(dx) * srcWm1 << 16) / dstWm1
		}
	}

	for dy := 0; dy < dstH; dy++ {
		var srcY int64
		if dstHm1 > 0 {
			srcY = (int64(dy) * srcHm1 << 16) / dstHm1
		}
		iy := int(srcY >> 16)
		fy := int(srcY & 0xFFFF)

		iy1 := iy + 1
		if iy1 >= srcH {
			iy1 = srcH - 1
		}

		row0 := src[iy*srcW:]
		row1 := src[iy1*srcW:]
		dstRow := kernelDst[dy*dstW:]

		scaleBilinearRow(&dstRow[0], &row0[0], &row1[0], srcW, dstW, &xCoords[0], fy)
	}

	for i := range refDst {
		if refDst[i] != kernelDst[i] {
			row := i / dstW
			col := i % dstW
			t.Errorf("pixel (%d,%d): ref=%d, kernel=%d", col, row, refDst[i], kernelDst[i])
		}
	}
}

func TestScaleBilinearRow_CrossValidation_Downscale(t *testing.T) {
	srcW, srcH := 16, 16
	dstW, dstH := 8, 8

	src := make([]byte, srcW*srcH)
	for i := range src {
		src[i] = byte((i*17 + 42) % 256)
	}

	refDst := make([]byte, dstW*dstH)
	scalePlane(src, srcW, srcH, refDst, dstW, dstH)

	kernelDst := make([]byte, dstW*dstH)

	dstWm1 := int64(dstW - 1)
	dstHm1 := int64(dstH - 1)
	srcWm1 := int64(srcW - 1)
	srcHm1 := int64(srcH - 1)

	xCoords := make([]int64, dstW)
	if dstWm1 > 0 {
		for dx := 0; dx < dstW; dx++ {
			xCoords[dx] = (int64(dx) * srcWm1 << 16) / dstWm1
		}
	}

	for dy := 0; dy < dstH; dy++ {
		var srcY int64
		if dstHm1 > 0 {
			srcY = (int64(dy) * srcHm1 << 16) / dstHm1
		}
		iy := int(srcY >> 16)
		fy := int(srcY & 0xFFFF)

		iy1 := iy + 1
		if iy1 >= srcH {
			iy1 = srcH - 1
		}

		row0 := src[iy*srcW:]
		row1 := src[iy1*srcW:]
		dstRow := kernelDst[dy*dstW:]

		scaleBilinearRow(&dstRow[0], &row0[0], &row1[0], srcW, dstW, &xCoords[0], fy)
	}

	for i := range refDst {
		if refDst[i] != kernelDst[i] {
			row := i / dstW
			col := i % dstW
			t.Errorf("pixel (%d,%d): ref=%d, kernel=%d", col, row, refDst[i], kernelDst[i])
		}
	}
}

// --- Benchmark ---

func BenchmarkScaleBilinearRow_1920(b *testing.B) {
	srcW, dstW := 1280, 1920
	row0 := make([]byte, srcW)
	row1 := make([]byte, srcW)
	dst := make([]byte, dstW)
	for i := range row0 {
		row0[i] = byte(i % 256)
		row1[i] = byte((i + 128) % 256)
	}

	xCoords := make([]int64, dstW)
	dstWm1 := int64(dstW - 1)
	srcWm1 := int64(srcW - 1)
	for dx := 0; dx < dstW; dx++ {
		xCoords[dx] = (int64(dx) * srcWm1 << 16) / dstWm1
	}

	b.SetBytes(int64(dstW))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scaleBilinearRow(&dst[0], &row0[0], &row1[0], srcW, dstW, &xCoords[0], 32768)
	}
}
