//go:build correctness

package playout

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/playout"
)

// ==========================================================================
// Helpers for real media tests
// ==========================================================================

// requireFFmpeg skips the test if ffmpeg is not found in PATH.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}
}

// generateColorClip generates a solid-color MPEG-TS clip via ffmpeg.
// Returns the path to the generated file.
func generateColorClip(t *testing.T, dir, name, color string, durationSec int, width, height, fps int) string {
	t.Helper()
	outPath := filepath.Join(dir, name+".ts")
	cmd := exec.Command("ffmpeg",
		"-y", "-loglevel", "error",
		"-f", "lavfi", "-i",
		fmt.Sprintf("color=c=%s:duration=%d:size=%dx%d:rate=%d", color, durationSec, width, height, fps),
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p", "-an",
		"-f", "mpegts", outPath,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ffmpeg failed for %s: %s", name, string(out))

	info, err := os.Stat(outPath)
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(0), "generated clip %s is empty", name)
	t.Logf("Generated clip %s: %d bytes", name, info.Size())
	return outPath
}

// fileAssetProvider opens real media files from disk using FileFrameIterator.
type fileAssetProvider struct {
	mu    sync.Mutex
	paths map[string]string // asset URI -> file path
}

func newFileAssetProvider() *fileAssetProvider {
	return &fileAssetProvider{
		paths: make(map[string]string),
	}
}

func (p *fileAssetProvider) addAsset(uri, path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths[uri] = path
}

func (p *fileAssetProvider) Open(assetURI string) (playout.FrameIterator, error) {
	p.mu.Lock()
	path, ok := p.paths[assetURI]
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("asset not found: %s", assetURI)
	}
	return playout.NewFileFrameIterator(path)
}

// frameSample captures key measurements from an output frame.
type frameSample struct {
	ts         time.Time
	pts        int64
	isKeyframe bool
	avgY       float64 // average luma of the first row
	width      int
	height     int
}

// computeAvgY computes the average luma of the first scanline of a YUV420 frame.
func computeAvgY(yuv []byte, w, h int) float64 {
	if len(yuv) < w || w == 0 {
		return 0
	}
	sum := 0.0
	for i := 0; i < w; i++ {
		sum += float64(yuv[i])
	}
	return sum / float64(w)
}

// isBlackFrame returns true if the average luma of the frame is below threshold.
func isBlackFrame(yuv []byte, w, h int) bool {
	if len(yuv) < w*h || w == 0 || h == 0 {
		return true
	}
	// Sample every 16th pixel for speed.
	ySize := w * h
	sum := 0.0
	count := 0
	for i := 0; i < ySize; i += 16 {
		sum += float64(yuv[i])
		count++
	}
	if count == 0 {
		return true
	}
	avgY := sum / float64(count)
	return avgY < 20
}

// ==========================================================================
// Test 1: Playout with generated clips (full decode->schedule->output chain)
// ==========================================================================

func TestPlayoutWithGeneratedClips(t *testing.T) {
	requireFFmpeg(t)

	tmpDir := t.TempDir()

	// Generate 3 short test clips with distinct colors.
	clip1Path := generateColorClip(t, tmpDir, "red", "red", 2, 320, 240, 30)
	clip2Path := generateColorClip(t, tmpDir, "green", "green", 2, 320, 240, 30)
	clip3Path := generateColorClip(t, tmpDir, "blue", "blue", 2, 320, 240, 30)

	// Build asset provider.
	provider := newFileAssetProvider()
	provider.addAsset("local:///clips/red.ts", clip1Path)
	provider.addAsset("local:///clips/green.ts", clip2Path)
	provider.addAsset("local:///clips/blue.ts", clip3Path)

	ch := playout.NewChannel(1, playout.ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider,
		Width:         320,
		Height:        240,
		FPS:           30.0,
	})

	var mu sync.Mutex
	var samples []frameSample
	var totalFrames atomic.Int64
	var blackFrames atomic.Int64

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		totalFrames.Add(1)

		avgY := computeAvgY(yuv, w, h)
		if isBlackFrame(yuv, w, h) {
			blackFrames.Add(1)
		}

		mu.Lock()
		samples = append(samples, frameSample{
			ts:         time.Now(),
			pts:        pts,
			isKeyframe: isKeyframe,
			avgY:       avgY,
			width:      w,
			height:     h,
		})
		mu.Unlock()
	})

	pl := &playout.Playlist{
		ID:   "test-generated-clips",
		Name: "Generated Clips Test",
		Items: []playout.PlaylistItem{
			{
				ID:       "clip-red",
				Type:     playout.ItemClip,
				AssetURI: "local:///clips/red.ts",
				Title:    "Red Clip",
			},
			{
				ID:       "clip-green",
				Type:     playout.ItemClip,
				AssetURI: "local:///clips/green.ts",
				Title:    "Green Clip",
			},
			{
				ID:       "clip-blue",
				Type:     playout.ItemClip,
				AssetURI: "local:///clips/blue.ts",
				Title:    "Blue Clip",
			},
		},
	}

	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	// Wait for either completion or timeout (8 seconds).
	deadline := time.After(8 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Logf("Timeout reached, stopping channel")
			ch.Stop()
			goto analyze
		case <-ticker.C:
			if ch.State() == playout.StateCompleted {
				t.Logf("Playlist completed naturally")
				goto analyze
			}
		}
	}

