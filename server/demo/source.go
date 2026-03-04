// Package demo provides simulated camera sources for testing and demonstration.
package demo

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
)

// SwitcherAPI is the subset of switcher.Switcher needed by the demo package.
type SwitcherAPI interface {
	RegisterSource(key string, relay *distribution.Relay)
	SetLabel(ctx context.Context, key, label string) error
	Cut(ctx context.Context, source string) error
	SetPreview(ctx context.Context, source string) error
}

// MixerAPI is the subset of audio.AudioMixer needed by the demo package.
type MixerAPI interface {
	AddChannel(key string)
}

// Minimal valid H.264 baseline SPS/PPS for a 320×240 stream.
var (
	demoSPS = []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88}
	demoPPS = []byte{0x68, 0xCE, 0x38, 0x80}
)

// StartSources creates n demo sources, registers them with the switcher,
// sets cam1 as program / cam2 as preview, labels them "Camera 1" etc,
// and starts frame generation goroutines at ~30fps.
// Returns a stop function that cancels all generators.
func StartSources(ctx context.Context, sw SwitcherAPI, mixer MixerAPI, n int) func() {
	ctx, cancel := context.WithCancel(ctx)

	relays := make([]*distribution.Relay, n)
	for i := range n {
		key := fmt.Sprintf("cam%d", i+1)
		label := fmt.Sprintf("Camera %d", i+1)

		relays[i] = distribution.NewRelay()
		sw.RegisterSource(key, relays[i])
		mixer.AddChannel(key)
		if err := sw.SetLabel(ctx, key, label); err != nil {
			slog.Warn("demo: failed to set label", "key", key, "err", err)
		}
	}

	// Set initial program/preview.
	if n >= 1 {
		if err := sw.Cut(ctx, "cam1"); err != nil {
			slog.Warn("demo: failed to cut to cam1", "err", err)
		}
	}
	if n >= 2 {
		if err := sw.SetPreview(ctx, "cam2"); err != nil {
			slog.Warn("demo: failed to set preview to cam2", "err", err)
		}
	}

	// Start frame generators.
	for i := range n {
		go generateFrames(ctx, relays[i], fmt.Sprintf("cam%d", i+1))
	}

	slog.Info("demo: started simulated sources", "count", n)
	return cancel
}

// generateFrames pumps video+audio frames into a relay at ~30fps.
// Every 30th frame is a keyframe (1 per second). PTS uses 90kHz clock.
func generateFrames(ctx context.Context, relay *distribution.Relay, key string) {
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()

	var frameNum int64
	const ptsPerFrame = 3000 // 33ms × 90kHz

	for {
		select {
		case <-ctx.Done():
			slog.Debug("demo: source stopped", "key", key)
			return
		case <-ticker.C:
			pts := frameNum * ptsPerFrame
			isKeyframe := frameNum%30 == 0

			if isKeyframe {
				relay.BroadcastVideo(&media.VideoFrame{
					PTS:        pts,
					DTS:        pts,
					IsKeyframe: true,
					SPS:        demoSPS,
					PPS:        demoPPS,
					WireData:   []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
					Codec:      "h264",
				})
			} else {
				relay.BroadcastVideo(&media.VideoFrame{
					PTS:        pts,
					DTS:        pts,
					IsKeyframe: false,
					WireData:   []byte{0x00, 0x00, 0x00, 0x03, 0x41, 0x9A, 0x24},
					Codec:      "h264",
				})
			}

			relay.BroadcastAudio(&media.AudioFrame{
				PTS:        pts,
				Data:       []byte{0xDE, 0x04, 0x00, 0x26, 0x20, 0x54, 0xE5, 0x00},
				SampleRate: 48000,
				Channels:   2,
			})

			frameNum++
		}
	}
}
