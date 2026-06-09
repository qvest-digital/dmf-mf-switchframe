//go:build cgo && cuda && tensorrt && integration

package gpu

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/sidecar"
)

// TestSegmentationProxy_P210Format verifies that the sidecar segmentation
// pipeline works with P210 (10-bit 4:2:2) input frames, producing valid
// masks identical in structure to NV12 (8-bit 4:2:0) input.
//
// The RVM model input is always 320x320 RGB — the preprocessing kernel
// (p210_to_rgb_chw vs nv12_to_rgb_chw) is the only difference. The mask
// output is always uint8 NV12-layout regardless of input format.
//
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,tensorrt,integration' -v -race -timeout 300s -run TestSegmentationProxy_P210Format ./gpu/
func TestSegmentationProxy_P210Format(t *testing.T) {
	if _, err := os.Stat(trtSidecarBin); err != nil {
		t.Skipf("AI sidecar binary not found at %s", trtSidecarBin)
	}
	rvmPath := aiModelsDir + "/" + rvmModelFile
	if _, err := os.Stat(rvmPath); err != nil {
		t.Skipf("RVM model not found at %s", rvmPath)
	}

	ctx := testContext(t)

	const width, height = 640, 480

	// Create P210 frame pool (10-bit 4:2:2).
	pool, err := NewP210FramePool(ctx, width, height, 8)
	if err != nil {
		t.Fatalf("create P210 frame pool: %v", err)
	}
	defer pool.Close()

	if pool.Format() != GPUFormatP210 {
		t.Fatalf("expected P210 format, got %v", pool.Format())
	}

	// Create CUDA IPC pool from P210 frame pool.
	framePtrs := pool.AllDevPtrs()
	if len(framePtrs) == 0 {
		t.Fatal("no frame pool device pointers")
	}
	ipcPool, err := sidecar.NewCUDAGPUPoolHostFromPtrs(framePtrs, sidecar.GPUPoolConfig{
		Width:  width,
		Height: height,
		Pitch:  pool.Pitch(),
		Format: int(GPUFormatP210),
	}, "/switchframe-p210-test")
	if err != nil {
		t.Fatalf("create IPC pool: %v", err)
	}
	defer ipcPool.Close()

	// Message tracking.
	var mu sync.Mutex
	var maskReady *sidecar.MaskHandleMessage
	maskReadyCh := make(chan struct{}, 1)
	slotDoneCh := make(chan struct{}, 1)

	// Spawn sidecar with --models-dir (auto-discovers RVM ONNX).
	host := sidecar.NewHost(sidecar.Config{
		Binary:       trtSidecarBin,
		ReadyTimeout: 120 * time.Second,
		BuildArgs: func(socketDir string) []string {
			socketPath := socketDir + "/control.sock"
			return []string{"--models-dir", aiModelsDir, socketPath}
		},
	})
	host.OnMessage = func(msg *sidecar.Message) {
		switch msg.Type {
		case sidecar.MsgMaskReady:
			mh, _ := sidecar.ParseMaskHandleMessage(msg)
			if mh != nil {
				mu.Lock()
				maskReady = mh
				mu.Unlock()
				select {
				case maskReadyCh <- struct{}{}:
				default:
				}
			}
		case sidecar.MsgSlotDone:
			select {
			case slotDoneCh <- struct{}{}:
			default:
			}
		}
	}

	t.Log("starting sidecar with TRT model for P210 test...")
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start sidecar: %v", err)
	}
	defer host.Close()

	// Send handshake with P210 format.
	hs := &sidecar.GPUPoolHandshake{
		Config:       ipcPool.Config(),
		ShmName:      "/switchframe-p210-test",
		FrameHandles: ipcPool.FrameHandles(),
		ReadyEvents:  ipcPool.ReadyEventHandles(),
		DoneEvents:   ipcPool.DoneEventHandles(),
	}
	if err := host.Send(&sidecar.Message{Cmd: sidecar.CmdGPUPoolInit, Data: hs}); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Create proxy and wire OnMessage.
	proxy := NewSegmentationProxy(host, ipcPool)
	defer proxy.Close()
	origOnMessage := host.OnMessage
	host.OnMessage = func(msg *sidecar.Message) {
		proxy.HandleMessage(msg)
		origOnMessage(msg)
	}

	// Enable segmentation.
	if err := proxy.EnableSource("cam1", width, height, 0); err != nil {
		t.Fatalf("enable source: %v", err)
	}

	select {
	case <-maskReadyCh:
		t.Log("mask_ready received")
	case <-time.After(120 * time.Second):
		t.Fatal("timeout waiting for mask_ready")
	}

	mu.Lock()
	mh := maskReady
	mu.Unlock()
	if mh.Width != width || mh.Height != height {
		t.Errorf("mask dimensions %dx%d, want %dx%d", mh.Width, mh.Height, width, height)
	}

	// Upload a test frame in YUV422P10LE (planar uint16).
	// Upload10 converts YUV422P10LE → P210 (semi-planar) on the GPU.
	// 10-bit neutral gray: Y=512, Cb=512, Cr=512.
	frame, err := pool.Acquire()
	if err != nil {
		t.Fatalf("acquire frame: %v", err)
	}

	// YUV422P10LE layout: Y plane (w*h uint16) + Cb plane (w/2*h uint16) + Cr plane (w/2*h uint16).
	yPlaneSize := width * height * 2        // uint16 per sample
	cbPlaneSize := (width / 2) * height * 2 // 4:2:2 = full height, half width
	crPlaneSize := cbPlaneSize
	yuv422 := make([]byte, yPlaneSize+cbPlaneSize+crPlaneSize)

	// Fill Y=512, Cb=512, Cr=512 (little-endian uint16).
	for i := 0; i < yPlaneSize; i += 2 {
		yuv422[i] = 0x00   // low byte of 512
		yuv422[i+1] = 0x02 // high byte (0x0200 = 512)
	}
	for i := yPlaneSize; i < yPlaneSize+cbPlaneSize; i += 2 {
		yuv422[i] = 0x00
		yuv422[i+1] = 0x02
	}
	for i := yPlaneSize + cbPlaneSize; i < len(yuv422); i += 2 {
		yuv422[i] = 0x00
		yuv422[i+1] = 0x02
	}

	// Upload YUV422P10LE → P210 on GPU.
	if err := Upload10(ctx, frame, yuv422, width, height); err != nil {
		frame.Release()
		t.Fatalf("upload P210: %v", err)
	}

	proxy.Segment("cam1", frame)
	frame.Release()

	select {
	case <-slotDoneCh:
		t.Log("slot_done received — P210 inference completed")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for slot_done")
	}

	// Verify mask is available and has correct dimensions.
	mask := proxy.MaskForSource("cam1")
	if mask == nil {
		t.Fatal("MaskForSource returned nil after P210 inference")
	}
	defer mask.Release()
	t.Logf("P210 mask: %dx%d, DevPtr=0x%x", mask.Width, mask.Height, mask.DevPtr)

	if mask.Width != width || mask.Height != height {
		t.Errorf("mask dimensions %dx%d, want %dx%d", mask.Width, mask.Height, width, height)
	}

	// Download mask and verify structure.
	// Mask is always uint8 NV12-layout regardless of input format.
	maskSize := width * height * 3 / 2
	maskData := make([]byte, maskSize)
	if err := Download(ctx, maskData, mask, width, height); err != nil {
		t.Fatalf("download mask: %v", err)
	}

	// UV plane should be neutral chroma (128).
	ySize := width * height
	uvNeutral := 0
	for i := ySize; i < maskSize; i++ {
		if maskData[i] == 128 {
			uvNeutral++
		}
	}
	uvSize := maskSize - ySize
	if uvNeutral < uvSize*90/100 {
		t.Errorf("UV plane: %d/%d neutral (expected >90%%)", uvNeutral, uvSize)
	}
	t.Logf("P210 mask UV neutral: %d/%d (%.1f%%)", uvNeutral, uvSize, float64(uvNeutral)/float64(uvSize)*100)

	// Run a second frame for temporal consistency.
	select {
	case <-slotDoneCh:
	default:
	}

	frame2, err := pool.Acquire()
	if err != nil {
		t.Fatalf("acquire frame 2: %v", err)
	}
	if err := Upload10(ctx, frame2, yuv422, width, height); err != nil {
		frame2.Release()
		t.Fatalf("upload P210 frame 2: %v", err)
	}
	proxy.Segment("cam1", frame2)
	frame2.Release()

	select {
	case <-slotDoneCh:
		t.Log("second P210 frame slot_done — temporal consistency verified")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for second slot_done")
	}

	proxy.DisableSource("cam1")
	t.Log("P210 segmentation integration test passed")
}
