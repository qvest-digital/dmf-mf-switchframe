//go:build cgo && st2110 && integration

package st2110

import (
	"context"
	"testing"
	"time"
)

const testServerIP = "10.128.0.2"

// TestIntegration_MTLInstance verifies MTL initialization, PTP clock access,
// and video/audio session creation on real hardware.
//
// Multicast TX→RX loopback requires either:
//   - A NIC with hardware loopback support
//   - Two NICs connected back-to-back
//   - A multicast-enabled switch/router
//
// Cloud VMs (GCP/AWS) don't route multicast back to the same host,
// so round-trip tests are skipped in that environment.
func TestIntegration_MTLInstance(t *testing.T) {
	inst, err := NewInstance(InstanceConfig{
		Port:     "kernel:ens4",
		SourceIP: testServerIP,
		PMDType:  "kernel",
		TxQueues: 4,
		RxQueues: 4,
		LogLevel: 4,
	})
	if err != nil {
		t.Fatalf("NewInstance failed: %v", err)
	}
	defer inst.Close()

	// Subtest: PTP status.
	t.Run("PTPStatus", func(t *testing.T) {
		status, err := inst.PTPStatus()
		if err != nil {
			t.Fatalf("PTPStatus: %v", err)
		}
		t.Logf("PTP: locked=%v offset=%dns", status.Locked, status.OffsetNs)
	})

	// Subtest: Video TX session creation.
	t.Run("CreateVideoTx", func(t *testing.T) {
		vtx, err := inst.CreateVideoTx(TxVideoConfig{
			MulticastIP: "239.0.0.1",
			SourceIP:    testServerIP,
			Port:        20000,
			Format: VideoFormat{
				Width:  1920,
				Height: 1080,
				FPSNum: 30,
				FPSDen: 1,
			},
			FrameBuffers: 3,
		})
		if err != nil {
			t.Fatalf("CreateVideoTx: %v", err)
		}
		defer vtx.Close()

		// Submit a test frame to verify the pipeline works.
		yuvSize := 1920*1080 + 960*540*2
		yuv := make([]byte, yuvSize)
		for i := range yuv {
			yuv[i] = 128
		}
		if err := vtx.PutFrame(yuv); err != nil {
			t.Fatalf("PutFrame: %v", err)
		}
		t.Log("Video TX: frame submitted successfully")
	})

	// Subtest: Video RX session creation.
	t.Run("CreateVideoRx", func(t *testing.T) {
		vrx, err := inst.CreateVideoRx(RxVideoConfig{
			MulticastIP: "239.0.0.10",
			SourceIP:    testServerIP,
			Port:        20100,
			Format: VideoFormat{
				Width:  1920,
				Height: 1080,
				FPSNum: 30,
				FPSDen: 1,
			},
			FrameBuffers: 3,
		})
		if err != nil {
			t.Fatalf("CreateVideoRx: %v", err)
		}
		defer vrx.Close()

		// Verify GetFrame returns timeout (no sender on this address).
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_, _, _, _, err = vrx.GetFrame(ctx)
		if err != context.DeadlineExceeded {
			t.Fatalf("expected DeadlineExceeded, got: %v", err)
		}
		t.Log("Video RX: session created, GetFrame correctly timed out (no sender)")
	})

	// Subtest: Audio TX session creation.
	t.Run("CreateAudioTx", func(t *testing.T) {
		atx, err := inst.CreateAudioTx(TxAudioConfig{
			MulticastIP: "239.0.0.2",
			SourceIP:    testServerIP,
			Port:        20010,
			Format: AudioFormat{
				SampleRate:   48000,
				Channels:     2,
				BitDepth:     24,
				PacketTimeUs: 1000,
			},
			FrameBuffers: 3,
		})
		if err != nil {
			t.Fatalf("CreateAudioTx: %v", err)
		}
		defer atx.Close()

		pcm := make([]float32, 96) // 48 samples * 2 channels
		for i := range pcm {
			pcm[i] = 0.5
		}
		if err := atx.PutFrame(pcm); err != nil {
			t.Fatalf("PutFrame: %v", err)
		}
		t.Log("Audio TX: frame submitted successfully")
	})

	// Subtest: Audio RX session creation.
	t.Run("CreateAudioRx", func(t *testing.T) {
		arx, err := inst.CreateAudioRx(RxAudioConfig{
			MulticastIP: "239.0.0.20",
			SourceIP:    testServerIP,
			Port:        20110,
			Format: AudioFormat{
				SampleRate:   48000,
				Channels:     2,
				BitDepth:     24,
				PacketTimeUs: 1000,
			},
			FrameBuffers: 3,
		})
		if err != nil {
			t.Fatalf("CreateAudioRx: %v", err)
		}
		defer arx.Close()
		t.Log("Audio RX: session created successfully")
	})
}
