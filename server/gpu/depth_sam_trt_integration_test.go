//go:build cgo && cuda && tensorrt && integration

package gpu_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/arinfer"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/sidecar"
)

const (
	depthSidecarBin = "/tmp/switchframe-ai"
	depthModelDir   = "/data/switchframe/ar_models"
)

// TestDepthSidecar_E2E spawns the AI sidecar, enables depth via the
// InferenceProxy, submits a frame through the GPU pool, waits for
// occlusion_ready with the depth IPC handle, and verifies the host
// can import it.
//
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,tensorrt,integration' -v -race -timeout 120s -run TestDepthSidecar_E2E ./gpu/
func TestDepthSidecar_E2E(t *testing.T) {
	if _, err := os.Stat(depthSidecarBin); err != nil {
		t.Skipf("AI sidecar binary not found at %s", depthSidecarBin)
	}
	if _, err := os.Stat(depthModelDir + "/depth_anything_v2_vits.plan"); err != nil {
		t.Skipf("depth model not found at %s/depth_anything_v2_vits.plan", depthModelDir)
	}

	gpuCtx, err := gpu.NewContext()
	if err != nil {
		t.Fatalf("gpu context: %v", err)
	}
	defer gpuCtx.Close()
	ctx := gpuCtx
	const width, height = 640, 480
	pool, err := gpu.NewFramePool(ctx, width, height, 8)
	if err != nil {
		t.Fatalf("create frame pool: %v", err)
	}
	defer pool.Close()

	framePtrs := pool.AllDevPtrs()
	if len(framePtrs) == 0 {
		t.Fatal("no frame pool device pointers")
	}

	ipcPool, err := sidecar.NewCUDAGPUPoolHostFromPtrs(framePtrs, sidecar.GPUPoolConfig{
		Width:  width,
		Height: height,
		Pitch:  pool.Pitch(),
	}, "/switchframe-depth-test")
	if err != nil {
		t.Fatalf("create IPC pool: %v", err)
	}
	defer ipcPool.Close()

	// Track messages.
	occlusionReadyCh := make(chan map[string]any, 2)
	arSlotDoneCh := make(chan struct{}, 4)

	host := sidecar.NewHost(sidecar.Config{
		Binary:       depthSidecarBin,
		ReadyTimeout: 120 * time.Second,
		BuildArgs: func(socketDir string) []string {
			return []string{"--models-dir", depthModelDir, socketDir + "/control.sock"}
		},
	})
	host.OnMessage = func(msg *sidecar.Message) {
		switch msg.Type {
		case sidecar.MsgOcclusionReady:
			if data, ok := msg.Data.(map[string]any); ok {
				select {
				case occlusionReadyCh <- data:
				default:
				}
			}
		case sidecar.MsgARSlotDone:
			select {
			case arSlotDoneCh <- struct{}{}:
			default:
			}
		}
	}

	t.Log("starting sidecar with --models-dir for depth inference...")
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start sidecar: %v", err)
	}
	defer host.Close()

	// Send GPU pool handshake.
	hs := &sidecar.GPUPoolHandshake{
		Config:       ipcPool.Config(),
		ShmName:      "/switchframe-depth-test",
		FrameHandles: ipcPool.FrameHandles(),
		ReadyEvents:  ipcPool.ReadyEventHandles(),
		DoneEvents:   ipcPool.DoneEventHandles(),
	}
	if err := host.Send(&sidecar.Message{Cmd: sidecar.CmdGPUPoolInit, Data: hs}); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Create InferenceProxy.
	arProxy := arinfer.NewInferenceProxy(host, ipcPool, pool)
	defer arProxy.Close()

	// Wire OnMessage to proxy.
	origOnMessage := host.OnMessage
	host.OnMessage = func(msg *sidecar.Message) {
		arProxy.HandleMessage(msg)
		origOnMessage(msg)
	}

	// Enable depth.
	if err := arProxy.EnableDepth(""); err != nil {
		t.Fatalf("enable depth: %v", err)
	}

	// Wait for occlusion_ready with depth IPC handle.
	select {
	case data := <-occlusionReadyCh:
		t.Log("occlusion_ready received")
		dh, _ := data["depth_handle"].(string)
		if dh == "" {
			t.Fatal("occlusion_ready missing depth_handle")
		}
		dw := int(floatValMap(data, "depth_w"))
		dh2 := int(floatValMap(data, "depth_h"))
		if dw == 0 || dh2 == 0 {
			t.Fatalf("depth dimensions %dx%d invalid", dw, dh2)
		}
		t.Logf("depth handle exported: %dx%d", dw, dh2)
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for occlusion_ready")
	}

	// Verify proxy imported the handle.
	occ := arProxy.LastOcclusion()
	if occ == nil {
		t.Fatal("LastOcclusion nil after occlusion_ready")
	}
	if occ.DepthPtr == 0 {
		t.Fatal("DepthPtr is 0 — IPC handle not imported")
	}
	t.Logf("depth DevPtr=0x%x, dims=%dx%d", occ.DepthPtr, occ.DepthW, occ.DepthH)

	// Upload a gray frame and submit for AR inference.
	frame, err := pool.Acquire()
	if err != nil {
		t.Fatalf("acquire frame: %v", err)
	}

	ySize := width * height
	yuv := make([]byte, ySize+ySize/2)
	for i := range yuv {
		yuv[i] = 128
	}
	if err := gpu.Upload(ctx, frame, yuv, width, height); err != nil {
		frame.Release()
		t.Fatalf("upload: %v", err)
	}

	arProxy.ProcessFrame(uintptr(frame.DevPtr), width, height, pool.Pitch())
	frame.Release()

	// Wait for ar_slot_done.
	select {
	case <-arSlotDoneCh:
		t.Log("ar_slot_done received — depth inference completed")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for ar_slot_done")
	}

	// Verify occlusion map updated.
	occ2 := arProxy.LastOcclusion()
	if occ2 == nil || occ2.DepthPtr == 0 {
		t.Fatal("LastOcclusion nil or DepthPtr 0 after frame processing")
	}
	t.Logf("after inference: DepthPtr=0x%x, dims=%dx%d", occ2.DepthPtr, occ2.DepthW, occ2.DepthH)

	// Disable depth.
	arProxy.DisableDepth()
	if arProxy.IsActive() {
		t.Error("expected IsActive=false after DisableDepth")
	}

	t.Log("depth sidecar E2E test passed")
}