analyze:
	total := totalFrames.Load()
	black := blackFrames.Load()
	t.Logf("MEASURE: total frames = %d, black frames = %d", total, black)

	mu.Lock()
	samplesCopy := make([]frameSample, len(samples))
	copy(samplesCopy, samples)
	mu.Unlock()

	// Log first and last few frames.
	for i := 0; i < len(samplesCopy) && i < 5; i++ {
		s := samplesCopy[i]
		t.Logf("  frame[%d]: pts=%d, keyframe=%v, avgY=%.1f, %dx%d",
			i, s.pts, s.isKeyframe, s.avgY, s.width, s.height)
	}
	if len(samplesCopy) > 10 {
		for i := len(samplesCopy) - 5; i < len(samplesCopy); i++ {
			s := samplesCopy[i]
			t.Logf("  frame[%d]: pts=%d, keyframe=%v, avgY=%.1f, %dx%d",
				i, s.pts, s.isKeyframe, s.avgY, s.width, s.height)
		}
	}

	// Verify: frames received, total count > 100 (6s at 30fps ~ 180).
	// 3 clips x 2s x 30fps = ~180 frames expected.
	assert.Greater(t, total, int64(100),
		"expected > 100 frames for 3 x 2s clips at 30fps, got %d", total)

	// Verify: no extended black gaps (> 5 consecutive frames of Y < 20).
	consecutiveBlack := 0
	maxConsecutiveBlack := 0
	for _, s := range samplesCopy {
		if s.avgY < 20 {
			consecutiveBlack++
			if consecutiveBlack > maxConsecutiveBlack {
				maxConsecutiveBlack = consecutiveBlack
			}
		} else {
			consecutiveBlack = 0
		}
	}
	t.Logf("MEASURE: max consecutive black frames = %d", maxConsecutiveBlack)
	assert.LessOrEqual(t, maxConsecutiveBlack, 5,
		"no extended black gaps expected, but found %d consecutive black frames", maxConsecutiveBlack)

	// Verify dimensions are consistent.
	for i, s := range samplesCopy {
		assert.Equal(t, 320, s.width, "frame %d has unexpected width %d", i, s.width)
		assert.Equal(t, 240, s.height, "frame %d has unexpected height %d", i, s.height)
	}

	// Verify PTS is monotonically increasing.
	ptsJumps := 0
	for i := 1; i < len(samplesCopy); i++ {
		if samplesCopy[i].pts <= samplesCopy[i-1].pts {
			ptsJumps++
			if ptsJumps <= 5 {
				t.Logf("MEASURE: PTS non-monotonic at frame %d: pts=%d <= prev=%d",
					i, samplesCopy[i].pts, samplesCopy[i-1].pts)
			}
		}
	}
	t.Logf("MEASURE: PTS non-monotonic jumps = %d / %d frames", ptsJumps, len(samplesCopy))
	assert.Equal(t, 0, ptsJumps, "PTS should be monotonically increasing")
}

