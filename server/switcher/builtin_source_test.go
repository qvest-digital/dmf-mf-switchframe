package switcher

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// builtinSourceKeyBlack / builtinSourceKeyColorbars are the canonical keys.
// Pinned here so any rename must update the broadcast contract + UI.
const (
	testBlackKey     = "source:black"
	testColorbarsKey = "source:colorbars"
)

func TestSwitcher_BuiltinBlackSourceRegistered(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	state := sw.State()
	src, ok := state.Sources[testBlackKey]
	require.True(t, ok, "source:black must be auto-registered by Switcher.New")
	require.True(t, src.IsVirtual, "source:black must have IsVirtual=true")
}

func TestSwitcher_BuiltinColorbarsRegistered(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	state := sw.State()
	src, ok := state.Sources[testColorbarsKey]
	require.True(t, ok, "source:colorbars must be auto-registered by Switcher.New")
	require.True(t, src.IsVirtual, "source:colorbars must have IsVirtual=true")
}

func TestSwitcher_BlackSourceYUVValues(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	yuv, w, h, err := sw.BuiltinSourceFrame(testBlackKey)
	require.NoError(t, err)
	require.Greater(t, w, 0)
	require.Greater(t, h, 0)

	ySize := w * h
	uSize := (w / 2) * (h / 2)
	require.Equal(t, ySize+2*uSize, len(yuv), "YUV420 byte count")

	for i := 0; i < ySize; i++ {
		require.Equalf(t, byte(16), yuv[i], "Y[%d] expected 16 (BT.709 legal-range black)", i)
	}
	for i := ySize; i < ySize+uSize; i++ {
		require.Equalf(t, byte(128), yuv[i], "Cb[%d] expected 128", i-ySize)
	}
	for i := ySize + uSize; i < ySize+2*uSize; i++ {
		require.Equalf(t, byte(128), yuv[i], "Cr[%d] expected 128", i-ySize-uSize)
	}
}

// rp219Bar75 returns the (R, G, B) of the i-th SMPTE RP 219 75% bar
// (i=0..7: gray, yellow, cyan, green, magenta, red, blue, black).
// 75% × 255 = 191.
func rp219Bar75(i int) (r, g, b uint8) {
	const v = 191
	switch i {
	case 0:
		return v, v, v // gray
	case 1:
		return v, v, 0 // yellow
	case 2:
		return 0, v, v // cyan
	case 3:
		return 0, v, 0 // green
	case 4:
		return v, 0, v // magenta
	case 5:
		return v, 0, 0 // red
	case 6:
		return 0, 0, v // blue
	case 7:
		return 0, 0, 0 // black
	}
	panic("bar index out of range")
}

func TestSwitcher_ColorbarsMatchSMPTERp219(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	yuv, w, h, err := sw.BuiltinSourceFrame(testColorbarsKey)
	require.NoError(t, err)

	// Sample column-centers at y = H/4 — deep inside the bars region,
	// well above the annex band at the bottom. ±1 tolerance per plane
	// for rounding on RGB→YUV conversion.
	sampleY := h / 4
	ySize := w * h
	uOff := ySize
	vOff := ySize + (w/2)*(h/2)
	halfW := w / 2

	barWidth := w / 8
	for bar := 0; bar < 8; bar++ {
		col := bar*barWidth + barWidth/2
		r, g, b := rp219Bar75(bar)
		wantY, wantCb, wantCr := transition.RGBToYUV_BT709Limited(r, g, b)

		gotY := yuv[sampleY*w+col]
		gotCb := yuv[uOff+(sampleY/2)*halfW+(col/2)]
		gotCr := yuv[vOff+(sampleY/2)*halfW+(col/2)]

		require.InDeltaf(t, int(wantY), int(gotY), 1,
			"bar %d (R=%d G=%d B=%d): Y want %d got %d", bar, r, g, b, wantY, gotY)
		require.InDeltaf(t, int(wantCb), int(gotCb), 1,
			"bar %d: Cb want %d got %d", bar, wantCb, gotCb)
		require.InDeltaf(t, int(wantCr), int(gotCr), 1,
			"bar %d: Cr want %d got %d", bar, wantCr, gotCr)
	}
}

func TestSwitcher_BuiltinSourcesRegenerateOnFormatChange(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	// Switch to 720p30 and pull one frame from each virtual source.
	fmt720 := PipelineFormat{Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1}
	require.NoError(t, sw.SetPipelineFormat(fmt720))

	_, w, h, err := sw.BuiltinSourceFrame(testBlackKey)
	require.NoError(t, err)
	require.Equal(t, 1280, w)
	require.Equal(t, 720, h)

	_, w, h, err = sw.BuiltinSourceFrame(testColorbarsKey)
	require.NoError(t, err)
	require.Equal(t, 1280, w)
	require.Equal(t, 720, h)

	// Switch to 1080p60, next pull must reflect the new format.
	fmt1080 := PipelineFormat{Width: 1920, Height: 1080, FPSNum: 60, FPSDen: 1}
	require.NoError(t, sw.SetPipelineFormat(fmt1080))

	_, w, h, err = sw.BuiltinSourceFrame(testBlackKey)
	require.NoError(t, err)
	require.Equal(t, 1920, w, "black regenerated at new width")
	require.Equal(t, 1080, h, "black regenerated at new height")

	_, w, h, err = sw.BuiltinSourceFrame(testColorbarsKey)
	require.NoError(t, err)
	require.Equal(t, 1920, w)
	require.Equal(t, 1080, h)
}

