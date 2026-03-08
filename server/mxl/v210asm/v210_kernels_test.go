package v210asm

import (
	"encoding/binary"
	"testing"
)

// --- ChromaVAvg tests ---

func TestChromaVAvg_Basic(t *testing.T) {
	top := []byte{100, 200, 50, 150}
	bot := []byte{200, 100, 150, 50}
	dst := make([]byte, 4)

	ChromaVAvg(&dst[0], &top[0], &bot[0], 4)

	// (100+200+1)>>1 = 150, (200+100+1)>>1 = 150, (50+150+1)>>1 = 100, (150+50+1)>>1 = 100
	expected := []byte{150, 150, 100, 100}
	for i, want := range expected {
		if dst[i] != want {
			t.Errorf("dst[%d] = %d, want %d", i, dst[i], want)
		}
	}
}

func TestChromaVAvg_ZeroPlusZero(t *testing.T) {
	top := []byte{0, 0, 0, 0}
	bot := []byte{0, 0, 0, 0}
	dst := make([]byte, 4)

	ChromaVAvg(&dst[0], &top[0], &bot[0], 4)

	for i := range dst {
		if dst[i] != 0 {
			t.Errorf("dst[%d] = %d, want 0", i, dst[i])
		}
	}
}

func TestChromaVAvg_MaxPlusMax(t *testing.T) {
	top := []byte{255, 255, 255, 255}
	bot := []byte{255, 255, 255, 255}
	dst := make([]byte, 4)

	ChromaVAvg(&dst[0], &top[0], &bot[0], 4)

	for i := range dst {
		if dst[i] != 255 {
			t.Errorf("dst[%d] = %d, want 255", i, dst[i])
		}
	}
}

func TestChromaVAvg_ZeroPlusMax(t *testing.T) {
	top := []byte{0, 0, 0, 0}
	bot := []byte{255, 255, 255, 255}
	dst := make([]byte, 4)

	ChromaVAvg(&dst[0], &top[0], &bot[0], 4)

	// (0+255+1)>>1 = 128
	for i := range dst {
		if dst[i] != 128 {
			t.Errorf("dst[%d] = %d, want 128", i, dst[i])
		}
	}
}

func TestChromaVAvg_OddRounding(t *testing.T) {
	// (1+0+1)>>1 = 1, (0+1+1)>>1 = 1 — rounds up
	top := []byte{1}
	bot := []byte{0}
	dst := make([]byte, 1)
	ChromaVAvg(&dst[0], &top[0], &bot[0], 1)
	if dst[0] != 1 {
		t.Errorf("dst[0] = %d, want 1 (rounds up)", dst[0])
	}
}

func TestChromaVAvg_LargeN(t *testing.T) {
	n := 960 // 1920/2, typical 1080p chroma row width
	top := make([]byte, n)
	bot := make([]byte, n)
	dst := make([]byte, n)

	for i := 0; i < n; i++ {
		top[i] = byte(i % 256)
		bot[i] = byte((i + 128) % 256)
	}

	ChromaVAvg(&dst[0], &top[0], &bot[0], n)

	for i := 0; i < n; i++ {
		expected := byte((uint16(top[i]) + uint16(bot[i]) + 1) >> 1)
		if dst[i] != expected {
			t.Errorf("dst[%d] = %d, want %d (top=%d, bot=%d)", i, dst[i], expected, top[i], bot[i])
		}
	}
}

// --- V210UnpackRow tests ---

