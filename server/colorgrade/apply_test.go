package colorgrade

import (
	"encoding/binary"
	"testing"
)

// The reference functions below transcribe the GPU kernels in
// gpu/cuda/colorgrade.cu line by line, per pixel, with no caching, so the
// CPU path is checked against the arithmetic the CUDA build runs.

func refLookup(lut []float32, size int, r, g, b float32) (float32, float32, float32) {
	fr := r * float32(size-1)
	fg := g * float32(size-1)
	fb := b * float32(size-1)
	r0 := min(max(int(fr), 0), size-2)
	g0 := min(max(int(fg), 0), size-2)
	b0 := min(max(int(fb), 0), size-2)
	dr := fr - float32(r0)
	dg := fg - float32(g0)
	db := fb - float32(b0)
	s2 := size * size
	at := func(ri, gi, bi, ch int) float32 { return lut[(bi*s2+gi*size+ri)*3+ch] }
	var out [3]float32
	for ch := 0; ch < 3; ch++ {
		c00 := at(r0, g0, b0, ch) + (at(r0+1, g0, b0, ch)-at(r0, g0, b0, ch))*dr
		c10 := at(r0, g0+1, b0, ch) + (at(r0+1, g0+1, b0, ch)-at(r0, g0+1, b0, ch))*dr
		c01 := at(r0, g0, b0+1, ch) + (at(r0+1, g0, b0+1, ch)-at(r0, g0, b0+1, ch))*dr
		c11 := at(r0, g0+1, b0+1, ch) + (at(r0+1, g0+1, b0+1, ch)-at(r0, g0+1, b0+1, ch))*dr
		c0 := c00 + (c10-c00)*dg
		c1 := c01 + (c11-c01)*dg
		out[ch] = c0 + (c1-c0)*db
	}
	return out[0], out[1], out[2]
}

func clip(v, lo, hi float32) float32 { return min(max(v, lo), hi) }

// refGrade grades normalized Y, Cb, Cr and returns Y', Cb', Cr' in the
// same normalized units, before the range scaling of each bit depth.
func refGrade(lut []float32, size int, yn, cbn, crn, intensity float32) (float32, float32, float32) {
	r := clip(yn+1.5748*crn, 0, 1)
	g := clip(yn-0.1873*cbn-0.4681*crn, 0, 1)
	b := clip(yn+1.8556*cbn, 0, 1)
	lr, lg, lb := refLookup(lut, size, r, g, b)
	fr := r + (lr-r)*intensity
	fg := g + (lg-g)*intensity
	fb := b + (lb-b)*intensity
	return 0.2126*fr + 0.7152*fg + 0.0722*fb,
		-0.1146*fr - 0.3854*fg + 0.5*fb,
		0.5*fr - 0.4542*fg - 0.0458*fb
}

// refGrade420 is colorgrade_lut3d_nv12_kernel on a planar 4:2:0 frame.
func refGrade420(src []byte, w, h int, lut []float32, size int, intensity float32) []byte {
	dst := append([]byte(nil), src...)
	ySize, cw := w*h, w/2
	cbOff, crOff := ySize, ySize+ySize/4
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ci := (y/2)*cw + x/2
			Y := float32(src[y*w+x])
			Cb := float32(src[cbOff+ci])
			Cr := float32(src[crOff+ci])
			yo, cbo, cro := refGrade(lut, size, (Y-16)/219, (Cb-128)/224, (Cr-128)/224, intensity)
			dst[y*w+x] = byte(clip(16+219*yo+0.5, 16, 235))
			if x%2 == 0 && y%2 == 0 {
				dst[cbOff+ci] = byte(clip(128+224*cbo+0.5, 16, 240))
				dst[crOff+ci] = byte(clip(128+224*cro+0.5, 16, 240))
			}
		}
	}
	return dst
}

