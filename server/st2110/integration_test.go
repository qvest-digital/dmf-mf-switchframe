//go:build cgo && st2110 && integration

package st2110

import (
	"context"
	"testing"
	"time"
)

// createTestInstance creates an MTL instance using kernel socket mode on ens4.
// This requires MTL to be installed and hugepages configured (dev server only).
// Must run with sudo for hugepage access.
func createTestInstance(t *testing.T) *Instance {
	t.Helper()
	inst, err := NewInstance(InstanceConfig{
		Port:     "kernel:ens4",
		SourceIP: "10.128.0.2",
		PMDType:  "kernel",
		TxQueues: 4,
		RxQueues: 4,
		LogLevel: 4,
	})
	if err != nil {
		t.Fatalf("NewInstance failed: %v", err)
	}
	t.Cleanup(func() { inst.Close() })
	return inst
}

func TestIntegration_VideoRoundTrip(t *testing.T) {
	inst := createTestInstance(t)

	const (
		width  = 1920
		height = 1080
		mcastIP = "239.0.0.1"
		port    = 20000
	)

	vfmt := VideoFormat{
		Width:       width,
		Height:      height,
		FPSNum:      30,
		FPSDen:      1,
		Sampling:    "YCbCr-4:2:2",
		Colorimetry: "BT709",
		BitDepth:    10,
	}

	// Create TX first, then RX.
	vtx, err := inst.CreateVideoTx(TxVideoConfig{
		MulticastIP:  mcastIP,
		SourceIP:     "127.0.0.1",
		Port:         port,
		Format:       vfmt,
		FrameBuffers: 3,
	})
	if err != nil {
		t.Fatalf("CreateVideoTx: %v", err)
	}
	t.Cleanup(func() { vtx.Close() })

	vrx, err := inst.CreateVideoRx(RxVideoConfig{
		MulticastIP:  mcastIP,
		SourceIP:     "127.0.0.1",
		Port:         port,
		Format:       vfmt,
		FrameBuffers: 3,
	})
	if err != nil {
		t.Fatalf("CreateVideoRx: %v", err)
	}
	t.Cleanup(func() { vrx.Close() })

	// Build a YUV420p test frame: Y=width*height, U=width*height/4, V=width*height/4
	ySize := width * height
	uvSize := ySize / 4
	frameSize := ySize + 2*uvSize
	frame := make([]byte, frameSize)

	// Fill Y with 128 (mid-gray), U and V with 128 (neutral chroma).
	for i := 0; i < ySize; i++ {
		frame[i] = 128
	}
	for i := ySize; i < frameSize; i++ {
		frame[i] = 128
	}

	// Send the frame.
	if err := vtx.PutFrame(frame); err != nil {
		t.Fatalf("PutFrame: %v", err)
	}

	// Receive with a 5-second timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, w, h, _, err := vrx.GetFrame(ctx)
	if err != nil {
		t.Fatalf("GetFrame: %v", err)
	}

	if w != width || h != height {
		t.Errorf("dimensions: got %dx%d, want %dx%d", w, h, width, height)
	}

	// The received YUV420p frame should match the expected size.
	if len(data) != frameSize {
		t.Errorf("data size: got %d, want %d", len(data), frameSize)
	}
}

func TestIntegration_AudioRoundTrip(t *testing.T) {
	inst := createTestInstance(t)

	const (
		sampleRate = 48000
		channels   = 2
		samples    = 48
		mcastIP    = "239.0.0.2"
		port       = 20010
	)

	afmt := AudioFormat{
		SampleRate:   sampleRate,
		Channels:     channels,
		BitDepth:     24,
		PacketTimeUs: 1000,
	}

	atx, err := inst.CreateAudioTx(TxAudioConfig{
		MulticastIP:  mcastIP,
		SourceIP:     "127.0.0.1",
		Port:         port,
		Format:       afmt,
		FrameBuffers: 3,
	})
	if err != nil {
		t.Fatalf("CreateAudioTx: %v", err)
	}
	t.Cleanup(func() { atx.Close() })

	arx, err := inst.CreateAudioRx(RxAudioConfig{
		MulticastIP:  mcastIP,
		SourceIP:     "127.0.0.1",
		Port:         port,
		Format:       afmt,
		FrameBuffers: 3,
	})
	if err != nil {
		t.Fatalf("CreateAudioRx: %v", err)
	}
	t.Cleanup(func() { arx.Close() })

	// Build interleaved float32 PCM: samples * channels.
	pcm := make([]float32, samples*channels)
	for i := range pcm {
		pcm[i] = 0.5
	}

	if err := atx.PutFrame(pcm); err != nil {
		t.Fatalf("PutFrame: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, samplesPerCh, err := arx.GetFrame(ctx)
	if err != nil {
		t.Fatalf("GetFrame: %v", err)
	}

	if len(data) == 0 {
		t.Error("received empty audio data")
	}

	if samplesPerCh == 0 {
		t.Error("samplesPerCh is 0")
	}
}

func TestIntegration_PTPStatus(t *testing.T) {
	inst := createTestInstance(t)

	status, err := inst.PTPStatus()
	if err != nil {
		t.Fatalf("PTPStatus: %v", err)
	}

	// In loopback mode PTP may not be locked, but the call should succeed.
	t.Logf("PTP status: locked=%v grandmaster=%s offset=%dns domain=%d",
		status.Locked, status.GrandmasterID, status.OffsetNs, status.Domain)
}