func TestV210UnpackRow_SingleGroup(t *testing.T) {
	// Build one V210 group (16 bytes = 4 uint32 words) with known 10-bit values
	// Word 0: Cb0=400, Y0=512, Cr0=800
	// Word 1: Y1=256, Cb2=600, Y2=700
	// Word 2: Cr2=300, Y3=100, Cb4=900
	// Word 3: Y4=1000, Cr4=200, Y5=64
	v210 := make([]byte, 16)
	binary.LittleEndian.PutUint32(v210[0:], packV210Word(400, 512, 800))
	binary.LittleEndian.PutUint32(v210[4:], packV210Word(256, 600, 700))
	binary.LittleEndian.PutUint32(v210[8:], packV210Word(300, 100, 900))
	binary.LittleEndian.PutUint32(v210[12:], packV210Word(1000, 200, 64))

	yOut := make([]byte, 6)
	cbOut := make([]byte, 3)
	crOut := make([]byte, 3)

	V210UnpackRow(&yOut[0], &cbOut[0], &crOut[0], &v210[0], 1)

	// Y values (10-bit >> 2 = 8-bit): 512>>2=128, 256>>2=64, 700>>2=175, 100>>2=25, 1000>>2=250, 64>>2=16
	expectedY := []byte{128, 64, 175, 25, 250, 16}
	for i, want := range expectedY {
		if yOut[i] != want {
			t.Errorf("Y[%d] = %d, want %d", i, yOut[i], want)
		}
	}

	// Cb values: 400>>2=100, 600>>2=150, 900>>2=225
	expectedCb := []byte{100, 150, 225}
	for i, want := range expectedCb {
		if cbOut[i] != want {
			t.Errorf("Cb[%d] = %d, want %d", i, cbOut[i], want)
		}
	}

	// Cr values: 800>>2=200, 300>>2=75, 200>>2=50
	expectedCr := []byte{200, 75, 50}
	for i, want := range expectedCr {
		if crOut[i] != want {
			t.Errorf("Cr[%d] = %d, want %d", i, crOut[i], want)
		}
	}
}

func TestV210UnpackRow_MultipleGroups(t *testing.T) {
	groups := 4
	v210 := make([]byte, groups*16)
	yOut := make([]byte, groups*6)
	cbOut := make([]byte, groups*3)
	crOut := make([]byte, groups*3)

	// Fill each group with distinct pattern
	for g := 0; g < groups; g++ {
		base := uint32(g*100 + 64)
		offset := g * 16
		binary.LittleEndian.PutUint32(v210[offset:], packV210Word(base, base+4, base+8))
		binary.LittleEndian.PutUint32(v210[offset+4:], packV210Word(base+12, base+16, base+20))
		binary.LittleEndian.PutUint32(v210[offset+8:], packV210Word(base+24, base+28, base+32))
		binary.LittleEndian.PutUint32(v210[offset+12:], packV210Word(base+36, base+40, base+44))
	}

	V210UnpackRow(&yOut[0], &cbOut[0], &crOut[0], &v210[0], groups)

	// Verify against manual extraction for each group
	for g := 0; g < groups; g++ {
		offset := g * 16

		w0 := binary.LittleEndian.Uint32(v210[offset:])
		w1 := binary.LittleEndian.Uint32(v210[offset+4:])
		w2 := binary.LittleEndian.Uint32(v210[offset+8:])
		w3 := binary.LittleEndian.Uint32(v210[offset+12:])

		// Extract expected Y values
		expY := [6]byte{
			byte((w0 >> 10 & 0x3FF) >> 2), // Y0
			byte((w1 & 0x3FF) >> 2),       // Y1
			byte((w1 >> 20 & 0x3FF) >> 2), // Y2
			byte((w2 >> 10 & 0x3FF) >> 2), // Y3
			byte((w3 & 0x3FF) >> 2),       // Y4
			byte((w3 >> 20 & 0x3FF) >> 2), // Y5
		}
		for i, want := range expY {
			got := yOut[g*6+i]
			if got != want {
				t.Errorf("group %d Y[%d] = %d, want %d", g, i, got, want)
			}
		}
	}
}

func TestV210UnpackRow_ZeroGroups(t *testing.T) {
	// Should be a no-op
	v210 := make([]byte, 16)
	yOut := make([]byte, 6)
	cbOut := make([]byte, 3)
	crOut := make([]byte, 3)
	V210UnpackRow(&yOut[0], &cbOut[0], &crOut[0], &v210[0], 0)
	// No crash = pass
}