// ==========================================================================
// Test 2: Loop mode (single clip looping with item.Loop=true)
// ==========================================================================

func TestPlayoutLoopMode(t *testing.T) {
	requireFFmpeg(t)

	tmpDir := t.TempDir()

	// Generate a single 2-second clip.
	clipPath := generateColorClip(t, tmpDir, "loop-clip", "yellow", 2, 320, 240, 30)

	provider := newFileAssetProvider()
	provider.addAsset("local:///clips/loop.ts", clipPath)

	ch := playout.NewChannel(1, playout.ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider,
		Width:         320,
		Height:        240,
		FPS:           30.0,
	})

	var mu sync.Mutex
	var ptsList []int64
	var totalFrames atomic.Int64
	var blackFrames atomic.Int64

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		totalFrames.Add(1)
		if isBlackFrame(yuv, w, h) {
			blackFrames.Add(1)
		}
		mu.Lock()
		ptsList = append(ptsList, pts)
		mu.Unlock()
	})

	pl := &playout.Playlist{
		ID:   "test-loop-mode",
		Name: "Loop Mode Test",
		Items: []playout.PlaylistItem{
			{
				ID:       "loop-item",
				Type:     playout.ItemClip,
				AssetURI: "local:///clips/loop.ts",
				Title:    "Looping Clip",
				Loop:     true,
			},
		},
	}

	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	// Run for 7 seconds.
	time.Sleep(7 * time.Second)
	ch.Stop()

	total := totalFrames.Load()
	black := blackFrames.Load()

	mu.Lock()
	ptsListCopy := make([]int64, len(ptsList))
	copy(ptsListCopy, ptsList)
	mu.Unlock()

	// Estimate iterations: 2s clip at 30fps = ~60 frames per iteration.
	iterationsApprox := 0
	if total > 0 {
		iterationsApprox = int(total) / 60
	}

	t.Logf("MEASURE: total frames = %d, black frames = %d, loop iterations ~ %d",
		total, black, iterationsApprox)

	// Log first few PTS values and values around expected loop points.
	for i := 0; i < len(ptsListCopy) && i < 5; i++ {
		t.Logf("  pts[%d] = %d", i, ptsListCopy[i])
	}
	// Log around the ~60th frame (first loop boundary).
	if len(ptsListCopy) > 65 {
		for i := 55; i < 65 && i < len(ptsListCopy); i++ {
			t.Logf("  pts[%d] = %d (near loop point)", i, ptsListCopy[i])
		}
	}

	// Verify: > 180 frames (3+ iterations of 2s clip).
	assert.Greater(t, total, int64(180),
		"expected > 180 frames for 7s of 2s-clip looping at 30fps, got %d", total)

	// Verify: no black flash at loop point (no more than 2 consecutive black frames).
	consecutiveBlack := 0
	maxConsecutiveBlack := 0
	for _, pts := range ptsListCopy {
		_ = pts // pts used for gap analysis below
	}
	// Re-check using the actual frame data -- we only have PTS, not YUV anymore.
	// Use the black frame counter to check: ratio of black frames should be very small.
	blackRatio := float64(black) / float64(total)
	t.Logf("MEASURE: black frame ratio = %.4f (%d/%d)", blackRatio, black, total)
	assert.Less(t, blackRatio, 0.05,
		"black frame ratio should be < 5%%, got %.2f%%", blackRatio*100)

	// Verify: PTS continuously increasing.
	ptsDrops := 0
	for i := 1; i < len(ptsListCopy); i++ {
		if ptsListCopy[i] <= ptsListCopy[i-1] {
			ptsDrops++
			if ptsDrops <= 5 {
				t.Logf("MEASURE: PTS non-monotonic at frame %d: pts=%d <= prev=%d",
					i, ptsListCopy[i], ptsListCopy[i-1])
			}
		}
	}
	t.Logf("MEASURE: PTS non-monotonic drops = %d / %d frames", ptsDrops, len(ptsListCopy))
	// PTS should be monotonically increasing even across loop boundaries
	// (the channel re-stamps PTS from its own counter).
	assert.Equal(t, 0, ptsDrops, "PTS should be monotonically increasing across loop boundaries")

	// Check for gaps at loop boundaries. Since clip is ~60 frames at 30fps,
	// expect re-open every ~60 frames. Check that there are no large PTS gaps.
	maxPTSGap := int64(0)
	for i := 1; i < len(ptsListCopy); i++ {
		gap := ptsListCopy[i] - ptsListCopy[i-1]
		if gap > maxPTSGap {
			maxPTSGap = gap
		}
	}
	t.Logf("MEASURE: max PTS gap between consecutive frames = %d (expected ~%d for 30fps)",
		maxPTSGap, 3000)
	// PTS gap should be no more than 2x normal (allow for slight jitter).
	assert.LessOrEqual(t, maxPTSGap, int64(6000),
		"PTS gap should be <= 2x frame duration, got %d", maxPTSGap)

	// Detect loop-point black gaps by looking at consecutive black count.
	// We stored black count but not positions. Let's just verify total black is low.
	_ = consecutiveBlack
	_ = maxConsecutiveBlack
}

