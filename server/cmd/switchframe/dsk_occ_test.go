//go:build integration && cgo && cuda

package main

import (
	"testing"

	"github.com/zsiec/switchframe/server/gpu"
)

// TestDSKOcclusionKernel verifies that the occlusion-aware DSK kernel
// correctly suppresses overlay alpha in occluded regions.
// Left half of the frame has no occlusion - overlay should appear.
// Right half has full occlusion - overlay should be blocked.
func TestDSKOcclusionKernel(t *testing.T) {
	ctx := requireGPU(t)

	W, H := 256, 256

	// Allocate a GPUFrame (NV12) from the pool
	pool, err := gpu.NewFramePool(ctx, W, H, 4)
	if err != nil {
		t.Fatalf("NewFramePool: %v", err)
	}
	defer pool.Close()

	frame, err := pool.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer frame.Release()

	// Fill frame with mid-gray via upload (YUV420p: Y + Cb + Cr)
	yuvSize := W*H + W*H/2 // YUV420p
	gray := make([]byte, yuvSize)
	for i := 0; i < W*H; i++ {
		gray[i] = 128 // Y
	}
	for i := W * H; i < yuvSize; i++ {
		gray[i] = 128 // Cb + Cr
	}
	if err := gpu.Upload(ctx, frame, gray, W, H); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	_ = frame.Pitch

	// Upload RGBA overlay: solid red, fully opaque
	overlay := make([]byte, W*H*4)
	for i := 0; i < W*H; i++ {
		overlay[i*4+0] = 255 // R
		overlay[i*4+1] = 0   // G
		overlay[i*4+2] = 0   // B
		overlay[i*4+3] = 255 // A
	}
	gpuOverlay, err := gpu.UploadOverlay(ctx, overlay, W, H)
	if err != nil {
		t.Fatalf("UploadOverlay: %v", err)
	}
	defer gpu.FreeOverlay(gpuOverlay)

	// Occlusion mask: left=0 (background), right=255 (foreground/person)
	occMask := make([]byte, W*H)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if x >= W/2 {
				occMask[y*W+x] = 255
			}
		}
	}
	occBuf, err := gpu.AllocDeviceBytes(len(occMask))
	if err != nil {
		t.Fatalf("alloc occ: %v", err)
	}
	defer gpu.FreeDeviceBytes(occBuf)
	if err := gpu.UploadBytes(occBuf, occMask); err != nil {
		t.Fatalf("upload occ: %v", err)
	}

	// Run occlusion-aware DSK composite
	err = gpu.DSKCompositeFullFrameOcc(ctx, frame, gpuOverlay, 1.0,
		uintptr(occBuf), W, H)
	if err != nil {
		t.Fatalf("DSKCompositeFullFrameOcc: %v", err)
	}

	// Download to check
	dlBuf := make([]byte, yuvSize)
	if err := gpu.Download(ctx, dlBuf, frame, W, H); err != nil {
		t.Fatalf("Download: %v", err)
	}
	yPlane := dlBuf[:W*H]

	// Verify: left half should show the red overlay, right half should be unchanged
	leftChanged := 0
	rightChanged := 0
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			yVal := yPlane[y*W+x]
			if x < W/2 {
				if yVal != 128 {
					leftChanged++
				}
			} else {
				if yVal != 128 {
					rightChanged++
				}
			}
		}
	}

	totalHalf := W / 2 * H
	t.Logf("Left half - background, no occlusion: %d/%d changed (%.1f%%)",
		leftChanged, totalHalf, 100.0*float64(leftChanged)/float64(totalHalf))
	t.Logf("Right half - foreground, occluded: %d/%d changed (%.1f%%)",
		rightChanged, totalHalf, 100.0*float64(rightChanged)/float64(totalHalf))

	if leftChanged < totalHalf/2 {
		t.Errorf("Left half should have overlay applied, only %d/%d changed", leftChanged, totalHalf)
	}
	if rightChanged > totalHalf/10 {
		t.Errorf("Right half should be protected by occlusion, but %d/%d changed", rightChanged, totalHalf)
	}

	t.Log("DSK occlusion verified: overlay visible on background, hidden behind foreground")
}
