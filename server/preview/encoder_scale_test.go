package preview

import (
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/internal/video"
)

// encodedSize sends frames of w x h until the relay reports its VideoInfo
// and returns the size the stream declares.
func encodedSize(t *testing.T, cfg Config, frame []byte, w, h int) (int, int) {
	t.Helper()
	relay := &mockRelay{}
	cfg.Relay = relay
	enc, err := NewEncoder(cfg)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	defer enc.Stop()
	deadline := time.Now().Add(10 * time.Second)
	for i := 0; relay.getVideoInfo() == nil; i++ {
		if time.Now().After(deadline) {
			t.Fatal("no VideoInfo from the encoder")
		}
		enc.Send(frame, w, h, int64(i)*3000)
		time.Sleep(20 * time.Millisecond)
	}
	info := relay.getVideoInfo()
	return info.Width, info.Height
}

func scaleCfg(scale bool) Config {
	return Config{SourceKey: "s", Width: 640, Height: 360, Bitrate: 300_000, FPSNum: 50, FPSDen: 1, ScaleDown: scale}
}

// A source larger than the configured size is scaled down to it on the
// encode goroutine, so the sender does not scale it on its own.
func TestEncoder_ScaleDown_EncodesAtConfiguredSize(t *testing.T) {
	skipWithoutEncoder(t)
	if w, h := encodedSize(t, scaleCfg(true), makeYUV420(1920, 1080, 90), 1920, 1080); w != 640 || h != 360 {
		t.Fatalf("encoded %dx%d, want 640x360", w, h)
	}
}

// A 10-bit 4:2:2 source is downconverted, then scaled.
func TestEncoder_ScaleDown_TenBitSource(t *testing.T) {
	skipWithoutEncoder(t)
	frame := video.NewBlackFrame(video.YUV422_10bit, 1920, 1080)
	if w, h := encodedSize(t, scaleCfg(true), frame, 1920, 1080); w != 640 || h != 360 {
		t.Fatalf("encoded %dx%d, want 640x360", w, h)
	}
}

// A source no larger than the configured size is not scaled up, and
// without ScaleDown a source is encoded at its own size, as before.
func TestEncoder_ScaleDown_LeavesOtherSizes(t *testing.T) {
	skipWithoutEncoder(t)
	if w, h := encodedSize(t, scaleCfg(true), makeYUV420(480, 270, 90), 480, 270); w != 480 || h != 270 {
		t.Fatalf("small source encoded %dx%d, want 480x270", w, h)
	}
	if w, h := encodedSize(t, scaleCfg(false), makeYUV420(1280, 720, 90), 1280, 720); w != 1280 || h != 720 {
		t.Fatalf("without ScaleDown encoded %dx%d, want 1280x720", w, h)
	}
}
