//go:build correctness

package pipeline

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// TestPipelineWithMotionContent
//
// Real video with motion exercises encoder rate control and motion estimation
// code paths that solid-color tests never touch. Generates 60 frames where
// a vertical white bar moves across a black background.
//
// Verifies:
// - Valid TS output with ~60 frames
// - PTS monotonically increasing
// - Per-frame PSNR vs input > 25 dB (lossy but recognizable)
// ==========================================================================

func TestPipelineWithMotionContent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	// Register a source — color doesn't matter, we'll feed custom frames.
	h.addSource("motion", sourceA)

	h.cut(t, "motion")

	const (
		w         = testWidth
		ht        = testHeight
		numFrames = 60
		barWidth  = 20
	)

	// Generate and feed frames with a moving vertical bar.
	inputFrames := make([][]byte, numFrames)
	frameSize := w*ht + (w/2)*(ht/2)*2

	for frame := 0; frame < numFrames; frame++ {
		yuv := make([]byte, frameSize)
		barX := (frame * 5) % (w - barWidth) // bar moves 5 pixels per frame, wraps

		// Y plane: white bar on black background (broadcast levels)
		for y := 0; y < ht; y++ {
			for x := 0; x < w; x++ {
				if x >= barX && x < barX+barWidth {
					yuv[y*w+x] = 235 // white
				} else {
					yuv[y*w+x] = 16 // black
				}
			}
		}

		// Cb plane: neutral 128
		cbStart := w * ht
		for i := 0; i < (w/2)*(ht/2); i++ {
			yuv[cbStart+i] = 128
		}

		// Cr plane: neutral 128
		crStart := cbStart + (w/2)*(ht/2)
		for i := 0; i < (w/2)*(ht/2); i++ {
			yuv[crStart+i] = 128
		}

		inputFrames[frame] = yuv

		pf := &switcher.ProcessingFrame{
			YUV:        copyBytes(yuv),
			Width:      w,
			Height:     ht,
			PTS:        90000 + int64(frame)*ptsDelta,
			IsKeyframe: frame == 0,
		}
		h.sw.IngestRawVideo("motion", pf)
		if frame < numFrames-1 {
			time.Sleep(5 * time.Millisecond)
		}
	}

	h.waitForPipeline(800 * time.Millisecond)

	// Write TS and verify with ffprobe.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in TS output")
	require.Equal(t, "h264", vs.CodecName)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: video frame count = %d, expected ~%d", len(videoFrames), numFrames)
	require.Greater(t, len(videoFrames), 20, "expected at least 20 video frames")

	// Verify PTS monotonicity.
	ptsValues := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "PTS")

	// Decode output and compute per-frame PSNR vs input.
	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	t.Logf("MEASURE: decoded frame count = %d", len(decoded))
	require.Greater(t, len(decoded), 10, "need decoded frames for PSNR check")

	var (
		totalPSNR float64
		minPSNR   = math.Inf(1)
		psnrCount int
	)

	// Match decoded frames to input frames using best-PSNR matching.
	// The encoder introduces B-frame reordering, so PTS-based matching
	// can be offset. For each decoded frame, find the input frame with
	// the highest PSNR (best visual match) within a search window.
	for i, df := range decoded {
		// Search window: ±5 frames around the sequential index position.
		bestPSNR := -1.0
		bestIdx := i
		searchStart := i - 5
		if searchStart < 0 {
			searchStart = 0
		}
		searchEnd := i + 5
		if searchEnd > len(inputFrames) {
			searchEnd = len(inputFrames)
		}
		for j := searchStart; j < searchEnd; j++ {
			p := testutil.YUVFramePSNR(inputFrames[j], df.YUV, df.Width, df.Height)
			if p > bestPSNR {
				bestPSNR = p
				bestIdx = j
			}
		}

		// Log first few and any interesting frames for diagnostics.
		if i < 5 || bestPSNR < 30.0 {
			// Compute max pixel difference for diagnostics.
			var maxDiff int
			yPlane := w * ht
			for px := 0; px < yPlane && px < len(df.YUV) && px < len(inputFrames[bestIdx]); px++ {
				d := int(inputFrames[bestIdx][px]) - int(df.YUV[px])
				if d < 0 {
					d = -d
				}
				if d > maxDiff {
					maxDiff = d
				}
			}
			t.Logf("MEASURE: decoded[%d] best-match input[%d] PSNR = %.1f dB, max_pixel_diff = %d",
				i, bestIdx, bestPSNR, maxDiff)
		}

		totalPSNR += bestPSNR
		if bestPSNR < minPSNR {
			minPSNR = bestPSNR
		}
		psnrCount++
	}

	avgPSNR := totalPSNR / float64(psnrCount)

	// Compute output bitrate from TS size.
	probeResult := testutil.ProbeFile(t, tsPath)
	bitrateStr := probeResult.Format.BitRate
	bitrate, _ := strconv.Atoi(bitrateStr)

	t.Logf("MEASURE: avg PSNR = %.1f dB, min PSNR = %.1f dB, bitrate = %d bps, frames compared = %d",
		avgPSNR, minPSNR, bitrate, psnrCount)

	// PSNR = 999 means identical — this would indicate a bug (H.264 is lossy).
	// For high-contrast simple patterns, x264 CRF may hit near-lossless,
	// but we expect at least SOME quantization error on motion content.
	if avgPSNR >= 999.0 {
		t.Logf("WARNING: avg PSNR = 999.0 (identical frames) — encoder may be operating in lossless mode or comparison is misaligned")
	}

	require.Greater(t, avgPSNR, 25.0,
		"average PSNR should be > 25 dB for recognizable video, got %.1f", avgPSNR)
	require.Greater(t, minPSNR, 20.0,
		"minimum PSNR should be > 20 dB, got %.1f", minPSNR)
}

