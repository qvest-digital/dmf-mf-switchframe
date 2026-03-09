package graphics

import (
	"testing"
	"unsafe"
)

// BenchmarkSpillSuppressChroma_1080p_NearKey benchmarks the spill suppression
// kernel directly at 1080p with all pixels near the key color (worst case).
func BenchmarkSpillSuppressChroma_1080p_NearKey(b *testing.B) {
	width, height := 1920, 1080
	uvWidth := width / 2
	uvHeight := height / 2
	uvSize := uvWidth * uvHeight

	// Near-green chroma values
	cb := make([]byte, uvSize)
	cr := make([]byte, uvSize)
	for i := range cb {
		cb[i] = 50 // near green Cb=30
		cr[i] = 30 // near green Cr=12
	}

	var keyCb float32 = 30
	var keyCr float32 = 12
	var spillSup float32 = 0.8
	simDist := float32(0.3) * 181.0
	smoothDist := float32(0.15) * 181.0
	spillDist := (simDist + smoothDist) * 2
	spillDistSq := spillDist * spillDist
	invSpillDistSq := float32(1.0 / spillDistSq)
	var replaceCb float32 = 128
	var replaceCr float32 = 128

	b.SetBytes(int64(uvSize) * 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset chroma between iterations
		for j := range cb {
			cb[j] = 50
			cr[j] = 30
		}
		spillSuppressChroma(&cb[0], &cr[0], keyCb, keyCr, spillSup, invSpillDistSq, replaceCb, replaceCr, uvSize)
	}
}

// spillSuppressChromaGo is the pure Go reference implementation for baseline comparison.
func spillSuppressChromaGo(cbPlane, crPlane *byte, keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr float32, n int) {
	if n <= 0 {
		return
	}
	cbS := unsafe.Slice(cbPlane, n)
	crS := unsafe.Slice(crPlane, n)

	for i := 0; i < n; i++ {
		cb := float32(cbS[i])
		cr := float32(crS[i])
		dCb := cb - keyCb
		dCr := cr - keyCr
		distSq := dCb*dCb + dCr*dCr
		ratio := distSq * invSpillDistSq
		if ratio < 1.0 {
			spillAmount := spillSuppress * (1.0 - ratio)
			if spillAmount > 0 {
				newCb := cb + (replaceCb-cb)*spillAmount
				newCr := cr + (replaceCr-cr)*spillAmount
				if newCb < 0 {
					newCb = 0
				} else if newCb > 255 {
					newCb = 255
				}
				if newCr < 0 {
					newCr = 0
				} else if newCr > 255 {
					newCr = 255
				}
				cbS[i] = byte(newCb)
				crS[i] = byte(newCr)
			}
		}
	}
}

// BenchmarkSpillSuppressChromaGo_1080p_NearKey is the pure Go baseline.
func BenchmarkSpillSuppressChromaGo_1080p_NearKey(b *testing.B) {
	width, height := 1920, 1080
	uvWidth := width / 2
	uvHeight := height / 2
	uvSize := uvWidth * uvHeight

	cb := make([]byte, uvSize)
	cr := make([]byte, uvSize)
	for i := range cb {
		cb[i] = 50
		cr[i] = 30
	}

	var keyCb float32 = 30
	var keyCr float32 = 12
	var spillSup float32 = 0.8
	simDist := float32(0.3) * 181.0
	smoothDist := float32(0.15) * 181.0
	spillDist := (simDist + smoothDist) * 2
	spillDistSq := spillDist * spillDist
	invSpillDistSq := float32(1.0 / spillDistSq)
	var replaceCb float32 = 128
	var replaceCr float32 = 128

	b.SetBytes(int64(uvSize) * 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range cb {
			cb[j] = 50
			cr[j] = 30
		}
		spillSuppressChromaGo(&cb[0], &cr[0], keyCb, keyCr, spillSup, invSpillDistSq, replaceCb, replaceCr, uvSize)
	}
}

// BenchmarkSpillSuppressChroma_1080p_FarFromKey benchmarks spill suppression
// where all pixels are far from the key color (best case — early skip).
func BenchmarkSpillSuppressChroma_1080p_FarFromKey(b *testing.B) {
	width, height := 1920, 1080
	uvWidth := width / 2
	uvHeight := height / 2
	uvSize := uvWidth * uvHeight

	// Far-from-green chroma values
	cb := make([]byte, uvSize)
	cr := make([]byte, uvSize)
	for i := range cb {
		cb[i] = 200
		cr[i] = 200
	}

	var keyCb float32 = 30
	var keyCr float32 = 12
	var spillSup float32 = 0.8
	simDist := float32(0.3) * 181.0
	smoothDist := float32(0.15) * 181.0
	spillDist := (simDist + smoothDist) * 2
	spillDistSq := spillDist * spillDist
	invSpillDistSq := float32(1.0 / spillDistSq)
	var replaceCb float32 = 128
	var replaceCr float32 = 128

	b.SetBytes(int64(uvSize) * 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spillSuppressChroma(&cb[0], &cr[0], keyCb, keyCr, spillSup, invSpillDistSq, replaceCb, replaceCr, uvSize)
	}
}
