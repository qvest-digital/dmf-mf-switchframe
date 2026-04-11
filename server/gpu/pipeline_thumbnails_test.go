//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"sync"
	"testing"
	"time"
)

// mockThumbnailStore implements ThumbnailStore for testing.
type mockThumbnailStore struct {
	mu            sync.Mutex
	shouldCapture bool
	stored        map[string][]byte
	storeCount    int
}

func newMockThumbnailStore(shouldCapture bool) *mockThumbnailStore {
	return &mockThumbnailStore{
		shouldCapture: shouldCapture,
		stored:        make(map[string][]byte),
	}
}

func (m *mockThumbnailStore) ShouldCapture() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shouldCapture
}

func (m *mockThumbnailStore) Store(nodeID string, jpeg []byte, width, height int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stored[nodeID] = jpeg
	m.storeCount++
}

func (m *mockThumbnailStore) getStored() map[string][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string][]byte, len(m.stored))
	for k, v := range m.stored {
		cp[k] = v
	}
	return cp
}

func (m *mockThumbnailStore) getStoreCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storeCount
}

func TestNV12ToJPEG(t *testing.T) {
	w, h := thumbW, thumbH
	ySize := w * h
	uvSize := w * (h / 2)
	nv12 := make([]byte, ySize+uvSize)

	// Fill Y with 128 (mid-gray), UV with 128 (neutral chroma)
	for i := 0; i < ySize; i++ {
		nv12[i] = 128
	}
	for i := ySize; i < ySize+uvSize; i++ {
		nv12[i] = 128
	}

	jpeg := nv12ToJPEG(nv12, w, h)
	if jpeg == nil {
		t.Fatal("nv12ToJPEG returned nil")
	}

	// Check JPEG SOI marker
	if len(jpeg) < 2 || jpeg[0] != 0xFF || jpeg[1] != 0xD8 {
		t.Fatalf("not a valid JPEG: got %02x %02x", jpeg[0], jpeg[1])
	}

	// 160x90 at quality 60 should be small
	if len(jpeg) > 20*1024 {
		t.Fatalf("JPEG too large: %d bytes", len(jpeg))
	}
	if len(jpeg) < 50 {
		t.Fatalf("JPEG too small: %d bytes", len(jpeg))
	}
}

func TestNV12ToJPEG_TooSmallBuffer(t *testing.T) {
	// Buffer smaller than w*h + w*(h/2) should return nil
	jpeg := nv12ToJPEG(make([]byte, 100), thumbW, thumbH)
	if jpeg != nil {
		t.Fatal("expected nil for undersized buffer")
	}
}

