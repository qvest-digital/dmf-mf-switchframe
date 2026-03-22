package replay

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFileRing_NewAndClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ring.buf")
	maxBytes := int64(1024 * 1024) // 1MB

	ring, err := newFileRing(path, maxBytes)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}

	// File should exist with the correct size.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != maxBytes {
		t.Errorf("file size = %d, want %d", info.Size(), maxBytes)
	}

	// Close should succeed.
	if err := ring.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// File should still exist after Close (Close does NOT remove).
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file should still exist after Close: %v", err)
	}
}

func TestFileRing_CloseAndRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ring.buf")

	ring, err := newFileRing(path, 4096)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}

	if err := ring.CloseAndRemove(); err != nil {
		t.Errorf("CloseAndRemove: %v", err)
	}

	// File should be gone.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should not exist after CloseAndRemove, got err=%v", err)
	}
}

func TestFileRing_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 4096)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	data := []byte("hello replay world")
	now := time.Now()
	pts := int64(90000)

	if _, err := ring.Write(data, pts, now, true); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read it back using the frame entry metadata.
	frames := ring.Frames()
	if len(frames) != 1 {
		t.Fatalf("Frames() len = %d, want 1", len(frames))
	}

	got, err := ring.Read(frames[0].offset, frames[0].size)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Read data = %q, want %q", got, data)
	}
}

func TestFileRing_MultipleFrames(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 1024*1024)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	now := time.Now()
	frameData := make([][]byte, 10)
	for i := range frameData {
		frameData[i] = make([]byte, 100)
		rand.Read(frameData[i])
		isIDR := i == 0 || i == 5
		if _, err := ring.Write(frameData[i], int64(i*3000), now.Add(time.Duration(i)*33*time.Millisecond), isIDR); err != nil {
			t.Fatalf("Write frame %d: %v", i, err)
		}
	}

	frames := ring.Frames()
	if len(frames) != 10 {
		t.Fatalf("Frames() len = %d, want 10", len(frames))
	}

	// Read each frame back and verify.
	for i, f := range frames {
		got, err := ring.Read(f.offset, f.size)
		if err != nil {
			t.Fatalf("Read frame %d: %v", i, err)
		}
		if !bytes.Equal(got, frameData[i]) {
			t.Errorf("frame %d data mismatch", i)
		}
	}
}

func TestFileRing_Wrap(t *testing.T) {
	dir := t.TempDir()
	maxBytes := int64(500)
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), maxBytes)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	now := time.Now()

	// Write 4 frames of 150 bytes each = 600 bytes total.
	// After frame 3 (offset 450), frame 4 (150 bytes) won't fit
	// (450+150=600 > 500), so it wraps to offset 0, invalidating frame 0.
	frameData := make([][]byte, 4)
	for i := range frameData {
		frameData[i] = make([]byte, 150)
		rand.Read(frameData[i])
		if _, err := ring.Write(frameData[i], int64(i*3000), now.Add(time.Duration(i)*33*time.Millisecond), i == 0); err != nil {
			t.Fatalf("Write frame %d: %v", i, err)
		}
	}

	// Frame 0 should have been invalidated by the wrap.
	frames := ring.Frames()

	// The wrap at frame 3 should overwrite frame 0 (offset 0, size 150).
	// Frame 3 writes at offset 0, invalidating anything in [0, 150).
	// That includes frame 0 (offset 0, size 150).
	// Remaining: frames 1, 2, 3.
	if len(frames) != 3 {
		t.Fatalf("after wrap: Frames() len = %d, want 3", len(frames))
	}

	// Frames 1 and 2 should still be readable.
	for _, idx := range []int{0, 1} {
		got, err := ring.Read(frames[idx].offset, frames[idx].size)
		if err != nil {
			t.Fatalf("Read post-wrap frame %d: %v", idx, err)
		}
		if !bytes.Equal(got, frameData[idx+1]) {
			t.Errorf("post-wrap frame %d data mismatch", idx)
		}
	}

	// Frame 3 (the wrapped one) should also be readable.
	got, err := ring.Read(frames[2].offset, frames[2].size)
	if err != nil {
		t.Fatalf("Read wrapped frame: %v", err)
	}
	if !bytes.Equal(got, frameData[3]) {
		t.Errorf("wrapped frame data mismatch")
	}
}