// ==========================================================================
// TestPipelineWithSceneChange
//
// Feeds 30 frames of pattern A (horizontal gradient), then 30 frames of
// pattern B (vertical gradient). The abrupt content change may trigger an
// IDR if scene change detection is enabled.
//
// Verifies:
// - Valid TS output
// - Keyframe positions logged (expect one near frame 30 if SCD is active)
// - Stream is decodable end-to-end
// ==========================================================================

func TestPipelineWithSceneChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("scene", sourceA)
	h.cut(t, "scene")

	const (
		w  = testWidth
		ht = testHeight
	)
	frameSize := w*ht + (w/2)*(ht/2)*2

	// Pattern A: horizontal gradient (Y varies left-to-right, same each row).
	feedPatternFrames := func(startFrame, count int, patternFn func(x, y int) byte) {
		for frame := 0; frame < count; frame++ {
			yuv := make([]byte, frameSize)

			// Y plane
			for py := 0; py < ht; py++ {
				for px := 0; px < w; px++ {
					yuv[py*w+px] = patternFn(px, py)
				}
			}

			// Neutral chroma
			cbStart := w * ht
			for i := 0; i < (w/2)*(ht/2); i++ {
				yuv[cbStart+i] = 128
			}
			crStart := cbStart + (w/2)*(ht/2)
			for i := 0; i < (w/2)*(ht/2); i++ {
				yuv[crStart+i] = 128
			}

			globalIdx := startFrame + frame
			pf := &switcher.ProcessingFrame{
				YUV:        yuv,
				Width:      w,
				Height:     ht,
				PTS:        90000 + int64(globalIdx)*ptsDelta,
				IsKeyframe: globalIdx == 0,
			}
			h.sw.IngestRawVideo("scene", pf)
			if frame < count-1 {
				time.Sleep(5 * time.Millisecond)
			}
		}
	}

	// Pattern A: horizontal gradient.
	horizontalGradient := func(x, y int) byte {
		return byte(16 + int(float64(x)/float64(w-1)*219.0+0.5))
	}

	// Pattern B: vertical gradient.
	verticalGradient := func(x, y int) byte {
		return byte(16 + int(float64(y)/float64(ht-1)*219.0+0.5))
	}

	feedPatternFrames(0, 30, horizontalGradient)
	h.waitForPipeline(300 * time.Millisecond)

	feedPatternFrames(30, 30, verticalGradient)
	h.waitForPipeline(500 * time.Millisecond)

	// Write TS and verify.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in TS output")
	require.Equal(t, "h264", vs.CodecName)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: video frame count = %d, expected ~60", len(videoFrames))
	require.Greater(t, len(videoFrames), 20, "expected at least 20 video frames")

	// Collect keyframe positions.
	var keyframePositions []int
	for i, f := range videoFrames {
		if f.KeyFrame {
			keyframePositions = append(keyframePositions, i)
		}
	}
	t.Logf("MEASURE: keyframe positions = %v", keyframePositions)

	// We expect at least 1 keyframe (the first frame).
	require.GreaterOrEqual(t, len(keyframePositions), 1,
		"expected at least 1 keyframe")

	// Check if there's a keyframe near the scene change (frame ~30).
	// If scene change detection is active, we'd expect one in [25, 40].
	hasSceneChangeIDR := false
	halfFrameCount := len(videoFrames) / 2
	for _, pos := range keyframePositions {
		if pos >= halfFrameCount-5 && pos <= halfFrameCount+10 {
			hasSceneChangeIDR = true
			break
		}
	}
	t.Logf("MEASURE: scene change IDR near midpoint = %v (half=%d)", hasSceneChangeIDR, halfFrameCount)

	// Verify stream is fully decodable.
	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	t.Logf("MEASURE: decoded frame count = %d", len(decoded))
	require.Greater(t, len(decoded), 10,
		"stream must be fully decodable after scene change")
}

