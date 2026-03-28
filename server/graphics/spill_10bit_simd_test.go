package graphics

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// TestSpillSuppress10Row_DirectKernel tests the low-level spill suppression kernel
// against the Go reference.
func TestSpillSuppress10Row_DirectKernel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))

	for _, count := range []int{1, 3, 4, 7, 8, 9, 15, 16, 17, 32, 64, 100} {
		cbData := make([]byte, count*2)
		crData := make([]byte, count*2)
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint16(cbData[i*2:], uint16(rng.Intn(1024)))
			binary.LittleEndian.PutUint16(crData[i*2:], uint16(rng.Intn(1024)))
		}

		keyCb, keyCr := float32(300), float32(700)
		spillSuppress := float32(0.5)
		spillDist := float32(200)
		invSpillDistSq := float32(1.0) / (spillDist * spillDist)
		replaceCb, replaceCr := float32(512), float32(512)

		refCb := make([]byte, count*2)
		refCr := make([]byte, count*2)
		gotCb := make([]byte, count*2)
		gotCr := make([]byte, count*2)
		copy(refCb, cbData)
		copy(refCr, crData)
		copy(gotCb, cbData)
		copy(gotCr, crData)

		spillSuppress10RowGo(&refCb[0], &refCr[0], count, keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr)
		spillSuppress10Row(&gotCb[0], &gotCr[0], count, keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr)

		if !bytes.Equal(refCb, gotCb) || !bytes.Equal(refCr, gotCr) {
			for i := 0; i < count; i++ {
				rCb := binary.LittleEndian.Uint16(refCb[i*2:])
				gCb := binary.LittleEndian.Uint16(gotCb[i*2:])
				rCr := binary.LittleEndian.Uint16(refCr[i*2:])
				gCr := binary.LittleEndian.Uint16(gotCr[i*2:])
				if rCb != gCb || rCr != gCr {
					t.Fatalf("count=%d sample %d: refCb=%d gotCb=%d refCr=%d gotCr=%d",
						count, i, rCb, gCb, rCr, gCr)
				}
			}
		}
	}
}

// TestSpillSuppress10Row_NoChange tests that far-from-key pixels are unchanged.
func TestSpillSuppress10Row_NoChange(t *testing.T) {
	t.Parallel()
	count := 16
	cbData := make([]byte, count*2)
	crData := make([]byte, count*2)
	for i := 0; i < count; i++ {
		// Far from key color (300, 700)
		binary.LittleEndian.PutUint16(cbData[i*2:], 900)
		binary.LittleEndian.PutUint16(crData[i*2:], 100)
	}

	keyCb, keyCr := float32(300), float32(700)
	spillSuppress := float32(0.5)
	spillDist := float32(50) // very small spill distance
	invSpillDistSq := float32(1.0) / (spillDist * spillDist)
	replaceCb, replaceCr := float32(512), float32(512)

	refCb := make([]byte, count*2)
	refCr := make([]byte, count*2)
	copy(refCb, cbData)
	copy(refCr, crData)

	spillSuppress10Row(&refCb[0], &refCr[0], count, keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr)

	// Should be unchanged
	if !bytes.Equal(refCb, cbData) || !bytes.Equal(refCr, crData) {
		t.Fatal("far-from-key pixels should not be modified")
	}
}

// TestSpillSuppress10Row_NearKey tests that near-key pixels are modified.
func TestSpillSuppress10Row_NearKey(t *testing.T) {
	t.Parallel()
	count := 16
	cbData := make([]byte, count*2)
	crData := make([]byte, count*2)
	for i := 0; i < count; i++ {
		// Very close to key color (300, 700)
		binary.LittleEndian.PutUint16(cbData[i*2:], 310)
		binary.LittleEndian.PutUint16(crData[i*2:], 690)
	}

	keyCb, keyCr := float32(300), float32(700)
	spillSuppress := float32(1.0) // max suppression
	spillDist := float32(200)
	invSpillDistSq := float32(1.0) / (spillDist * spillDist)
	replaceCb, replaceCr := float32(512), float32(512)

	gotCb := make([]byte, count*2)
	gotCr := make([]byte, count*2)
	copy(gotCb, cbData)
	copy(gotCr, crData)

	spillSuppress10Row(&gotCb[0], &gotCr[0], count, keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr)

	// Should be modified (pulled toward replacement color 512, 512)
	origCb := binary.LittleEndian.Uint16(cbData[0:])
	newCb := binary.LittleEndian.Uint16(gotCb[0:])
	if newCb == origCb {
		t.Errorf("near-key Cb should be modified: still %d", origCb)
	}
}

func BenchmarkSpillSuppress10_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	chromaW := w / 2
	count := chromaW * h
	cbData := make([]byte, count*2)
	crData := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(cbData[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(crData[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(count * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spillSuppress10RowGo(&cbData[0], &crData[0], count, 300, 700, 0.5, 1.0/(200*200), 512, 512)
	}
}

func BenchmarkSpillSuppress10_SIMD_1080p(b *testing.B) {
	w, h := 1920, 1080
	chromaW := w / 2
	count := chromaW * h
	cbData := make([]byte, count*2)
	crData := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(cbData[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(crData[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(count * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spillSuppress10Row(&cbData[0], &crData[0], count, 300, 700, 0.5, 1.0/(200*200), 512, 512)
	}
}