func TestFileRing_WrapInvalidatesMultiple(t *testing.T) {
	dir := t.TempDir()
	maxBytes := int64(300)
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), maxBytes)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	now := time.Now()

	// Write 3 frames of 100 bytes each (fills the buffer exactly).
	for i := 0; i < 3; i++ {
		data := bytes.Repeat([]byte{byte(i)}, 100)
		if _, err := ring.Write(data, int64(i*3000), now.Add(time.Duration(i)*33*time.Millisecond), false); err != nil {
			t.Fatalf("Write frame %d: %v", i, err)
		}
	}
	if ring.FrameCount() != 3 {
		t.Fatalf("expected 3 frames, got %d", ring.FrameCount())
	}

	// Write a 200-byte frame — wraps to 0, invalidating frames 0 and 1.
	bigData := bytes.Repeat([]byte{0xFF}, 200)
	if _, err := ring.Write(bigData, 9000, now.Add(100*time.Millisecond), true); err != nil {
		t.Fatalf("Write big frame: %v", err)
	}

	// Only frame 2 (offset 200, size 100) and the big frame (offset 0, size 200) remain.
	if ring.FrameCount() != 2 {
		t.Fatalf("expected 2 frames after big write, got %d", ring.FrameCount())
	}

	// Verify the big frame is readable.
	frames := ring.Frames()
	got, err := ring.Read(frames[1].offset, frames[1].size)
	if err != nil {
		t.Fatalf("Read big frame: %v", err)
	}
	if !bytes.Equal(got, bigData) {
		t.Errorf("big frame data mismatch")
	}
}

func TestFileRing_FrameCount(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 1024)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	if ring.FrameCount() != 0 {
		t.Errorf("initial FrameCount = %d, want 0", ring.FrameCount())
	}

	now := time.Now()
	for i := 0; i < 5; i++ {
		ring.Write([]byte("frame"), int64(i*3000), now.Add(time.Duration(i)*time.Millisecond), false)
	}
	if ring.FrameCount() != 5 {
		t.Errorf("after 5 writes: FrameCount = %d, want 5", ring.FrameCount())
	}

	// Force a wrap that invalidates some frames.
	// Buffer is 1024 bytes. 5 frames of 5 bytes = 25 bytes used.
	// Write a 1020-byte frame — wraps to 0, invalidating all 5 small frames.
	bigData := make([]byte, 1020)
	ring.Write(bigData, 99000, now.Add(100*time.Millisecond), true)

	// The 1020-byte frame at offset 25 doesn't fit (25+1020=1045 > 1024),
	// so it wraps to 0 and invalidates frames in [0, 1020) = all 5 small frames.
	if ring.FrameCount() != 1 {
		t.Errorf("after wrap: FrameCount = %d, want 1", ring.FrameCount())
	}
}

func TestFileRing_BytesUsed(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 1024*1024)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	if ring.BytesUsed() != 0 {
		t.Errorf("initial BytesUsed = %d, want 0", ring.BytesUsed())
	}

	now := time.Now()
	ring.Write(make([]byte, 100), 0, now, false)
	ring.Write(make([]byte, 200), 3000, now.Add(time.Millisecond), false)
	ring.Write(make([]byte, 300), 6000, now.Add(2*time.Millisecond), true)

	if ring.BytesUsed() != 600 {
		t.Errorf("BytesUsed = %d, want 600", ring.BytesUsed())
	}
}

func TestFileRing_BytesUsedAfterWrap(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 500)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	now := time.Now()
	// Write 4 x 150 = 600 bytes into 500 byte ring.
	for i := 0; i < 4; i++ {
		ring.Write(make([]byte, 150), int64(i*3000), now.Add(time.Duration(i)*time.Millisecond), false)
	}

	// Frame 0 (150 bytes) should be invalidated.
	// Remaining: frames 1, 2, 3 = 450 bytes.
	if ring.BytesUsed() != 450 {
		t.Errorf("BytesUsed after wrap = %d, want 450", ring.BytesUsed())
	}
}

func TestFileRing_FrameTooLarge(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 100)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	data := make([]byte, 101)
	_, err = ring.Write(data, 0, time.Now(), false)
	if err == nil {
		t.Fatal("expected error for frame too large, got nil")
	}
}