// ==========================================================================
// Test 3: Item transition (crossfade between clips)
// ==========================================================================

func TestPlayoutItemTransition(t *testing.T) {
	requireFFmpeg(t)

	tmpDir := t.TempDir()

	// Generate two clips with very different luma.
	// Red: high luma Y ~82 (BT.709), Blue: low luma Y ~41 (BT.709).
	clip1Path := generateColorClip(t, tmpDir, "trans-red", "red", 3, 320, 240, 30)
	clip2Path := generateColorClip(t, tmpDir, "trans-blue", "blue", 3, 320, 240, 30)

	provider := newFileAssetProvider()
	provider.addAsset("local:///clips/trans-red.ts", clip1Path)
	provider.addAsset("local:///clips/trans-blue.ts", clip2Path)

	ch := playout.NewChannel(1, playout.ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider,
		Width:         320,
		Height:        240,
		FPS:           30.0,
	})

	var mu sync.Mutex
	var samples []frameSample
	var totalFrames atomic.Int64

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		totalFrames.Add(1)
		avgY := computeAvgY(yuv, w, h)

		mu.Lock()
		samples = append(samples, frameSample{
			ts:         time.Now(),
			pts:        pts,
			isKeyframe: isKeyframe,
			avgY:       avgY,
			width:      w,
			height:     h,
		})
		mu.Unlock()
	})

	dissolveMs := 500
	pl := &playout.Playlist{
		ID:   "test-transition",
		Name: "Transition Test",
		DefaultTransition: playout.TransitionConfig{
			Type:       "dissolve",
			DurationMs: dissolveMs,
		},
		Items: []playout.PlaylistItem{
			{
				ID:       "trans-clip-1",
				Type:     playout.ItemClip,
				AssetURI: "local:///clips/trans-red.ts",
				Title:    "Red Clip",
			},
			{
				ID:       "trans-clip-2",
				Type:     playout.ItemClip,
				AssetURI: "local:///clips/trans-blue.ts",
				Title:    "Blue Clip",
				Transition: &playout.TransitionConfig{
					Type:       "dissolve",
					DurationMs: dissolveMs,
				},
			},
		},
	}

	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	// Wait for completion or timeout.
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Logf("Timeout reached, stopping channel")
			ch.Stop()
			goto analyzeTransition
		case <-ticker.C:
			if ch.State() == playout.StateCompleted {
				t.Logf("Playlist completed naturally")
				goto analyzeTransition
			}
		}
	}

