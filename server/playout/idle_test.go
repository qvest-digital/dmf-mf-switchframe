package playout

import (
	"fmt"
	"sync"
	"testing"
)

// makeTestFrames creates n test frames with sequential PTS and 4x4 YUV data.
func makeTestFrames(n int) []DecodedFrame {
	frames := make([]DecodedFrame, n)
	for i := range frames {
		w, h := 4, 4
		ySize := w * h
		uvSize := ySize / 4
		yuv := make([]byte, ySize+2*uvSize)
		// Fill Y with frame index so we can distinguish frames.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte(i + 1)
		}
		frames[i] = DecodedFrame{
			YUV:    yuv,
			PTS:    int64(i) * 3000,
			Width:  w,
			Height: h,
		}
	}
	return frames
}

func TestIdleLoop_BlackFrameWhenNoAsset(t *testing.T) {
	loop := NewIdleLoop("", nil, 1920, 1080)
	defer loop.Close()

	frame := loop.NextFrame()
	if frame == nil {
		t.Fatal("NextFrame returned nil")
	}

	// Check Y plane is all 16 (BT.709 black).
	ySize := 1920 * 1080
	for i := 0; i < ySize; i++ {
		if frame.YUV[i] != 16 {
			t.Fatalf("Y[%d] = %d, want 16", i, frame.YUV[i])
		}
	}

	// Check UV planes are all 128.
	for i := ySize; i < len(frame.YUV); i++ {
		if frame.YUV[i] != 128 {
			t.Fatalf("UV[%d] = %d, want 128", i, frame.YUV[i])
		}
	}
}

func TestIdleLoop_BlackFrameCorrectSize(t *testing.T) {
	tests := []struct {
		w, h int
	}{
		{1920, 1080},
		{1280, 720},
		{640, 480},
		{320, 240},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%dx%d", tc.w, tc.h), func(t *testing.T) {
			loop := NewIdleLoop("", nil, tc.w, tc.h)
			defer loop.Close()

			frame := loop.NextFrame()
			expected := yuvFrameSize(tc.w, tc.h)
			if len(frame.YUV) != expected {
				t.Fatalf("YUV len = %d, want %d", len(frame.YUV), expected)
			}
			if frame.Width != tc.w || frame.Height != tc.h {
				t.Fatalf("frame dimensions = %dx%d, want %dx%d", frame.Width, frame.Height, tc.w, tc.h)
			}
		})
	}
}

func TestIdleLoop_PlaysAssetFrames(t *testing.T) {
	frames := makeTestFrames(5)
	provider := NewFakeAssetProvider(frames)
	loop := NewIdleLoop("test://idle.ts", provider, 4, 4)
	defer loop.Close()

	for i := 0; i < 5; i++ {
		f := loop.NextFrame()
		if f == nil {
			t.Fatalf("frame %d: NextFrame returned nil", i)
		}
		// Y plane should be filled with i+1 (our test pattern).
		if f.YUV[0] != byte(i+1) {
			t.Fatalf("frame %d: Y[0] = %d, want %d", i, f.YUV[0], i+1)
		}
	}
}

func TestIdleLoop_AutoRewindsOnEOF(t *testing.T) {
	frames := makeTestFrames(3)
	provider := NewFakeAssetProvider(frames)
	loop := NewIdleLoop("test://idle.ts", provider, 4, 4)
	defer loop.Close()

	// Play through all 3 frames.
	for i := 0; i < 3; i++ {
		f := loop.NextFrame()
		if f.YUV[0] != byte(i+1) {
			t.Fatalf("first pass frame %d: Y[0] = %d, want %d", i, f.YUV[0], i+1)
		}
	}

	// The next frame should be the first frame again (auto-rewind).
	f := loop.NextFrame()
	if f.YUV[0] != 1 {
		t.Fatalf("after rewind: Y[0] = %d, want 1", f.YUV[0])
	}

	// Continue playing the rest of the second pass.
	f = loop.NextFrame()
	if f.YUV[0] != 2 {
		t.Fatalf("second pass frame 1: Y[0] = %d, want 2", f.YUV[0])
	}
}