// refGrade422P10 is colorgrade_lut3d_p210_kernel on a planar 4:2:2 10-bit
// frame.
func refGrade422P10(src []byte, w, h int, lut []float32, size int, intensity float32) []byte {
	le := binary.LittleEndian
	dst := append([]byte(nil), src...)
	cw := w / 2
	cbOff, crOff := w*h*2, w*h*2+cw*h*2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ci := (y*cw + x/2) * 2
			Y := float32(le.Uint16(src[(y*w+x)*2:]))
			Cb := float32(le.Uint16(src[cbOff+ci:]))
			Cr := float32(le.Uint16(src[crOff+ci:]))
			yo, cbo, cro := refGrade(lut, size, (Y-64)/876, (Cb-512)/896, (Cr-512)/896, intensity)
			le.PutUint16(dst[(y*w+x)*2:], uint16(clip(64+876*yo+0.5, 64, 940)))
			if x%2 == 0 {
				le.PutUint16(dst[cbOff+ci:], uint16(clip(512+896*cbo+0.5, 64, 960)))
				le.PutUint16(dst[crOff+ci:], uint16(clip(512+896*cro+0.5, 64, 960)))
			}
		}
	}
	return dst
}

// testFrame420 holds every Y code and a spread of chroma, so saturated and
// clipped colours are in it as well as neutral ones.
func testFrame420(w, h int) []byte {
	f := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		f[i] = byte(i * 37)
	}
	cw, ch := w/2, h/2
	for i := 0; i < cw*ch; i++ {
		f[w*h+i] = byte(i * 13)
		f[w*h+cw*ch+i] = byte(255 - i*29)
	}
	return f
}

func testFrame422P10(w, h int) []byte {
	le := binary.LittleEndian
	f := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		le.PutUint16(f[i*2:], uint16(i*37)&1023)
	}
	cw := w / 2
	for i := 0; i < cw*h; i++ {
		le.PutUint16(f[w*h*2+i*2:], uint16(i*13)&1023)
		le.PutUint16(f[w*h*2+cw*h*2+i*2:], uint16(1023-i*29)&1023)
	}
	return f
}

func lookLUT(t *testing.T, name string) *LUT3D {
	t.Helper()
	for _, p := range BuiltinLooks() {
		if p.Name == name {
			return GenerateLook(p)
		}
	}
	t.Fatalf("no built-in look %q", name)
	return nil
}

func TestApplyYUV420MatchesGPUKernel(t *testing.T) {
	const w, h = 96, 64
	for _, tc := range []struct {
		look      string
		intensity float32
	}{
		{"noir", 1}, {"teal-and-orange", 1}, {"kodachrome", 0.5}, {"hlg-to-sdr", 0.8},
	} {
		lut := lookLUT(t, tc.look)
		src := testFrame420(w, h)
		want := refGrade420(src, w, h, lut.Data, lut.Size, tc.intensity)
		g, err := NewGrader(lut.Data, lut.Size)
		if err != nil {
			t.Fatal(err)
		}
		// Twice: the second pass reads every sample from the cache the
		// first one filled.
		for pass := 0; pass < 2; pass++ {
			got := append([]byte(nil), src...)
			if err := g.ApplyYUV420(got, w, h, tc.intensity); err != nil {
				t.Fatal(err)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("%s@%v pass %d: byte %d = %d, GPU kernel gives %d", tc.look, tc.intensity, pass, i, got[i], want[i])
				}
			}
		}
	}
}

func TestApplyYUV422P10MatchesGPUKernel(t *testing.T) {
	const w, h = 96, 64
	le := binary.LittleEndian
	for _, tc := range []struct {
		look      string
		intensity float32
	}{
		{"noir", 1}, {"teal-and-orange", 1}, {"kodachrome", 0.5},
	} {
		lut := lookLUT(t, tc.look)
		src := testFrame422P10(w, h)
		want := refGrade422P10(src, w, h, lut.Data, lut.Size, tc.intensity)
		g, err := NewGrader(lut.Data, lut.Size)
		if err != nil {
			t.Fatal(err)
		}
		got := append([]byte(nil), src...)
		if err := g.ApplyYUV422P10(got, w, h, tc.intensity); err != nil {
			t.Fatal(err)
		}
		// Odd luma samples interpolate a luma table instead of three
		// channels: equal in exact arithmetic, one code apart at most in
		// float32.
		for i := 0; i < len(want); i += 2 {
			d := int(le.Uint16(got[i:])) - int(le.Uint16(want[i:]))
			if d < -1 || d > 1 {
				t.Fatalf("%s@%v: sample %d = %d, GPU kernel gives %d", tc.look, tc.intensity, i/2, le.Uint16(got[i:]), le.Uint16(want[i:]))
			}
		}
	}
}

