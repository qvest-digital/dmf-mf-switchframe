package graphics

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// alphaBlendRGBA10GoRef is the Go reference for alpha blend, used to verify SIMD.
func alphaBlendRGBA10GoRef(yuv []byte, rgba []byte, frameW, frameH int) error {
	return AlphaBlendRGBA10(yuv, rgba, frameW, frameH)
}

// TestAlphaBlendRGBA10SIMD_MatchesReference verifies SIMD dispatch produces
// bit-identical results to the Go reference.
func TestAlphaBlendRGBA10SIMD_MatchesReference(t *testing.T) {
	t.Parallel()
	w, h := 64, 32
	rng := rand.New(rand.NewSource(42))

	yuvRef := make([]byte, w*h*4)
	yuvGot := make([]byte, w*h*4)
	for i := 0; i < len(yuvRef)/2; i++ {
		v := uint16(rng.Intn(1024))
		binary.LittleEndian.PutUint16(yuvRef[i*2:], v)
		binary.LittleEndian.PutUint16(yuvGot[i*2:], v)
	}

	rgba := make([]byte, w*h*4)
	for i := range rgba {
		rgba[i] = byte(rng.Intn(256))
	}

	if err := alphaBlendRGBA10GoRef(yuvRef, rgba, w, h); err != nil {
		t.Fatal(err)
	}
	if err := alphaBlendRGBA10Dispatch(yuvGot, rgba, w, h); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(yuvRef, yuvGot) {
		for i := 0; i < len(yuvRef); i += 2 {
			rv := binary.LittleEndian.Uint16(yuvRef[i:])
			gv := binary.LittleEndian.Uint16(yuvGot[i:])
			if rv != gv {
				t.Fatalf("mismatch at byte %d (sample %d): ref=%d, got=%d", i, i/2, rv, gv)
			}
		}
	}
}

// TestAlphaBlendRGBA10YRow_DirectKernel tests the Y plane row kernel.
func TestAlphaBlendRGBA10YRow_DirectKernel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(77))

	for _, count := range []int{1, 3, 4, 7, 8, 9, 15, 16, 17, 32, 64, 100} {
		yuvData := make([]byte, count*2)
		rgba := make([]byte, count*4)
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint16(yuvData[i*2:], uint16(rng.Intn(1024)))
			rgba[i*4+0] = byte(rng.Intn(256))
			rgba[i*4+1] = byte(rng.Intn(256))
			rgba[i*4+2] = byte(rng.Intn(256))
			rgba[i*4+3] = byte(rng.Intn(256))
		}

		ref := make([]byte, count*2)
		got := make([]byte, count*2)
		copy(ref, yuvData)
		copy(got, yuvData)

		alphaBlendRGBA10YRowGo(&ref[0], &rgba[0], count)
		alphaBlendRGBA10YRow(&got[0], &rgba[0], count)

		if !bytes.Equal(ref, got) {
			for i := 0; i < count; i++ {
				rv := binary.LittleEndian.Uint16(ref[i*2:])
				gv := binary.LittleEndian.Uint16(got[i*2:])
				if rv != gv {
					t.Fatalf("count=%d pixel %d: ref=%d, got=%d (R=%d G=%d B=%d A=%d yuv=%d)",
						count, i, rv, gv,
						rgba[i*4], rgba[i*4+1], rgba[i*4+2], rgba[i*4+3],
						binary.LittleEndian.Uint16(yuvData[i*2:]))
				}
			}
		}
	}
}