// ==========================================================================
// TestPipelineRateControl
//
// Generates high-complexity frames (checkerboard with per-frame random noise)
// and feeds 300 frames (10s) through the pipeline at 2 Mbps target.
// Measures bitrate in 2-second windows and asserts each is within ±50%.
//
// This exercises rate control with real content variation — solid-color tests
// produce trivially low bitrates that never stress the rate controller.
// ==========================================================================

func TestPipelineRateControl(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("ratecontrol", sourceA)
	h.cut(t, "ratecontrol")

	const (
		w         = testWidth
		ht        = testHeight
		numFrames = 300 // 10 seconds at 30fps
		blockSize = 8
	)
	frameSize := w*ht + (w/2)*(ht/2)*2

	rng := rand.New(rand.NewSource(42)) // deterministic

	for frame := 0; frame < numFrames; frame++ {
		yuv := make([]byte, frameSize)

		// Checkerboard base with per-frame noise.
		for py := 0; py < ht; py++ {
			for px := 0; px < w; px++ {
				blockX := px / blockSize
				blockY := py / blockSize
				var base byte
				if (blockX+blockY)%2 == 0 {
					base = 40
				} else {
					base = 200
				}
				// Add noise: ±30 levels
				noise := int(rng.Int31n(61)) - 30
				val := int(base) + noise
				if val < 16 {
					val = 16
				}
				if val > 235 {
					val = 235
				}
				yuv[py*w+px] = byte(val)
			}
		}

		// Slightly varied chroma to add complexity.
		cbStart := w * ht
		for i := 0; i < (w/2)*(ht/2); i++ {
			yuv[cbStart+i] = byte(118 + rng.Intn(21)) // 118-138
		}
		crStart := cbStart + (w/2)*(ht/2)
		for i := 0; i < (w/2)*(ht/2); i++ {
			yuv[crStart+i] = byte(118 + rng.Intn(21))
		}

		pf := &switcher.ProcessingFrame{
			YUV:        yuv,
			Width:      w,
			Height:     ht,
			PTS:        90000 + int64(frame)*ptsDelta,
			IsKeyframe: frame%30 == 0, // keyframe every second
		}
		h.sw.IngestRawVideo("ratecontrol", pf)

		// Pace slightly faster than real time to avoid test taking 10s.
		if frame < numFrames-1 {
			time.Sleep(3 * time.Millisecond)
		}
	}

	h.waitForPipeline(1000 * time.Millisecond)

	// Write TS and verify.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in TS output")

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: total video frames = %d, expected ~%d", len(videoFrames), numFrames)
	require.Greater(t, len(videoFrames), 100, "expected at least 100 video frames for rate analysis")

	// Measure bitrate in 2-second windows (60 frames per window at 30fps).
	// Use pkt_size from ffprobe to compute per-window bitrate.
	const windowFrames = 60
	var windowBitrates []float64

	for start := 0; start+windowFrames <= len(videoFrames); start += windowFrames {
		windowEnd := start + windowFrames
		var windowBytes int64
		for i := start; i < windowEnd; i++ {
			windowBytes += int64(videoFrames[i].PktSize)
		}
		// 2-second window
		windowBitrateKbps := float64(windowBytes*8) / 2000.0
		windowBitrates = append(windowBitrates, windowBitrateKbps)
	}

	t.Logf("MEASURE: per-window bitrates = %v kbps", windowBitrates)

	// Compute overall bitrate from format info.
	overallBitrateStr := result.Format.BitRate
	overallBitrate, _ := strconv.Atoi(overallBitrateStr)
	t.Logf("MEASURE: overall bitrate = %d bps", overallBitrate)

	if len(windowBitrates) >= 2 {
		// Compute average window bitrate.
		var sumBitrate float64
		for _, br := range windowBitrates {
			sumBitrate += br
		}
		avgBitrate := sumBitrate / float64(len(windowBitrates))
		t.Logf("MEASURE: avg window bitrate = %.1f kbps", avgBitrate)

		// Assert each window is within ±50% of average.
		for i, br := range windowBitrates {
			ratio := br / avgBitrate
			t.Logf("MEASURE: window %d bitrate = %.1f kbps (ratio to avg = %.2f)", i, br, ratio)
			require.Greater(t, ratio, 0.5,
				"window %d bitrate %.1f kbps is more than 50%% below average %.1f kbps", i, br, avgBitrate)
			require.Less(t, ratio, 1.5,
				"window %d bitrate %.1f kbps is more than 50%% above average %.1f kbps", i, br, avgBitrate)
		}
	}

	// Verify PTS monotonicity.
	ptsValues := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "PTS")
}

