//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"bytes"
	"image"
	"image/jpeg"
	"log/slog"
	"time"
)

// ThumbnailStore is the callback interface for storing per-node JPEG thumbnails.
// Implemented by switcher.ThumbnailCache. The GPU pipeline writes thumbnails
// here without importing the switcher package.
type ThumbnailStore interface {
	ShouldCapture() bool
	Store(nodeID string, jpeg []byte, width, height int)
}

const (
	thumbW            = 160
	thumbH            = 90
	thumbCaptureRate  = time.Second // at most 1 capture pass per second
	thumbJPEGQuality  = 60
)

// thumbnailState holds per-pipeline thumbnail capture resources.
// Allocated lazily on first SetThumbnailStore call. All scratch buffers
// are pre-allocated and reused across capture passes.
type thumbnailState struct {
	store         ThumbnailStore
	lastCapture   time.Time
	scratchPool   *FramePool     // tiny 160x90 NV12 frames for GPU downsample
	scratchFrames []*GPUFrame    // one per active node, acquired from scratchPool
	cpuBufs       [][]byte       // CPU-side NV12 buffers for readback (reused)
	nodeNames     []string       // cached node names for thumbnail keys
	workQueue     *GPUWorkQueue  // separate queue for async downsample (nil = default)

	// Background JPEG encode goroutine
	encodeCh chan thumbnailEncodeReq
	done     chan struct{}
}

type thumbnailEncodeReq struct {
	nodeID string
	nv12   []byte // NV12 data (thumbW x thumbH)
	store  ThumbnailStore
}

// SetThumbnailStore attaches a thumbnail store to the pipeline.
// When set, Run() will periodically capture per-node thumbnails.
// Safe to call before Build(). Pass nil to disable.
func (p *GPUPipeline) SetThumbnailStore(store ThumbnailStore) {
	// Close any existing thumbnail state first.
	p.closeThumbnails()

	if store == nil {
		return
	}

	ts := &thumbnailState{
		store:    store,
		encodeCh: make(chan thumbnailEncodeReq, 16),
		done:     make(chan struct{}),
	}

	// Try to create a separate work queue for thumbnail dispatches.
	// Falls back to nil (uses default queue) if creation fails.
	if p.ctx != nil {
		q, err := NewWorkQueue(p.ctx)
		if err == nil {
			ts.workQueue = q
		}
	}

	go ts.encodeLoop()
	p.thumbState.Store(ts)
}

// initScratch allocates scratch buffers for the current node count.
// Called from Run() on the first capture pass after Build().
func (ts *thumbnailState) initScratch(ctx *Context, nodeCount int, nodeNames []string) error {
	if ts.scratchPool == nil {
		pool, err := NewFramePool(ctx, thumbW, thumbH, nodeCount)
		if err != nil {
			return err
		}
		ts.scratchPool = pool
	}

	// Allocate scratch frames and CPU buffers
	ts.scratchFrames = make([]*GPUFrame, nodeCount)
	ts.cpuBufs = make([][]byte, nodeCount)
	ts.nodeNames = make([]string, nodeCount)
	copy(ts.nodeNames, nodeNames)

	nv12Size := thumbW * thumbH * 3 / 2
	for i := 0; i < nodeCount; i++ {
		frame, err := ts.scratchPool.Acquire()
		if err != nil {
			return err
		}
		ts.scratchFrames[i] = frame
		ts.cpuBufs[i] = make([]byte, nv12Size)
	}

	return nil
}

// captureNode scales the current pipeline frame down to the scratch buffer
// for the given node index. This enqueues a GPU bilinear scale dispatch
// which runs asynchronously -- the caller continues immediately.
func (ts *thumbnailState) captureNode(ctx *Context, idx int, src *GPUFrame) {
	if idx >= len(ts.scratchFrames) || ts.scratchFrames[idx] == nil {
		return
	}
	dst := ts.scratchFrames[idx]
	// Only capture NV12 frames (P210 requires format conversion; skip for now)
	if src.Format != GPUFormatNV12 {
		return
	}
	if err := ScaleBilinearOn(ctx, dst, src, ts.workQueue); err != nil {
		slog.Debug("gpu: thumbnail scale failed", "node_idx", idx, "err", err)
	}
}