// TestApplyYUV420IntensityChangeRegrades guards the cache: graded values
// belong to one intensity and must not be served for another.
func TestApplyYUV420IntensityChangeRegrades(t *testing.T) {
	const w, h = 32, 16
	lut := lookLUT(t, "noir")
	g, _ := NewGrader(lut.Data, lut.Size)
	src := testFrame420(w, h)
	first := append([]byte(nil), src...)
	_ = g.ApplyYUV420(first, w, h, 1)
	got := append([]byte(nil), src...)
	_ = g.ApplyYUV420(got, w, h, 0.25)
	want := refGrade420(src, w, h, lut.Data, lut.Size, 0.25)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("after intensity change byte %d = %d, want %d", i, got[i], want[i])
		}
	}
}

// TestApplyNoirRemovesChroma checks the effect an operator sees: the noir
// look is monochrome, so colour bars come out neutral grey.
func TestApplyNoirRemovesChroma(t *testing.T) {
	const w, h = 16, 2
	// Y, Cb, Cr of 75% BT.709 bars: yellow, cyan, green, magenta, red, blue.
	bars := [][3]byte{{168, 44, 136}, {145, 147, 44}, {134, 63, 52}, {63, 193, 204}, {51, 109, 212}, {28, 212, 120}}
	lut := lookLUT(t, "noir")
	g, _ := NewGrader(lut.Data, lut.Size)
	for _, bar := range bars {
		f := make([]byte, w*h*3/2)
		for i := 0; i < w*h; i++ {
			f[i] = bar[0]
		}
		for i := 0; i < w*h/4; i++ {
			f[w*h+i] = bar[1]
			f[w*h+w*h/4+i] = bar[2]
		}
		if err := g.ApplyYUV420(f, w, h, 1); err != nil {
			t.Fatal(err)
		}
		cb, cr := int(f[w*h]), int(f[w*h+w*h/4])
		if cb < 126 || cb > 130 || cr < 126 || cr > 130 {
			t.Errorf("bar %v graded to Cb=%d Cr=%d, want neutral 128", bar, cb, cr)
		}
	}
}

// The GPU tests pin an identity LUT to preserve white, black and mid grey
// within 3 codes, and intensity 0 to change nothing.
func TestApplyIdentityAndZeroIntensity(t *testing.T) {
	id := GenerateIdentityLUT()
	g, _ := NewGrader(id.Data, id.Size)
	le := binary.LittleEndian
	for _, c := range [][3]uint16{{940, 512, 512}, {64, 512, 512}, {502, 512, 512}} {
		f := make([]byte, 8*2*4)
		for i := 0; i < 16; i++ {
			le.PutUint16(f[i*2:], c[0])
		}
		for i := 0; i < 8; i++ {
			le.PutUint16(f[32+i*2:], c[1])
			le.PutUint16(f[48+i*2:], c[2])
		}
		_ = g.ApplyYUV422P10(f, 8, 2, 1)
		for i, want := range c {
			got := le.Uint16(f[[]int{0, 32, 48}[i]:])
			if d := int(got) - int(want); d < -3 || d > 3 {
				t.Errorf("identity 10-bit %v: plane %d = %d", c, i, got)
			}
		}
	}
	src := testFrame420(32, 16)
	lut := lookLUT(t, "noir")
	gn, _ := NewGrader(lut.Data, lut.Size)
	got := append([]byte(nil), src...)
	_ = gn.ApplyYUV420(got, 32, 16, 0)
	want := refGrade420(src, 32, 16, lut.Data, lut.Size, 0)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("intensity 0: byte %d = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestNewGraderRejectsBadLUT(t *testing.T) {
	if _, err := NewGrader(make([]float32, 10), 33); err == nil {
		t.Error("short LUT accepted")
	}
	if _, err := NewGrader(nil, 1); err == nil {
		t.Error("size 1 accepted")
	}
}

func TestApplyRejectsShortFrame(t *testing.T) {
	id := GenerateIdentityLUT()
	g, _ := NewGrader(id.Data, id.Size)
	if err := g.ApplyYUV420(make([]byte, 10), 16, 16, 1); err == nil {
		t.Error("short 4:2:0 frame accepted")
	}
	if err := g.ApplyYUV422P10(make([]byte, 10), 16, 16, 1); err == nil {
		t.Error("short 4:2:2 frame accepted")
	}
}