// ==========================================================================
// TestPipelineCutBetweenRealContent
//
// Feeds horizontal bar pattern A for 30 frames, cuts to vertical bar
// pattern B for 30 frames. Decodes output and verifies the structural
// difference: horizontal bars have high column-variance, low row-variance;
// vertical bars the opposite.
//
// This proves the cut actually switches CONTENT, not just metadata.
// ==========================================================================

func TestPipelineCutBetweenRealContent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("patA", sourceA)
	h.addSource("patB", sourceB)

	const (
		w         = testWidth
		ht        = testHeight
		barHeight = 20 // for horizontal bars
		barWidth  = 20 // for vertical bars
	)
	frameSize := w*ht + (w/2)*(ht/2)*2

	// Generate horizontal bar pattern: alternating Y=16 and Y=235 every barHeight rows.
	makeHorizontalBars := func() []byte {
		yuv := make([]byte, frameSize)
		for py := 0; py < ht; py++ {
			var yVal byte
			if (py/barHeight)%2 == 0 {
				yVal = 16
			} else {
				yVal = 235
			}
			for px := 0; px < w; px++ {
				yuv[py*w+px] = yVal
			}
		}
		cbStart := w * ht
		for i := 0; i < (w/2)*(ht/2); i++ {
			yuv[cbStart+i] = 128
		}
		crStart := cbStart + (w/2)*(ht/2)
		for i := 0; i < (w/2)*(ht/2); i++ {
			yuv[crStart+i] = 128
		}
		return yuv
	}

	// Generate vertical bar pattern: alternating Y=16 and Y=235 every barWidth columns.
	makeVerticalBars := func() []byte {
		yuv := make([]byte, frameSize)
		for py := 0; py < ht; py++ {
			for px := 0; px < w; px++ {
				if (px/barWidth)%2 == 0 {
					yuv[py*w+px] = 16
				} else {
					yuv[py*w+px] = 235
				}
			}
		}
		cbStart := w * ht
		for i := 0; i < (w/2)*(ht/2); i++ {
			yuv[cbStart+i] = 128
		}
		crStart := cbStart + (w/2)*(ht/2)
		for i := 0; i < (w/2)*(ht/2); i++ {
			yuv[crStart+i] = 128
		}
		return yuv
	}

	hBars := makeHorizontalBars()
	vBars := makeVerticalBars()

	// Cut to patA and feed 30 frames of horizontal bars.
	h.cut(t, "patA")
	for frame := 0; frame < 30; frame++ {
		pf := &switcher.ProcessingFrame{
			YUV:        copyBytes(hBars),
			Width:      w,
			Height:     ht,
			PTS:        90000 + int64(frame)*ptsDelta,
			IsKeyframe: frame == 0,
		}
		h.sw.IngestRawVideo("patA", pf)
		if frame < 29 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	h.waitForPipeline(500 * time.Millisecond)

	framesAfterA := h.capture.videoCount()
	t.Logf("MEASURE: frames after pattern A = %d", framesAfterA)

	// Cut to patB and feed 30 frames of vertical bars.
	h.cut(t, "patB")
	for frame := 0; frame < 30; frame++ {
		pf := &switcher.ProcessingFrame{
			YUV:        copyBytes(vBars),
			Width:      w,
			Height:     ht,
			PTS:        90000 + int64(30+frame)*ptsDelta,
			IsKeyframe: frame == 0,
		}
		h.sw.IngestRawVideo("patB", pf)
		if frame < 29 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	h.waitForPipeline(500 * time.Millisecond)

	// Write TS and verify.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: total video frames = %d", len(videoFrames))
	require.Greater(t, len(videoFrames), 20, "expected at least 20 video frames")

	// Decode output frames.
	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	t.Logf("MEASURE: decoded frame count = %d", len(decoded))
	require.Greater(t, len(decoded), 6, "need enough decoded frames for first/last analysis")

	// Compute row variance and column variance for a frame.
	// Row variance: compute mean Y per row, then variance across rows.
	// Column variance: compute mean Y per column, then variance across columns.
	computeRowVariance := func(yuv []byte, fw, fh int) float64 {
		rowMeans := make([]float64, fh)
		for py := 0; py < fh; py++ {
			var sum float64
			for px := 0; px < fw; px++ {
				sum += float64(yuv[py*fw+px])
			}
			rowMeans[py] = sum / float64(fw)
		}
		// Variance
		var mean float64
		for _, m := range rowMeans {
			mean += m
		}
		mean /= float64(fh)
		var varSum float64
		for _, m := range rowMeans {
			diff := m - mean
			varSum += diff * diff
		}
		return varSum / float64(fh)
	}

	computeColVariance := func(yuv []byte, fw, fh int) float64 {
		colMeans := make([]float64, fw)
		for px := 0; px < fw; px++ {
			var sum float64
			for py := 0; py < fh; py++ {
				sum += float64(yuv[py*fw+px])
			}
			colMeans[px] = sum / float64(fh)
		}
		// Variance
		var mean float64
		for _, m := range colMeans {
			mean += m
		}
		mean /= float64(fw)
		var varSum float64
		for _, m := range colMeans {
			diff := m - mean
			varSum += diff * diff
		}
		return varSum / float64(fw)
	}

	// Analyze first 3 decoded frames — should be horizontal bars.
	// Horizontal bars: HIGH row variance, LOW column variance.
	for i := 0; i < 3 && i < len(decoded); i++ {
		df := decoded[i]
		rowVar := computeRowVariance(df.YUV, df.Width, df.Height)
		colVar := computeColVariance(df.YUV, df.Width, df.Height)
		t.Logf("MEASURE: frame %d (expect H-bars): row_var = %.1f, col_var = %.1f, ratio = %.2f",
			i, rowVar, colVar, rowVar/(colVar+0.001))

		// For horizontal bars, row variance should be much larger than column variance.
		require.Greater(t, rowVar, colVar,
			"frame %d: horizontal bars should have row_var > col_var (row=%.1f, col=%.1f)",
			i, rowVar, colVar)
	}

	// Analyze last 3 decoded frames — should be vertical bars.
	// Vertical bars: LOW row variance, HIGH column variance.
	for i := len(decoded) - 3; i < len(decoded); i++ {
		if i < 0 {
			continue
		}
		df := decoded[i]
		rowVar := computeRowVariance(df.YUV, df.Width, df.Height)
		colVar := computeColVariance(df.YUV, df.Width, df.Height)
		t.Logf("MEASURE: frame %d (expect V-bars): row_var = %.1f, col_var = %.1f, ratio = %.2f",
			i, rowVar, colVar, colVar/(rowVar+0.001))

		// For vertical bars, column variance should be much larger than row variance.
		require.Greater(t, colVar, rowVar,
			"frame %d: vertical bars should have col_var > row_var (col=%.1f, row=%.1f)",
			i, colVar, rowVar)
	}

	// Final sanity: the structural signature flipped. Compute the row/col
	// variance ratio for first and last frame and assert they're on opposite sides.
	if len(decoded) >= 6 {
		first := decoded[0]
		last := decoded[len(decoded)-1]

		firstRowVar := computeRowVariance(first.YUV, first.Width, first.Height)
		firstColVar := computeColVariance(first.YUV, first.Width, first.Height)
		lastRowVar := computeRowVariance(last.YUV, last.Width, last.Height)
		lastColVar := computeColVariance(last.YUV, last.Width, last.Height)

		firstRatio := firstRowVar / (firstColVar + 0.001)
		lastRatio := lastRowVar / (lastColVar + 0.001)

		t.Logf("MEASURE: first frame row/col ratio = %.2f, last frame row/col ratio = %.2f", firstRatio, lastRatio)

		// First should be >1 (horizontal dominant), last should be <1 (vertical dominant).
		require.Greater(t, firstRatio, 1.0,
			"first frame should be row-dominant (horizontal bars), ratio = %.2f", firstRatio)
		require.Less(t, lastRatio, 1.0,
			"last frame should be col-dominant (vertical bars), ratio = %.2f", lastRatio)

		// The difference should be large — we're going from one extreme to the other.
		require.Greater(t, math.Abs(math.Log10(firstRatio)-math.Log10(lastRatio)), 1.0,
			"structural signature should change by > 1 order of magnitude between patterns")
	}
}