func TestIdleLoop_FallsBackToBlackOnOpenError(t *testing.T) {
	provider := &errorAssetProvider{err: fmt.Errorf("disk failure")}
	loop := NewIdleLoop("test://broken.ts", provider, 1920, 1080)
	defer loop.Close()

	frame := loop.NextFrame()
	if frame == nil {
		t.Fatal("NextFrame returned nil")
	}

	// Should be a black frame.
	ySize := 1920 * 1080
	if frame.YUV[0] != 16 {
		t.Fatalf("fallback Y[0] = %d, want 16 (black)", frame.YUV[0])
	}
	if frame.YUV[ySize] != 128 {
		t.Fatalf("fallback UV[0] = %d, want 128 (neutral)", frame.YUV[ySize])
	}
}

func TestIdleLoop_NeverReturnsNil(t *testing.T) {
	// Test with nil provider.
	loop := NewIdleLoop("test://missing.ts", nil, 320, 240)
	defer loop.Close()

	for i := 0; i < 100; i++ {
		f := loop.NextFrame()
		if f == nil {
			t.Fatalf("frame %d: NextFrame returned nil", i)
		}
	}
}

func TestIdleLoop_SetAsset_ChangesPlayback(t *testing.T) {
	// Start with frames where Y=1,2,3.
	framesA := makeTestFrames(3)
	provider := &multiAssetProvider{
		assets: map[string][]DecodedFrame{
			"asset-a": framesA,
		},
	}
	loop := NewIdleLoop("asset-a", provider, 4, 4)
	defer loop.Close()

	// Read first frame from asset-a.
	f := loop.NextFrame()
	if f.YUV[0] != 1 {
		t.Fatalf("asset-a frame 0: Y[0] = %d, want 1", f.YUV[0])
	}

	// Add asset-b with distinct Y values (101, 102, 103).
	framesB := make([]DecodedFrame, 3)
	for i := range framesB {
		w, h := 4, 4
		ySize := w * h
		uvSize := ySize / 4
		yuv := make([]byte, ySize+2*uvSize)
		for j := 0; j < ySize; j++ {
			yuv[j] = byte(101 + i)
		}
		framesB[i] = DecodedFrame{
			YUV:    yuv,
			PTS:    int64(i) * 3000,
			Width:  w,
			Height: h,
		}
	}
	provider.mu.Lock()
	provider.assets["asset-b"] = framesB
	provider.mu.Unlock()

	// Switch to asset-b.
	loop.SetAsset("asset-b")

	// Next frame should be from asset-b.
	f = loop.NextFrame()
	if f.YUV[0] != 101 {
		t.Fatalf("asset-b frame 0: Y[0] = %d, want 101", f.YUV[0])
	}
}

func TestIdleLoop_PTSIncrements(t *testing.T) {
	loop := NewIdleLoop("", nil, 320, 240)
	defer loop.Close()

	var lastPTS int64 = -1
	for i := 0; i < 10; i++ {
		f := loop.NextFrame()
		if lastPTS >= 0 && f.PTS <= lastPTS {
			t.Fatalf("frame %d: PTS %d not greater than previous %d", i, f.PTS, lastPTS)
		}
		lastPTS = f.PTS
	}
}

func TestIdleLoop_Close_Cleanup(t *testing.T) {
	frames := makeTestFrames(5)
	provider := NewFakeAssetProvider(frames)
	loop := NewIdleLoop("test://idle.ts", provider, 4, 4)

	// Read a couple frames to open the iterator.
	loop.NextFrame()
	loop.NextFrame()

	// Close should not panic.
	loop.Close()

	// Double close should not panic.
	loop.Close()
}

func TestIdleLoop_ConcurrentAccess(t *testing.T) {
	frames := makeTestFrames(10)
	provider := NewFakeAssetProvider(frames)
	loop := NewIdleLoop("test://idle.ts", provider, 4, 4)
	defer loop.Close()

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				f := loop.NextFrame()
				if f == nil {
					t.Error("NextFrame returned nil in concurrent goroutine")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// errorAssetProvider always returns an error on Open.
type errorAssetProvider struct {
	err error
}

func (p *errorAssetProvider) Open(assetURI string) (FrameIterator, error) {
	return nil, p.err
}

// multiAssetProvider returns different frames based on asset URI.
type multiAssetProvider struct {
	mu     sync.Mutex
	assets map[string][]DecodedFrame
}

func (p *multiAssetProvider) Open(assetURI string) (FrameIterator, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	frames, ok := p.assets[assetURI]
	if !ok {
		return nil, fmt.Errorf("asset not found: %s", assetURI)
	}
	// Copy frames to avoid aliasing.
	copied := make([]DecodedFrame, len(frames))
	copy(copied, frames)
	return NewSliceIterator(copied), nil
}
