package switcher

import (
	"encoding/binary"
	"runtime"
	"strings"
	"sync"
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
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	state := sw.State()
	src, ok := state.Sources[testBlackKey]
	require.True(t, ok, "source:black must be auto-registered by Switcher.New")
	require.True(t, src.IsVirtual, "source:black must have IsVirtual=true")
}

func TestSwitcher_BuiltinColorbarsRegistered(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	state := sw.State()
	src, ok := state.Sources[testColorbarsKey]
	require.True(t, ok, "source:colorbars must be auto-registered by Switcher.New")
	require.True(t, src.IsVirtual, "source:colorbars must have IsVirtual=true")
}

func TestSwitcher_BlackSourceYUVValues(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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

// TestSwitcher_BuiltinSourcesAlwaysProduce pins the multiview-tile
// contract: the producer goroutine pushes frames for both built-ins
// unconditionally, regardless of whether either is on program or preview.
// Rationale: Switchframe's multiview subscribes to per-source preview
// MoQ tracks so operators can see thumbnails before routing. If the
// producer only fires when selected, the BARS/BLACK tiles show as
// empty — operators can't confirm what they're about to cut to.
//
// We probe the underlying health monitor's raw last-frame timestamp
// (buildStateLocked overrides the computed status to healthy regardless,
// so that path can't distinguish "ever produced" from "never produced").
func TestSwitcher_BuiltinSourcesAlwaysProduce(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	require.Empty(t, sw.State().ProgramSource, "must not be selected")
	require.Empty(t, sw.State().PreviewSource, "must not be selected")

	require.Eventually(t, func() bool {
		// lastFrameAgoMs returns -1 if no frame has ever been recorded.
		return sw.health.lastFrameAgoMs(BuiltinSourceKeyBlack) >= 0 &&
			sw.health.lastFrameAgoMs(BuiltinSourceKeyColorbars) >= 0
	}, 2*time.Second, 25*time.Millisecond,
		"built-ins must push frames without being on PGM or PVW (multiview needs thumbnails)")
}

// TestSwitcher_BuiltinFrameTapReceivesFrames pins the multiview-publish
// contract: the app layer installs a per-source tap (via
// SetBuiltinFrameTap) to feed a MoQ-published preview encoder for each
// built-in. The producer must invoke the tap for every frame it generates,
// for both built-ins, regardless of selection.
func TestSwitcher_BuiltinFrameTapReceivesFrames(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	var mu sync.Mutex
	counts := make(map[string]int)
	sw.SetBuiltinFrameTap(func(key string, yuv []byte, w, h int, pts int64, fmt video.Format) {
		mu.Lock()
		counts[key]++
		mu.Unlock()
		// Sanity: frame dimensions and byte count match what we asked for.
		require.Equal(t, fmt.FrameSize(w, h), len(yuv),
			"tap %q frame byte count matches declared dimensions + format", key)
	})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return counts[BuiltinSourceKeyBlack] > 0 && counts[BuiltinSourceKeyColorbars] > 0
	}, 2*time.Second, 25*time.Millisecond,
		"tap must receive at least one frame for each built-in")
}