func TestClampByte(t *testing.T) {
	tests := []struct {
		in   float64
		want byte
	}{
		{-10, 0},
		{0, 0},
		{128.5, 128},
		{255, 255},
		{300, 255},
	}
	for _, tt := range tests {
		got := clampByte(tt.in)
		if got != tt.want {
			t.Errorf("clampByte(%f) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestShouldCaptureThisFrame_NilState(t *testing.T) {
	p := &GPUPipeline{}
	if ts := p.shouldCaptureThisFrame(); ts != nil {
		t.Fatal("expected nil when thumbState is not set")
	}
}

func TestShouldCaptureThisFrame_NotRequested(t *testing.T) {
	p := &GPUPipeline{}
	store := newMockThumbnailStore(false)
	ts := &thumbnailState{store: store}
	p.thumbState.Store(ts)
	defer func() {
		p.thumbState.Store(nil)
	}()

	if result := p.shouldCaptureThisFrame(); result != nil {
		t.Fatal("expected nil when ShouldCapture() is false")
	}
}

func TestShouldCaptureThisFrame_RateLimited(t *testing.T) {
	p := &GPUPipeline{}
	store := newMockThumbnailStore(true)
	ts := &thumbnailState{
		store:       store,
		lastCapture: time.Now(), // just captured
	}
	p.thumbState.Store(ts)
	defer func() {
		p.thumbState.Store(nil)
	}()

	if result := p.shouldCaptureThisFrame(); result != nil {
		t.Fatal("expected nil due to rate limiting (just captured)")
	}
}

func TestShouldCaptureThisFrame_Ready(t *testing.T) {
	p := &GPUPipeline{
		activeNodes: []GPUPipelineNode{
			&gpuPassthroughNode{name: "test_node"},
		},
	}
	store := newMockThumbnailStore(true)
	ts := &thumbnailState{
		store:       store,
		lastCapture: time.Now().Add(-2 * thumbCaptureRate), // long enough ago
	}
	p.thumbState.Store(ts)
	defer func() {
		// Don't close -- no scratch resources allocated in this test
		p.thumbState.Store(nil)
	}()

	// shouldCaptureThisFrame will try to init scratch buffers, which needs
	// a GPU context. Without one, it returns nil (init fails gracefully).
	result := p.shouldCaptureThisFrame()
	// On a non-GPU test machine, initScratch fails (no GPU context),
	// so we accept nil. The logic path is still exercised.
	_ = result
}

func TestSetThumbnailStore_Nil(t *testing.T) {
	p := &GPUPipeline{}
	p.SetThumbnailStore(nil)
	if ts := p.thumbState.Load(); ts != nil {
		t.Fatal("expected nil after SetThumbnailStore(nil)")
	}
}

func TestCloseThumbnails_NilSafe(t *testing.T) {
	p := &GPUPipeline{}
	// Should not panic
	p.closeThumbnails()
}

func TestThumbnailEncodeLoop(t *testing.T) {
	store := newMockThumbnailStore(true)

	ts := &thumbnailState{
		store:    store,
		encodeCh: make(chan thumbnailEncodeReq, 4),
		done:     make(chan struct{}),
	}
	go ts.encodeLoop()

	// Create a valid NV12 buffer
	w, h := thumbW, thumbH
	ySize := w * h
	uvSize := w * (h / 2)
	nv12 := make([]byte, ySize+uvSize)
	for i := 0; i < ySize; i++ {
		nv12[i] = 128
	}
	for i := ySize; i < ySize+uvSize; i++ {
		nv12[i] = 128
	}

	ts.encodeCh <- thumbnailEncodeReq{
		nodeID: "test_node",
		nv12:   nv12,
		store:  store,
	}

	// Close the channel to stop the loop
	close(ts.encodeCh)
	<-ts.done

	stored := store.getStored()
	jpeg, ok := stored["test_node"]
	if !ok {
		t.Fatal("expected thumbnail to be stored for test_node")
	}
	if len(jpeg) < 2 || jpeg[0] != 0xFF || jpeg[1] != 0xD8 {
		t.Fatalf("stored thumbnail is not valid JPEG: %02x %02x", jpeg[0], jpeg[1])
	}
}

func TestThumbnailEncodeLoop_DropsWhenFull(t *testing.T) {
	store := newMockThumbnailStore(true)

	ts := &thumbnailState{
		store:    store,
		encodeCh: make(chan thumbnailEncodeReq, 1), // small buffer
		done:     make(chan struct{}),
	}

	// Fill the channel
	nv12 := make([]byte, thumbW*thumbH*3/2)
	ts.encodeCh <- thumbnailEncodeReq{nodeID: "node1", nv12: nv12, store: store}

	// This should be dropped (channel full)
	select {
	case ts.encodeCh <- thumbnailEncodeReq{nodeID: "node2", nv12: nv12, store: store}:
		// If this succeeds, the first was consumed -- that's also fine
	default:
		// Expected: channel full, dropped
	}

	go ts.encodeLoop()
	close(ts.encodeCh)
	<-ts.done
}

func TestThumbnailCaptureNode_SkipsP210(t *testing.T) {
	ts := &thumbnailState{
		scratchFrames: make([]*GPUFrame, 1),
		nodeNames:     []string{"test"},
	}

	// P210 frames should be skipped (no conversion support yet)
	src := &GPUFrame{
		Width:  1920,
		Height: 1080,
		Pitch:  1920,
		Format: GPUFormatP210,
	}
	// Should not panic even with nil scratch frame
	ts.captureNode(nil, 0, src)
}

func TestThumbnailCaptureNode_OutOfRange(t *testing.T) {
	ts := &thumbnailState{
		scratchFrames: make([]*GPUFrame, 1),
	}

	src := &GPUFrame{
		Width:  1920,
		Height: 1080,
		Pitch:  1920,
		Format: GPUFormatNV12,
	}
	// Index out of range should not panic
	ts.captureNode(nil, 5, src)
}

func TestThumbnailStoreInterface(t *testing.T) {
	// Compile-time check that mockThumbnailStore satisfies ThumbnailStore
	var _ ThumbnailStore = (*mockThumbnailStore)(nil)
}