func TestFileRing_ConcurrentReadWrite(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 1024*1024)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		now := time.Now()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			data := make([]byte, 100)
			rand.Read(data)
			ring.Write(data, int64(i*3000), now.Add(time.Duration(i)*time.Millisecond), i%30 == 0)
		}
	}()

	// Reader goroutine — exercises Frames() and Read().
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			frames := ring.Frames()
			if len(frames) > 0 {
				f := frames[len(frames)-1]
				ring.Read(f.offset, f.size)
			}
			_ = ring.FrameCount()
			_ = ring.BytesUsed()
		}
	}()

	// Let them run for a short time.
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	// If we got here without a race detector error, concurrent access is safe.
	if ring.FrameCount() == 0 {
		t.Error("expected frames to have been written")
	}
}

func TestFileRing_IDRTracking(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 1024*1024)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	now := time.Now()
	// Write a mix of IDR and non-IDR frames.
	idrFlags := []bool{true, false, false, false, true, false, false}
	for i, isIDR := range idrFlags {
		data := []byte(fmt.Sprintf("frame-%d", i))
		ring.Write(data, int64(i*3000), now.Add(time.Duration(i)*33*time.Millisecond), isIDR)
	}

	frames := ring.Frames()
	if len(frames) != len(idrFlags) {
		t.Fatalf("Frames() len = %d, want %d", len(frames), len(idrFlags))
	}

	for i, f := range frames {
		if f.isIDR != idrFlags[i] {
			t.Errorf("frame %d: isIDR = %v, want %v", i, f.isIDR, idrFlags[i])
		}
	}
}

func TestFileRing_WallTimeOrdering(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 1024*1024)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	base := time.Now()
	// Write frames with increasing wall times.
	for i := 0; i < 20; i++ {
		wt := base.Add(time.Duration(i) * 33 * time.Millisecond)
		ring.Write([]byte("x"), int64(i*3000), wt, i%10 == 0)
	}

	frames := ring.Frames()
	if len(frames) != 20 {
		t.Fatalf("Frames() len = %d, want 20", len(frames))
	}

	// Verify wall times are in non-decreasing order.
	for i := 1; i < len(frames); i++ {
		if frames[i].wallTime.Before(frames[i-1].wallTime) {
			t.Errorf("frame %d wallTime (%v) before frame %d wallTime (%v)",
				i, frames[i].wallTime, i-1, frames[i-1].wallTime)
		}
	}
}

func TestFileRing_PTSPreserved(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 1024*1024)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	now := time.Now()
	ptsList := []int64{0, 3000, 6000, 9000, 12000}
	for i, pts := range ptsList {
		ring.Write([]byte("data"), pts, now.Add(time.Duration(i)*33*time.Millisecond), false)
	}

	frames := ring.Frames()
	for i, f := range frames {
		if f.pts != ptsList[i] {
			t.Errorf("frame %d: pts = %d, want %d", i, f.pts, ptsList[i])
		}
	}
}

func TestFileRing_EmptyRead(t *testing.T) {
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 4096)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	frames := ring.Frames()
	if len(frames) != 0 {
		t.Errorf("Frames() on empty ring = %d, want 0", len(frames))
	}
	if ring.FrameCount() != 0 {
		t.Errorf("FrameCount() on empty ring = %d, want 0", ring.FrameCount())
	}
	if ring.BytesUsed() != 0 {
		t.Errorf("BytesUsed() on empty ring = %d, want 0", ring.BytesUsed())
	}
}

func TestFileRing_SequentialFill(t *testing.T) {
	// Verify correct sequential offsets before any wrapping occurs.
	dir := t.TempDir()
	ring, err := newFileRing(filepath.Join(dir, "ring.buf"), 1024*1024)
	if err != nil {
		t.Fatalf("newFileRing: %v", err)
	}
	defer ring.CloseAndRemove()

	now := time.Now()
	sizes := []int{50, 75, 100, 25, 200}
	expectedOffset := int64(0)
	for i, sz := range sizes {
		data := make([]byte, sz)
		ring.Write(data, int64(i*3000), now.Add(time.Duration(i)*time.Millisecond), false)
	}

	frames := ring.Frames()
	if len(frames) != len(sizes) {
		t.Fatalf("Frames() len = %d, want %d", len(frames), len(sizes))
	}

	expectedOffset = 0
	for i, f := range frames {
		if f.offset != expectedOffset {
			t.Errorf("frame %d: offset = %d, want %d", i, f.offset, expectedOffset)
		}
		if int(f.size) != sizes[i] {
			t.Errorf("frame %d: size = %d, want %d", i, f.size, sizes[i])
		}
		expectedOffset += int64(sizes[i])
	}
}

func TestFileRing_InvalidPath(t *testing.T) {
	_, err := newFileRing("/nonexistent/dir/ring.buf", 4096)
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}