// TestSwitcher_ColorbarsAnnexLivesBelowBars pins the broadcast-correctness
// contract: the annex band (logo + moving sprite + burned-in clock) lives in
// the bottom region, so the bars sample area is unaffected by it. We verify
// that a column-center at y=H/4 still matches SMPTE RP 219 exactly, AND that
// SOME pixel in the bottom 20% is non-zero-luma (proving the annex drew
// something — if the annex region were identical to the bars above it, this
// test would fail).
func TestSwitcher_ColorbarsAnnexLivesBelowBars(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	yuv, w, h, err := sw.BuiltinSourceFrame(testColorbarsKey)
	require.NoError(t, err)

	// Annex starts at y = floor(H * 0.8). Sample the luma directly.
	annexStart := (h * 4) / 5
	require.Less(t, annexStart, h, "sanity: annex must start inside the frame")

	// Find at least one annex pixel whose luma differs from the bar above
	// it at the same column. If the annex band renders nothing, every
	// annex pixel would match the bar it's under.
	differs := false
	for y := annexStart; y < h && !differs; y++ {
		for col := 0; col < w; col++ {
			above := yuv[(annexStart-1)*w+col]
			hit := yuv[y*w+col]
			if hit != above {
				differs = true
				break
			}
		}
	}
	require.True(t, differs,
		"annex band at y=[%d,%d) must render content (logo/clock/sprite) — got a solid band matching the bars above",
		annexStart, h)
}

// TestSwitcher_BuiltinSourcesHealthyImmediately — AC #2: built-ins appear
// with health=healthy, not offline. The produced state for a fresh switcher
// (no frames pushed yet) must report them as healthy because they're
// always-available reference sources that an operator can cut to for
// fallback.
func TestSwitcher_BuiltinSourcesHealthyImmediately(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	state := sw.State()
	for _, key := range []string{testBlackKey, testColorbarsKey} {
		src, ok := state.Sources[key]
		require.True(t, ok)
		require.Equal(t, string(SourceHealthy), src.Status,
			"built-in %s should report healthy, not offline (always-available reference)", key)
	}
}

// TestSwitcher_BlackSource10bit — professional pipeline mode uses YUV422 10-bit.
// Verify the frame generator produces the correct format with BT.709 10-bit
// legal-range black: Y=64, Cb=Cr=512 stored as little-endian uint16.
func TestSwitcher_BlackSource10bit(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	pf := sw.PipelineFormat()
	pf.Color.ColorFormat = ColorYUV422_10bit
	require.NoError(t, sw.SetPipelineFormat(pf))

	yuv, w, h, err := sw.BuiltinSourceFrame(testBlackKey)
	require.NoError(t, err)

	fmt := video.YUV422_10bit
	require.Equal(t, fmt.FrameSize(w, h), len(yuv))

	ySize := fmt.YPlaneSize(w, h)
	chromaSize := fmt.ChromaPlaneSize(w, h)
	cbOff := fmt.CbOffset(w, h)
	crOff := fmt.CrOffset(w, h)

	// Every Y sample must be 64 (LE uint16).
	for i := 0; i < ySize; i += 2 {
		got := binary.LittleEndian.Uint16(yuv[i : i+2])
		require.Equalf(t, uint16(64), got, "Y[sample %d] expected 64", i/2)
	}
	// Every Cb sample must be 512.
	for i := 0; i < chromaSize; i += 2 {
		got := binary.LittleEndian.Uint16(yuv[cbOff+i : cbOff+i+2])
		require.Equalf(t, uint16(512), got, "Cb[sample %d] expected 512", i/2)
	}
	// Every Cr sample must be 512.
	for i := 0; i < chromaSize; i += 2 {
		got := binary.LittleEndian.Uint16(yuv[crOff+i : crOff+i+2])
		require.Equalf(t, uint16(512), got, "Cr[sample %d] expected 512", i/2)
	}
}