// TestSAMSidecar_E2E spawns the AI sidecar, enables SAM via the
// InferenceProxy, adds a prompt, submits a frame, waits for
// occlusion_ready with the mask IPC handle, and verifies import.
//
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,tensorrt,integration' -v -race -timeout 120s -run TestSAMSidecar_E2E ./gpu/
func TestSAMSidecar_E2E(t *testing.T) {
	if _, err := os.Stat(depthSidecarBin); err != nil {
		t.Skipf("AI sidecar binary not found at %s", depthSidecarBin)
	}
	if _, err := os.Stat(depthModelDir + "/sam2_encoder.plan"); err != nil {
		t.Skipf("SAM encoder not found at %s/sam2_encoder.plan", depthModelDir)
	}
	if _, err := os.Stat(depthModelDir + "/sam2_decoder.plan"); err != nil {
		t.Skipf("SAM decoder not found at %s/sam2_decoder.plan", depthModelDir)
	}

	gpuCtx, err := gpu.NewContext()
	if err != nil {
		t.Fatalf("gpu context: %v", err)
	}
	defer gpuCtx.Close()
	ctx := gpuCtx
	const width, height = 640, 480
	pool, err := gpu.NewFramePool(ctx, width, height, 8)
	if err != nil {
		t.Fatalf("create frame pool: %v", err)
	}
	defer pool.Close()

	framePtrs := pool.AllDevPtrs()
	ipcPool, err := sidecar.NewCUDAGPUPoolHostFromPtrs(framePtrs, sidecar.GPUPoolConfig{
		Width:  width,
		Height: height,
		Pitch:  pool.Pitch(),
	}, "/switchframe-sam-test")
	if err != nil {
		t.Fatalf("create IPC pool: %v", err)
	}
	defer ipcPool.Close()

	occlusionReadyCh := make(chan map[string]any, 4)
	arSlotDoneCh := make(chan struct{}, 4)

	host := sidecar.NewHost(sidecar.Config{
		Binary:       depthSidecarBin,
		ReadyTimeout: 120 * time.Second,
		BuildArgs: func(socketDir string) []string {
			return []string{"--models-dir", depthModelDir, socketDir + "/control.sock"}
		},
	})
	host.OnMessage = func(msg *sidecar.Message) {
		switch msg.Type {
		case sidecar.MsgOcclusionReady:
			if data, ok := msg.Data.(map[string]any); ok {
				select {
				case occlusionReadyCh <- data:
				default:
				}
			}
		case sidecar.MsgARSlotDone:
			select {
			case arSlotDoneCh <- struct{}{}:
			default:
			}
		}
	}

	t.Log("starting sidecar for SAM inference...")
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start sidecar: %v", err)
	}
	defer host.Close()

	hs := &sidecar.GPUPoolHandshake{
		Config:       ipcPool.Config(),
		ShmName:      "/switchframe-sam-test",
		FrameHandles: ipcPool.FrameHandles(),
		ReadyEvents:  ipcPool.ReadyEventHandles(),
		DoneEvents:   ipcPool.DoneEventHandles(),
	}
	if err := host.Send(&sidecar.Message{Cmd: sidecar.CmdGPUPoolInit, Data: hs}); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	arProxy := arinfer.NewInferenceProxy(host, ipcPool, pool)
	defer arProxy.Close()

	origOnMessage := host.OnMessage
	host.OnMessage = func(msg *sidecar.Message) {
		arProxy.HandleMessage(msg)
		origOnMessage(msg)
	}

	// Enable SAM.
	if err := arProxy.EnableSAM("", ""); err != nil {
		t.Fatalf("enable SAM: %v", err)
	}

	// Wait for occlusion_ready with mask IPC handle (pre-allocated at enable).
	select {
	case data := <-occlusionReadyCh:
		t.Log("occlusion_ready received for SAM")
		mh, _ := data["mask_handle"].(string)
		if mh == "" {
			t.Fatal("occlusion_ready missing mask_handle")
		}
		mw := int(floatValMap(data, "mask_w"))
		mhVal := int(floatValMap(data, "mask_h"))
		t.Logf("mask handle exported: %dx%d", mw, mhVal)
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for occlusion_ready")
	}

	// Verify proxy imported the handle.
	occ := arProxy.LastOcclusion()
	if occ == nil {
		t.Fatal("LastOcclusion nil after occlusion_ready")
	}
	if occ.MaskPtr == 0 {
		t.Fatal("MaskPtr is 0 — IPC handle not imported")
	}
	t.Logf("mask DevPtr=0x%x, dims=%dx%d", occ.MaskPtr, occ.Width, occ.Height)

	// Add a prompt and submit a frame.
	if err := arProxy.AddPrompt(arinfer.SAMPrompt{
		ID:     "test-center",
		Type:   "point",
		Points: [][2]int{{width / 2, height / 2}},
		Label:  "test",
	}); err != nil {
		t.Fatalf("add prompt: %v", err)
	}

	frame, err := pool.Acquire()
	if err != nil {
		t.Fatalf("acquire frame: %v", err)
	}

	ySize := width * height
	yuv := make([]byte, ySize+ySize/2)
	for i := range yuv {
		yuv[i] = 128
	}
	if err := gpu.Upload(ctx, frame, yuv, width, height); err != nil {
		frame.Release()
		t.Fatalf("upload: %v", err)
	}

	arProxy.ProcessFrame(uintptr(frame.DevPtr), width, height, pool.Pitch())
	frame.Release()

	select {
	case <-arSlotDoneCh:
		t.Log("ar_slot_done received — SAM inference completed")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for ar_slot_done")
	}

	// Remove prompt and disable.
	if err := arProxy.RemovePrompt("test-center"); err != nil {
		t.Fatalf("remove prompt: %v", err)
	}
	arProxy.DisableSAM()
	if arProxy.IsActive() {
		t.Error("expected IsActive=false after DisableSAM")
	}

	t.Log("SAM sidecar E2E test passed")
}

func floatValMap(m map[string]any, key string) float64 {
	v, _ := m[key].(float64)
	return v
}