// TestSwitcher_BuiltinSourcesHealthyImmediately — AC #2: built-ins appear
// with health=healthy, not offline. The produced state for a fresh switcher
// (no frames pushed yet) must report them as healthy because they're
// always-available reference sources that an operator can cut to for
// fallback.
func TestSwitcher_BuiltinSourcesHealthyImmediately(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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

// TestSwitcher_SpriteMotionIsMonotonic pins the "sprite never goes
// backward" contract. Pulls one full cycle's worth of frames directly
// off the generator and asserts that the leftmost yellow pixel column
// (the sprite) strictly advances frame-over-frame — except when the
// sprite is entirely off-screen during the wrap, which is by design.
//
// Rationale: user reported the sprite oscillating forward/back on the
// program path, which implied the motion math had a jitter source.
// Switching to frame-count-based animation (instead of wall-clock) makes
// the sprite position a pure function of the call index and eliminates
// any pipeline-scheduling-induced oscillation. This test enforces that
// property.
func TestSwitcher_SpriteMotionIsMonotonic(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	// Stop the background producer so its concurrent Frame() calls don't
	// race our test's pulls on the shared frameIndex counter; this test
	// asserts a per-call invariant and needs exclusive control over the
	// sequence of calls.
	sw.stopBuiltinProducer()
	// Reset the sprite counters so wherever the producer happened to be
	// stopped doesn't bias the sequence we measure.
	for _, bs := range sw.builtinSources {
		bs.mu.Lock()
		bs.frameIndex = 0
		bs.mu.Unlock()
	}

	// Use a fixed, large format so the sprite travels far enough between
	// frames to produce a distinct leftmost x per step.
	fmt := PipelineFormat{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1}
	require.NoError(t, sw.SetPipelineFormat(fmt))

	prevX := -1
	// spriteCyclePeriodFrames frames of motion + a few extra across the wrap.
	for i := 0; i < 70; i++ {
		yuv, w, h, err := sw.BuiltinSourceFrame(testColorbarsKey)
		require.NoError(t, err)

		// Detect the sprite by its distinctive 75% yellow chroma (low Cb ≈ 44
		// vs near-128 for the white logo text and black annex). Sample
		// chroma at the center of the annex band to avoid picking up the
		// bars region above.
		cbOff := w * h
		halfW := w / 2
		trackY := (h * 4 / 5) + (h-(h*4/5))/2
		if trackY >= h {
			trackY = h - 1
		}
		cbRow := (trackY / 2) * halfW
		leftmost := -1
		for uvCol := 0; uvCol < halfW; uvCol++ {
			if yuv[cbOff+cbRow+uvCol] < 90 { // yellow Cb is ~44; white/gray Cb is ~128
				leftmost = uvCol * 2
				break
			}
		}

		if leftmost >= 0 && prevX >= 0 {
			require.GreaterOrEqualf(t, leftmost, prevX,
				"sprite regressed at frame %d: prev=%d current=%d (jitter)",
				i, prevX, leftmost)
		}
		if leftmost >= 0 {
			prevX = leftmost
		} else {
			// Off-screen frame — expected during wrap; next visible frame
			// resets monotonic tracking.
			prevX = -1
		}
	}
}

// TestSwitcher_ColorbarsSpriteMotion pulls two frames separated by enough
// time that the sprite should have moved. Confirms the producer doesn't
// serve a frozen cached frame when something in the annex is meant to be
// animated. We check luma difference in the sprite's row strip, not the
// whole frame, because the bars region is identical across frames by design.
func TestSwitcher_ColorbarsSpriteMotion(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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

// TestSwitcher_BuiltinProducer_ExitsQuicklyOnClose gates the producer
// teardown latency. Close() must cause builtinProducerLoop to return
// promptly — if it doesn't, ~300 leaked producers across a test run
// saturate CPU and hang the race suite. The 250ms bound gives CI headroom
// over the observed ~85ms on a warm laptop; the point is "bounded and
// short," not "exactly 100."
func TestSwitcher_BuiltinProducer_ExitsQuicklyOnClose(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)

	// Let the producer run long enough to exercise the "stop while producing
	// a frame" path, not just "stop before first tick".
	time.Sleep(75 * time.Millisecond)

	start := time.Now()
	sw.Close()
	elapsed := time.Since(start)

	if elapsed > 250*time.Millisecond {
		t.Fatalf("Switcher.Close returned in %v; want ≤250ms", elapsed)
	}

	// Crisp fast-fail: producer's done channel must be closed after Close
	// returns. If stopBuiltinProducer was bypassed, this observes the
	// regression directly instead of waiting on the slower stack walk below.
	select {
	case <-sw.builtinProducerDone:
	default:
		t.Fatal("builtinProducerDone still open after Close returned")
	}

	// Belt-and-suspenders: stack walk catches any future refactor that
	// splits the producer into multiple goroutines or keeps the loop
	// running under a different name.
	time.Sleep(10 * time.Millisecond) // scheduler slack
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)
	if strings.Contains(string(buf[:n]), "switcher.(*Switcher).builtinProducerLoop") {
		t.Fatalf("builtinProducerLoop goroutine still running after Close:\n%s", buf[:n])
	}
}

// TestSwitcher_Close_IsIdempotent guards the sync.Once gating on Close().
// Without it, a t.Cleanup(sw.Close) alongside an existing defer sw.Close()
// would panic on the second close(s.videoProcCh).
func TestSwitcher_Close_IsIdempotent(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)

	sw.Close()
	// Second call must not panic and must return promptly.
	done := make(chan struct{})
	go func() {
		defer close(done)
		sw.Close()
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second Close() blocked longer than 500ms — sync.Once gating is broken")
	}
}