func TestV210UnpackRow_320Groups(t *testing.T) {
	// 1920 pixels wide = 320 groups
	groups := 320
	v210 := make([]byte, groups*16)
	yOut := make([]byte, groups*6)
	cbOut := make([]byte, groups*3)
	crOut := make([]byte, groups*3)

	// Fill with a gradient pattern
	for g := 0; g < groups; g++ {
		val := uint32((g * 3) % 1024)
		offset := g * 16
		binary.LittleEndian.PutUint32(v210[offset:], packV210Word(val, (val+100)%1024, (val+200)%1024))
		binary.LittleEndian.PutUint32(v210[offset+4:], packV210Word((val+300)%1024, (val+400)%1024, (val+500)%1024))
		binary.LittleEndian.PutUint32(v210[offset+8:], packV210Word((val+600)%1024, (val+700)%1024, (val+800)%1024))
		binary.LittleEndian.PutUint32(v210[offset+12:], packV210Word((val+900)%1024, (val+50)%1024, (val+150)%1024))
	}

	V210UnpackRow(&yOut[0], &cbOut[0], &crOut[0], &v210[0], groups)

	// Verify against manual extraction
	for g := 0; g < groups; g++ {
		offset := g * 16
		w0 := binary.LittleEndian.Uint32(v210[offset:])

		wantY0 := byte((w0 >> 10 & 0x3FF) >> 2)
		gotY0 := yOut[g*6]
		if gotY0 != wantY0 {
			t.Fatalf("group %d Y[0] = %d, want %d", g, gotY0, wantY0)
		}

		wantCb0 := byte((w0 & 0x3FF) >> 2)
		gotCb0 := cbOut[g*3]
		if gotCb0 != wantCb0 {
			t.Fatalf("group %d Cb[0] = %d, want %d", g, gotCb0, wantCb0)
		}
	}
}

// --- V210PackRow tests ---

func TestV210PackRow_SingleGroup(t *testing.T) {
	yIn := []byte{128, 64, 175, 25, 250, 16}
	cbIn := []byte{100, 150, 225}
	crIn := []byte{200, 75, 50}
	v210Out := make([]byte, 16)

	V210PackRow(&v210Out[0], &yIn[0], &cbIn[0], &crIn[0], 1)

	// Verify packed values
	w0 := binary.LittleEndian.Uint32(v210Out[0:])
	w1 := binary.LittleEndian.Uint32(v210Out[4:])
	w2 := binary.LittleEndian.Uint32(v210Out[8:])
	w3 := binary.LittleEndian.Uint32(v210Out[12:])

	// W0: Cb0=100<<2=400, Y0=128<<2=512, Cr0=200<<2=800
	if w0&0x3FF != 400 {
		t.Errorf("W0 Cb0 = %d, want 400", w0&0x3FF)
	}
	if (w0>>10)&0x3FF != 512 {
		t.Errorf("W0 Y0 = %d, want 512", (w0>>10)&0x3FF)
	}
	if (w0>>20)&0x3FF != 800 {
		t.Errorf("W0 Cr0 = %d, want 800", (w0>>20)&0x3FF)
	}

	// W1: Y1=64<<2=256, Cb2=150<<2=600, Y2=175<<2=700
	if w1&0x3FF != 256 {
		t.Errorf("W1 Y1 = %d, want 256", w1&0x3FF)
	}
	if (w1>>10)&0x3FF != 600 {
		t.Errorf("W1 Cb2 = %d, want 600", (w1>>10)&0x3FF)
	}
	if (w1>>20)&0x3FF != 700 {
		t.Errorf("W1 Y2 = %d, want 700", (w1>>20)&0x3FF)
	}

	// W2: Cr2=75<<2=300, Y3=25<<2=100, Cb4=225<<2=900
	if w2&0x3FF != 300 {
		t.Errorf("W2 Cr2 = %d, want 300", w2&0x3FF)
	}
	if (w2>>10)&0x3FF != 100 {
		t.Errorf("W2 Y3 = %d, want 100", (w2>>10)&0x3FF)
	}
	if (w2>>20)&0x3FF != 900 {
		t.Errorf("W2 Cb4 = %d, want 900", (w2>>20)&0x3FF)
	}

	// W3: Y4=250<<2=1000, Cr4=50<<2=200, Y5=16<<2=64
	if w3&0x3FF != 1000 {
		t.Errorf("W3 Y4 = %d, want 1000", w3&0x3FF)
	}
	if (w3>>10)&0x3FF != 200 {
		t.Errorf("W3 Cr4 = %d, want 200", (w3>>10)&0x3FF)
	}
	if (w3>>20)&0x3FF != 64 {
		t.Errorf("W3 Y5 = %d, want 64", (w3>>20)&0x3FF)
	}
}

func TestV210PackRow_ZeroGroups(t *testing.T) {
	v210Out := make([]byte, 16)
	yIn := make([]byte, 6)
	cbIn := make([]byte, 3)
	crIn := make([]byte, 3)
	V210PackRow(&v210Out[0], &yIn[0], &cbIn[0], &crIn[0], 0)
	// No crash = pass
}