// readbackAndEncode downloads all scratch buffers to CPU and submits
// JPEG encode requests to the background goroutine. Must be called
// after all captureNode dispatches have been submitted.
func (ts *thumbnailState) readbackAndEncode(ctx *Context) {
	// Sync the work queue to ensure all scale dispatches have completed.
	if ts.workQueue != nil {
		if err := SyncWorkQueue(ts.workQueue); err != nil {
			slog.Debug("gpu: thumbnail sync failed", "err", err)
			return
		}
	} else {
		if err := ctx.Sync(); err != nil {
			slog.Debug("gpu: thumbnail sync failed", "err", err)
			return
		}
	}

	// Download NV12 from each scratch frame to CPU
	for i, frame := range ts.scratchFrames {
		if frame == nil || i >= len(ts.nodeNames) {
			continue
		}
		if err := Download(ctx, ts.cpuBufs[i], frame, thumbW, thumbH); err != nil {
			slog.Debug("gpu: thumbnail download failed", "node", ts.nodeNames[i], "err", err)
			continue
		}

		// Make a copy for the background encode goroutine
		nv12Copy := make([]byte, len(ts.cpuBufs[i]))
		copy(nv12Copy, ts.cpuBufs[i])

		select {
		case ts.encodeCh <- thumbnailEncodeReq{
			nodeID: ts.nodeNames[i],
			nv12:   nv12Copy,
			store:  ts.store,
		}:
		default:
			// Channel full -- drop this thumbnail
		}
	}
}

// encodeLoop runs in a background goroutine, converting NV12 to JPEG.
func (ts *thumbnailState) encodeLoop() {
	defer close(ts.done)
	for req := range ts.encodeCh {
		jpegData := nv12ToJPEG(req.nv12, thumbW, thumbH)
		if jpegData != nil {
			req.store.Store(req.nodeID, jpegData, thumbW, thumbH)
		}
	}
}

// close releases all scratch resources and stops the encode goroutine.
func (ts *thumbnailState) close() {
	if ts.encodeCh != nil {
		close(ts.encodeCh)
		<-ts.done
	}
	// Release scratch frames back to pool
	for i, f := range ts.scratchFrames {
		if f != nil {
			f.Release()
			ts.scratchFrames[i] = nil
		}
	}
	if ts.scratchPool != nil {
		ts.scratchPool.Close()
	}
	if ts.workQueue != nil {
		CloseWorkQueue(ts.workQueue)
	}
}

// nv12ToJPEG converts an NV12 buffer (non-pitched, contiguous) to JPEG.
// The conversion is NV12 → RGB → JPEG, done entirely on CPU.
func nv12ToJPEG(nv12 []byte, w, h int) []byte {
	ySize := w * h
	uvSize := w * (h / 2)
	if len(nv12) < ySize+uvSize {
		return nil
	}

	yPlane := nv12[:ySize]
	uvPlane := nv12[ySize : ySize+uvSize]

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	pix := img.Pix

	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			y := int(yPlane[row*w+col])
			uvRow := row / 2
			uvCol := (col / 2) * 2
			cb := int(uvPlane[uvRow*w+uvCol])
			cr := int(uvPlane[uvRow*w+uvCol+1])

			// BT.709 limited range → full range RGB
			yf := float64(y-16) * (255.0 / 219.0)
			cbf := float64(cb) - 128
			crf := float64(cr) - 128

			r := yf + 1.5748*crf
			g := yf - 0.1873*cbf - 0.4681*crf
			b := yf + 1.8556*cbf

			off := (row*w + col) * 4
			pix[off+0] = clampByte(r)
			pix[off+1] = clampByte(g)
			pix[off+2] = clampByte(b)
			pix[off+3] = 255
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: thumbJPEGQuality}); err != nil {
		return nil
	}
	return buf.Bytes()
}

func clampByte(v float64) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

// shouldCaptureThisFrame checks if the current frame should trigger
// thumbnail capture. Returns the thumbnailState if yes, nil otherwise.
func (p *GPUPipeline) shouldCaptureThisFrame() *thumbnailState {
	ts := p.thumbState.Load()
	if ts == nil {
		return nil
	}
	if ts.store == nil || !ts.store.ShouldCapture() {
		return nil
	}
	if time.Since(ts.lastCapture) < thumbCaptureRate {
		return nil
	}

	// Ensure scratch buffers are initialized for current node count
	if len(ts.scratchFrames) != len(p.activeNodes) {
		names := make([]string, len(p.activeNodes))
		for i, n := range p.activeNodes {
			names[i] = n.Name()
		}
		if err := ts.initScratch(p.ctx, len(p.activeNodes), names); err != nil {
			slog.Warn("gpu: thumbnail scratch init failed", "err", err)
			return nil
		}
	}

	return ts
}

// closeThumbnails releases thumbnail resources. Called from Close().
func (p *GPUPipeline) closeThumbnails() {
	ts := p.thumbState.Load()
	if ts != nil {
		ts.close()
		p.thumbState.Store(nil)
	}
}

// Compile-time assertion: GPUPipeline must have thumbState field.
var _ = func() {
	var p GPUPipeline
	_ = p.thumbState
}