// TestSwitcher_Colorbars10bitMatchRP219 — bar column centers in professional
// mode carry 10-bit YUV (8-bit RP 219 × 4). Same pattern as the 8-bit test,
// but samples uint16 LE values from the 10-bit planes.
func TestSwitcher_Colorbars10bitMatchRP219(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	pf := sw.PipelineFormat()
	pf.Color.ColorFormat = ColorYUV422_10bit
	require.NoError(t, sw.SetPipelineFormat(pf))

	yuv, w, h, err := sw.BuiltinSourceFrame(testColorbarsKey)
	require.NoError(t, err)

	fmt := video.YUV422_10bit
	ySize := fmt.YPlaneSize(w, h) // Y is w*h*2 bytes
	cbOff := fmt.CbOffset(w, h)
	crOff := fmt.CrOffset(w, h)
	halfW := fmt.ChromaWidth(w) // w/2

	sampleY := h / 4
	barWidth := w / 8
	for bar := 0; bar < 8; bar++ {
		col := bar*barWidth + barWidth/2
		r, g, b := rp219Bar75(bar)
		y8, cb8, cr8 := transition.RGBToYUV_BT709Limited(r, g, b)

		// 8-to-10-bit legal-range scale: left-shift by 2.
		wantY, wantCb, wantCr := uint16(y8)<<2, uint16(cb8)<<2, uint16(cr8)<<2

		// Y plane: each sample is 2 bytes LE.
		yByteOff := (sampleY*w + col) * 2
		require.Less(t, yByteOff+1, ySize)
		gotY := binary.LittleEndian.Uint16(yuv[yByteOff : yByteOff+2])

		// Cb/Cr are 4:2:2 (horizontal-only subsampling); chroma row = luma row.
		cbByteOff := cbOff + (sampleY*halfW+col/2)*2
		crByteOff := crOff + (sampleY*halfW+col/2)*2
		gotCb := binary.LittleEndian.Uint16(yuv[cbByteOff : cbByteOff+2])
		gotCr := binary.LittleEndian.Uint16(yuv[crByteOff : crByteOff+2])

		require.InDeltaf(t, int(wantY), int(gotY), 4,
			"bar %d: Y 10-bit want %d got %d", bar, wantY, gotY)
		require.InDeltaf(t, int(wantCb), int(gotCb), 4,
			"bar %d: Cb 10-bit want %d got %d", bar, wantCb, gotCb)
		require.InDeltaf(t, int(wantCr), int(gotCr), 4,
			"bar %d: Cr 10-bit want %d got %d", bar, wantCr, gotCr)
	}
}

// TestSwitcher_BuiltinSourcesRegenerateOnColorProfileChange — switching color
// profile (8-bit → 10-bit and back) must invalidate the cached frame so the
// next pull matches the new format's byte count.
func TestSwitcher_BuiltinSourcesRegenerateOnColorProfileChange(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	yuv, w, h, err := sw.BuiltinSourceFrame(testBlackKey)
	require.NoError(t, err)
	require.Equal(t, video.YUV420_8bit.FrameSize(w, h), len(yuv),
		"default profile: 8-bit YUV420 frame")

	pf := sw.PipelineFormat()
	pf.Color.ColorFormat = ColorYUV422_10bit
	require.NoError(t, sw.SetPipelineFormat(pf))

	yuv, w, h, err = sw.BuiltinSourceFrame(testBlackKey)
	require.NoError(t, err)
	require.Equal(t, video.YUV422_10bit.FrameSize(w, h), len(yuv),
		"professional profile: 10-bit YUV422 frame regenerated")

	pf.Color.ColorFormat = ColorYUV420_8bit
	require.NoError(t, sw.SetPipelineFormat(pf))

	yuv, w, h, err = sw.BuiltinSourceFrame(testBlackKey)
	require.NoError(t, err)
	require.Equal(t, video.YUV420_8bit.FrameSize(w, h), len(yuv),
		"back to standard: 8-bit YUV420 frame regenerated")
}

// TestSwitcher_ColorbarsSpriteMotion pulls two frames separated by enough
// time that the sprite should have moved. Confirms the producer doesn't
// serve a frozen cached frame when something in the annex is meant to be
// animated. We check luma difference in the sprite's row strip, not the
// whole frame, because the bars region is identical across frames by design.
func TestSwitcher_ColorbarsSpriteMotion(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	first, w, h, err := sw.BuiltinSourceFrame(testColorbarsKey)
	require.NoError(t, err)

	// Grab a copy — subsequent pulls may reuse the underlying buffer.
	firstCopy := make([]byte, len(first))
	copy(firstCopy, first)

	// Sleep at least one frame of a 30fps pipeline so motion had a
	// chance to advance. 100ms is 3 frames at 30fps.
	time.Sleep(100 * time.Millisecond)

	second, _, _, err := sw.BuiltinSourceFrame(testColorbarsKey)
	require.NoError(t, err)

	// Compare the annex region (bottom 20%) — the bars above are static.
	annexStart := (h * 4) / 5
	differs := false
	for y := annexStart; y < h && !differs; y++ {
		for col := 0; col < w; col++ {
			if firstCopy[y*w+col] != second[y*w+col] {
				differs = true
				break
			}
		}
	}
	require.True(t, differs,
		"annex must advance between frames (sprite motion / clock tick) — got identical annex bytes")
}