// --- Round-trip: unpack → pack ---

func TestV210UnpackPackRoundTrip(t *testing.T) {
	groups := 320 // 1920 pixels
	v210In := make([]byte, groups*16)

	// Fill with realistic V210 data
	for g := 0; g < groups; g++ {
		offset := g * 16
		y := uint32(64 + (g*7)%876)   // 10-bit luma: 64-940
		cb := uint32(64 + (g*13)%896) // 10-bit chroma
		cr := uint32(64 + (g*17)%896)
		binary.LittleEndian.PutUint32(v210In[offset:], packV210Word(cb, y, cr))
		binary.LittleEndian.PutUint32(v210In[offset+4:], packV210Word(y+4, cb+8, y+12))
		binary.LittleEndian.PutUint32(v210In[offset+8:], packV210Word(cr+16, y+20, cb+24))
		binary.LittleEndian.PutUint32(v210In[offset+12:], packV210Word(y+28, cr+32, y+36))
	}

	// Unpack
	yBuf := make([]byte, groups*6)
	cbBuf := make([]byte, groups*3)
	crBuf := make([]byte, groups*3)
	V210UnpackRow(&yBuf[0], &cbBuf[0], &crBuf[0], &v210In[0], groups)

	// Pack back
	v210Out := make([]byte, groups*16)
	V210PackRow(&v210Out[0], &yBuf[0], &cbBuf[0], &crBuf[0], groups)

	// Compare: should match within 8-bit→10-bit→8-bit truncation tolerance
	for g := 0; g < groups; g++ {
		offset := g * 16
		for w := 0; w < 4; w++ {
			orig := binary.LittleEndian.Uint32(v210In[offset+w*4:])
			got := binary.LittleEndian.Uint32(v210Out[offset+w*4:])

			origMasked := maskV210Bottom2(orig)
			gotMasked := maskV210Bottom2(got)

			if origMasked != gotMasked {
				t.Fatalf("group %d word %d: orig(masked)=0x%08X, got(masked)=0x%08X",
					g, w, origMasked, gotMasked)
			}
		}
	}
}

// maskV210Bottom2 zeros the bottom 2 bits of each 10-bit field in a V210 word.
func maskV210Bottom2(word uint32) uint32 {
	f0 := (word & 0x3FF) &^ 3
	f1 := ((word >> 10) & 0x3FF) &^ 3
	f2 := ((word >> 20) & 0x3FF) &^ 3
	return f0 | (f1 << 10) | (f2 << 20)
}

// --- Benchmarks ---

func BenchmarkChromaVAvg_1080p(b *testing.B) {
	n := 960 // 1920/2
	top := make([]byte, n)
	bot := make([]byte, n)
	dst := make([]byte, n)
	for i := range top {
		top[i] = byte(i % 256)
		bot[i] = byte((i + 128) % 256)
	}

	b.SetBytes(int64(n))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ChromaVAvg(&dst[0], &top[0], &bot[0], n)
	}
}

func BenchmarkV210UnpackRow_1080p(b *testing.B) {
	groups := 320 // 1920/6
	v210 := make([]byte, groups*16)
	yOut := make([]byte, groups*6)
	cbOut := make([]byte, groups*3)
	crOut := make([]byte, groups*3)
	for i := range v210 {
		v210[i] = byte(i % 256)
	}

	b.SetBytes(int64(groups * 16))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		V210UnpackRow(&yOut[0], &cbOut[0], &crOut[0], &v210[0], groups)
	}
}

func BenchmarkV210PackRow_1080p(b *testing.B) {
	groups := 320 // 1920/6
	v210Out := make([]byte, groups*16)
	yIn := make([]byte, groups*6)
	cbIn := make([]byte, groups*3)
	crIn := make([]byte, groups*3)
	for i := range yIn {
		yIn[i] = byte(i % 256)
	}
	for i := range cbIn {
		cbIn[i] = byte((i + 64) % 256)
		crIn[i] = byte((i + 128) % 256)
	}

	b.SetBytes(int64(groups * 16))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		V210PackRow(&v210Out[0], &yIn[0], &cbIn[0], &crIn[0], groups)
	}
}

// packV210Word packs three 10-bit values into a V210 word.
func packV210Word(a, b, c uint32) uint32 {
	return (a & 0x3FF) | ((b & 0x3FF) << 10) | ((c & 0x3FF) << 20)
}
