package main

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// initPlayout creates the playout Manager and wires lifecycle callbacks
// so playout channels register as switcher sources. Follows the initClips pattern.
func (a *App) initPlayout() error {
	mgr := playout.NewManager(playout.ManagerConfig{
		// Phase 1: no real demux — playout channels will use
		// the clip store + demuxer once the asset cache is implemented.
		// For now, the DemuxFunc is nil (channels can only be tested via API
		// with test fixtures or by extending this in Phase 2).
	})

	// PTS anchoring.
	mgr.SetPTSProvider(a.sw.LastBroadcastVideoPTS)

	// Channel lifecycle: register/unregister as switcher sources.
	mgr.OnChannelLifecycle(
		func(channelID int, key string) {
			a.sw.RegisterReplaySource(key)
			a.mixer.AddChannel(key)
			_ = a.mixer.SetAFV(key, true)
			a.mixer.OnProgramChange(a.sw.ProgramSource())
		},
		func(channelID int, key string) {
			a.sw.UnregisterSource(key)
			a.mixer.RemoveChannel(key)
		},
	)

	// Raw video output: playout sends decoded YUV to switcher pipeline.
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool) {
		pf := a.sw.PipelineFormat()
		outYUV, outW, outH := yuv, w, h
		if w != pf.Width || h != pf.Height {
			targetSize := pf.Width * pf.Height * 3 / 2
			scaled := make([]byte, targetSize)
			transition.ScaleYUV420(yuv, w, h, scaled, pf.Width, pf.Height)
			outYUV, outW, outH = scaled, pf.Width, pf.Height
		}

		frame := &switcher.ProcessingFrame{
			YUV:        outYUV,
			Width:      outW,
			Height:     outH,
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
		}
		a.sw.IngestReplayVideo(key, frame)
	})

	// State change broadcasts.
	mgr.SetOnStateChange(func() {
		a.clearLastOperator()
		a.broadcastState(nil)
	})

	a.playoutMgr = mgr
	slog.Info("playout manager initialized")
	return nil
}

// playoutChannelIDFromKey extracts the channel ID from a "playout:N" key.
func playoutChannelIDFromKey(key string) int {
	s := strings.TrimPrefix(key, "playout:")
	n, _ := strconv.Atoi(s)
	return n
}