// TestAlphaBlendRGBA10UVRow_DirectKernel tests the chroma row kernel.
func TestAlphaBlendRGBA10UVRow_DirectKernel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(99))

	for _, chromaCount := range []int{1, 3, 4, 7, 8, 9, 15, 16, 17, 32, 64} {
		cbData := make([]byte, chromaCount*2)
		crData := make([]byte, chromaCount*2)
		rgba := make([]byte, chromaCount*2*4) // 2 pixels per chroma sample
		for i := 0; i < chromaCount; i++ {
			binary.LittleEndian.PutUint16(cbData[i*2:], uint16(rng.Intn(1024)))
			binary.LittleEndian.PutUint16(crData[i*2:], uint16(rng.Intn(1024)))
		}
		for i := range rgba {
			rgba[i] = byte(rng.Intn(256))
		}

		refCb := make([]byte, chromaCount*2)
		refCr := make([]byte, chromaCount*2)
		gotCb := make([]byte, chromaCount*2)
		gotCr := make([]byte, chromaCount*2)
		copy(refCb, cbData)
		copy(refCr, crData)
		copy(gotCb, cbData)
		copy(gotCr, crData)

		alphaBlendRGBA10UVRowGo(&refCb[0], &refCr[0], &rgba[0], chromaCount)
		alphaBlendRGBA10UVRow(&gotCb[0], &gotCr[0], &rgba[0], chromaCount)

		if !bytes.Equal(refCb, gotCb) || !bytes.Equal(refCr, gotCr) {
			for i := 0; i < chromaCount; i++ {
				rCb := binary.LittleEndian.Uint16(refCb[i*2:])
				gCb := binary.LittleEndian.Uint16(gotCb[i*2:])
				rCr := binary.LittleEndian.Uint16(refCr[i*2:])
				gCr := binary.LittleEndian.Uint16(gotCr[i*2:])
				if rCb != gCb || rCr != gCr {
					t.Fatalf("count=%d chroma %d: refCb=%d gotCb=%d refCr=%d gotCr=%d",
						chromaCount, i, rCb, gCb, rCr, gCr)
				}
			}
		}
	}
}

// TestAlphaBlendRGBA10SIMD_TransparentOverlay verifies transparent RGBA leaves YUV unchanged.
func TestAlphaBlendRGBA10SIMD_TransparentOverlay(t *testing.T) {
	t.Parallel()
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 500, 400, 600)
	yuvOrig := make([]byte, len(yuv))
	copy(yuvOrig, yuv)

	rgba := make([]byte, w*h*4) // all zeros = transparent
	if err := alphaBlendRGBA10Dispatch(yuv, rgba, w, h); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(yuvOrig, yuv) {
		t.Fatal("transparent overlay should not modify YUV")
	}
}

// TestAlphaBlendRGBA10SIMD_OpaqueWhite tests opaque white overlay via SIMD.
func TestAlphaBlendRGBA10SIMD_OpaqueWhite(t *testing.T) {
	t.Parallel()
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 64, 512, 512)
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 255
		rgba[i*4+1] = 255
		rgba[i*4+2] = 255
		rgba[i*4+3] = 255
	}

	if err := alphaBlendRGBA10Dispatch(yuv, rgba, w, h); err != nil {
		t.Fatal(err)
	}

	gotY := readY10g(yuv, 0, 0, w)
	if gotY < 900 || gotY > 960 {
		t.Errorf("opaque white Y=%d, want ~940", gotY)
	}
}

func BenchmarkAlphaBlendRGBA10_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	yuv := make([]byte, w*h*4)
	rgba := make([]byte, w*h*4)
	rng := rand.New(rand.NewSource(42))
	for i := range rgba {
		rgba[i] = byte(rng.Intn(256))
	}
	b.SetBytes(int64(w * h * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AlphaBlendRGBA10(yuv, rgba, w, h)
	}
}

func BenchmarkAlphaBlendRGBA10_SIMD_1080p(b *testing.B) {
	w, h := 1920, 1080
	yuv := make([]byte, w*h*4)
	rgba := make([]byte, w*h*4)
	rng := rand.New(rand.NewSource(42))
	for i := range rgba {
		rgba[i] = byte(rng.Intn(256))
	}
	b.SetBytes(int64(w * h * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alphaBlendRGBA10Dispatch(yuv, rgba, w, h)
	}
}