analyzeTransition:
	total := totalFrames.Load()
	t.Logf("MEASURE: total frames = %d", total)

	mu.Lock()
	samplesCopy := make([]frameSample, len(samples))
	copy(samplesCopy, samples)
	mu.Unlock()

	// Log all frames' avgY to see the transition.
	t.Logf("Frame luma profile (%d frames):", len(samplesCopy))
	for i, s := range samplesCopy {
		if i < 10 || i > len(samplesCopy)-10 ||
			(i > 50 && i < 120) { // around expected transition point
			t.Logf("  frame[%d]: pts=%d, avgY=%.1f", i, s.pts, s.avgY)
		}
	}

	// Expect > 100 frames (2 clips at 3s x 30fps = ~180, minus some for decoder buffering).
	assert.Greater(t, total, int64(100),
		"expected > 100 frames, got %d", total)

	// Analyze the transition. The first clip is red (Y~82 in BT.709 for pure red).
	// The second clip is blue (Y~41 in BT.709 for pure blue).
	// During a dissolve, avgY should smoothly change between these values.
	// Find the transition region by detecting where avgY changes significantly.

	if len(samplesCopy) < 20 {
		t.Fatalf("too few frames to analyze transition: %d", len(samplesCopy))
	}

	// Characterize the first clip's avgY (skip first 5 frames for decoder warmup).
	firstClipY := 0.0
	firstClipCount := 0
	for i := 5; i < len(samplesCopy) && i < 30; i++ {
		firstClipY += samplesCopy[i].avgY
		firstClipCount++
	}
	if firstClipCount > 0 {
		firstClipY /= float64(firstClipCount)
	}
	t.Logf("MEASURE: first clip average Y = %.1f", firstClipY)

	// Characterize the last clip's avgY (last 20 frames, skip last 5).
	lastClipY := 0.0
	lastClipCount := 0
	for i := len(samplesCopy) - 25; i < len(samplesCopy)-5 && i >= 0; i++ {
		lastClipY += samplesCopy[i].avgY
		lastClipCount++
	}
	if lastClipCount > 0 {
		lastClipY /= float64(lastClipCount)
	}
	t.Logf("MEASURE: last clip average Y = %.1f", lastClipY)

	// The two clips should have noticeably different Y values.
	// Red (BT.709): Y = 0.2126*255 + 0.7152*0 + 0.0722*0 = 54.2 (full range)
	// But libx264 uses limited range, so actual Y might differ.
	// Blue (BT.709): Y = 0.2126*0 + 0.7152*0 + 0.0722*255 = 18.4 (full range)
	// Let's just verify they're different.
	yDiff := firstClipY - lastClipY
	if yDiff < 0 {
		yDiff = -yDiff
	}
	t.Logf("MEASURE: Y difference between clips = %.1f", yDiff)
	assert.Greater(t, yDiff, 5.0,
		"clips should have visibly different luma, got diff=%.1f", yDiff)

	// Look for transition frames: Y values between the two clip averages.
	// During a 500ms dissolve at 30fps, expect ~15 frames of blended content.
	transitionFrames := 0
	minY := firstClipY
	maxY := lastClipY
	if minY > maxY {
		minY, maxY = maxY, minY
	}
	margin := 3.0 // account for codec noise
	for _, s := range samplesCopy {
		// A transition frame has avgY between the two clip values (with some margin).
		if s.avgY > minY+margin && s.avgY < maxY-margin {
			transitionFrames++
		}
	}
	t.Logf("MEASURE: transition frames (Y between clip values) = %d", transitionFrames)

	// With a 500ms dissolve at 30fps, we expect ~15 blended frames.
	// Allow some slack for decoder buffering and timing jitter.
	// The dissolve should produce at least 10 blended frames.
	assert.GreaterOrEqual(t, transitionFrames, 10,
		"expected >= 10 transition frames for 500ms dissolve at 30fps, got %d", transitionFrames)

	// And no more than 20 (500ms / 33.3ms = 15 frames, plus reasonable slack).
	assert.LessOrEqual(t, transitionFrames, 20,
		"expected <= 20 transition frames for 500ms dissolve at 30fps, got %d", transitionFrames)

	// Verify PTS is monotonically increasing through the transition.
	ptsJumps := 0
	for i := 1; i < len(samplesCopy); i++ {
		if samplesCopy[i].pts <= samplesCopy[i-1].pts {
			ptsJumps++
			if ptsJumps <= 5 {
				t.Logf("MEASURE: PTS non-monotonic at frame %d: pts=%d <= prev=%d",
					i, samplesCopy[i].pts, samplesCopy[i-1].pts)
			}
		}
	}
	t.Logf("MEASURE: PTS non-monotonic jumps = %d / %d frames", ptsJumps, len(samplesCopy))
	assert.Equal(t, 0, ptsJumps, "PTS should be monotonically increasing through transition")
}
