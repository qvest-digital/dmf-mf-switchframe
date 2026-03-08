// Package demo provides simulated camera sources for testing and demonstration.
package demo

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/prism/moq"
)

// DemoStats tracks per-source frame counts for debug snapshots.
type DemoStats struct {
	mode              string
	file              string
	videoFramesLoaded int64
	audioFramesLoaded int64
	sources           sync.Map // map[string]*SourceStats
}

// SourceStats tracks per-source counters.
type SourceStats struct {
	VideoSent      atomic.Int64
	AudioSent      atomic.Int64
	LoopsCompleted atomic.Int64
}

// NewDemoStats creates a new DemoStats instance.
func NewDemoStats() *DemoStats {
	return &DemoStats{}
}

// SetFileInfo records the mode and file info after demuxing.
func (d *DemoStats) SetFileInfo(mode, file string, videoFrames, audioFrames int) {
	d.mode = mode
	d.file = file
	d.videoFramesLoaded = int64(videoFrames)
	d.audioFramesLoaded = int64(audioFrames)
}

// Source returns the SourceStats for a given key, creating one if needed.
func (d *DemoStats) Source(key string) *SourceStats {
	if val, ok := d.sources.Load(key); ok {
		return val.(*SourceStats)
	}
	s := &SourceStats{}
	actual, _ := d.sources.LoadOrStore(key, s)
	return actual.(*SourceStats)
}

// DebugSnapshot returns a snapshot of all demo stats for the debug endpoint.
func (d *DemoStats) DebugSnapshot() map[string]any {
	perSource := make(map[string]any)
	d.sources.Range(func(key, val any) bool {
		s := val.(*SourceStats)
		perSource[key.(string)] = map[string]any{
			"video_sent":      s.VideoSent.Load(),
			"audio_sent":      s.AudioSent.Load(),
			"loops_completed": s.LoopsCompleted.Load(),
		}
		return true
	})
	return map[string]any{
		"mode":                d.mode,
		"file":                d.file,
		"video_frames_loaded": d.videoFramesLoaded,
		"audio_frames_loaded": d.audioFramesLoaded,
		"per_source":          perSource,
	}
}

// SwitcherAPI is the subset of switcher.Switcher needed by the demo package.
type SwitcherAPI interface {
	SetLabel(ctx context.Context, key, label string) error
	Cut(ctx context.Context, source string) error
	SetPreview(ctx context.Context, source string) error
}

// Minimal valid H.264 baseline SPS/PPS for a 320×240 stream.
var (
	demoSPS = []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88}
	demoPPS = []byte{0x68, 0xCE, 0x38, 0x80}
)

// clipFiles maps camera indices to clip filenames (order matters for visual variety).
var clipFiles = []string{
	"tears_of_steel.ts",
	"sintel.ts",
	"bbb.ts",
	"elephants_dream.ts",
}

// StartSources takes pre-registered relays (one per source), sets labels
// and initial program/preview, then starts frame generation goroutines.
// If videoDir is non-empty, real MPEG-TS clips are demuxed from that directory
// and played back at real-time pace. Otherwise, synthetic frames are generated.
// The caller is responsible for registering the relays with Prism's
// server (so MoQ clients can subscribe) and with the switcher/mixer (via
// OnStreamRegistered). Returns a stop function that cancels all generators.
func StartSources(ctx context.Context, sw SwitcherAPI, relays []*distribution.Relay, stats *DemoStats, videoDir string) func() {
	ctx, cancel := context.WithCancel(ctx)

	n := len(relays)
	for i := range n {
		key := fmt.Sprintf("cam%d", i+1)
		label := fmt.Sprintf("Camera %d", i+1)
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

	if videoDir != "" {
		startFileBasedSources(ctx, relays, stats, videoDir)
	} else {
		// Record synthetic mode info.
		if stats != nil {
			stats.SetFileInfo("synthetic", "", 0, 0)
		}
		for i := range n {
			go generateFrames(ctx, relays[i], fmt.Sprintf("cam%d", i+1), stats)
		}
	}

	slog.Info("demo: started sources", "count", n, "videoDir", videoDir)
	return cancel
}

// startFileBasedSources demuxes clips from videoDir and starts real-time playback.
func startFileBasedSources(ctx context.Context, relays []*distribution.Relay, stats *DemoStats, videoDir string) {
	for i, relay := range relays {
		key := fmt.Sprintf("cam%d", i+1)
		filename := clipFiles[i%len(clipFiles)]
		path := filepath.Join(videoDir, filename)

		result, err := demuxTSFile(path)
		if err != nil {
			slog.Error("demo: failed to demux clip, falling back to synthetic", "key", key, "path", path, "err", err)
			go generateFrames(ctx, relay, key, stats)
			continue
		}

		// Set VideoInfo on relay from real SPS data, including avcC
		// for the MoQ catalog so the browser can configure the decoder
		// before the first keyframe arrives.
		if len(result.Video) > 0 {
			for _, vf := range result.Video {
				if vf.IsKeyframe && len(vf.SPS) > 0 {
					codecStr, width, height := parseSPS(vf.SPS)
					avcC := moq.BuildAVCDecoderConfig(vf.SPS, vf.PPS)
					relay.SetVideoInfo(distribution.VideoInfo{
						Codec:         codecStr,
						Width:         width,
						Height:        height,
						DecoderConfig: avcC,
					})
					slog.Info("demo: set video info from SPS", "key", key, "codec", codecStr, "width", width, "height", height)
					break
				}
			}
		}

		if stats != nil {
			stats.SetFileInfo("real_video", filename, len(result.Video), len(result.Audio))
		}

		slog.Info("demo: demuxed clip", "key", key, "file", filename,
			"video_frames", len(result.Video), "audio_frames", len(result.Audio))
		go generateFramesFromFile(ctx, relay, result.Video, result.Audio, key, stats)
	}
}

// generateFrames pumps synthetic video+audio frames into a relay at ~30fps.
// Every 30th frame is a keyframe (1 per second). PTS uses 90kHz clock.
func generateFrames(ctx context.Context, relay *distribution.Relay, key string, stats *DemoStats) {
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()

	var frameNum int64
	var groupID uint32 = 1   // start at 1; Prism uses 0 as "not damaged" sentinel
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
				if frameNum > 0 {
					groupID++
				}
				relay.BroadcastVideo(&media.VideoFrame{
					PTS:        pts,
					DTS:        pts,
					IsKeyframe: true,
					GroupID:    groupID,
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
					GroupID:    groupID,
					WireData:   []byte{0x00, 0x00, 0x00, 0x03, 0x41, 0x9A, 0x24},
					Codec:      "h264",
				})
			}
			if stats != nil {
				stats.Source(key).VideoSent.Add(1)
			}

			relay.BroadcastAudio(&media.AudioFrame{
				PTS:        pts,
				Data:       []byte{0xDE, 0x04, 0x00, 0x26, 0x20, 0x54, 0xE5, 0x00},
				SampleRate: 48000,
				Channels:   2,
			})
			if stats != nil {
				stats.Source(key).AudioSent.Add(1)
			}

			frameNum++
		}
	}
}

// generateFramesFromFile plays back pre-demuxed frames at real-time pace.
// DTS deltas between consecutive frames control timing (DTS is monotonic
// in decode order, unlike PTS which may reorder due to B-frames).
// On loop wrap, a timestamp offset increases by the clip duration to keep
// timestamps monotonically increasing.
func generateFramesFromFile(ctx context.Context, relay *distribution.Relay, videoFrames []media.VideoFrame, audioFrames []media.AudioFrame, key string, stats *DemoStats) {
	if len(videoFrames) == 0 {
		slog.Warn("demo: no video frames to play", "key", key)
		return
	}

	// Compute clip duration from last - first DTS.
	clipDuration := videoFrames[len(videoFrames)-1].DTS - videoFrames[0].DTS
	if clipDuration <= 0 {
		clipDuration = 90000 // 1 second fallback
	}
	// Add one frame duration to avoid timestamp collision on loop boundary.
	clipDuration += 3750 // ~41ms at 90kHz (24fps frame)

	// Cap audio frames per loop to match video clip duration. Without this,
	// audio tracks longer than video (e.g. 15.04s vs 14.46s in elephants_dream.ts)
	// accumulate excess audio each loop, bloating client ring buffers.
	// Audio PTS in the TS file may start before video DTS, so PTS-based
	// truncation doesn't work — all audio PTS fall within the video DTS range.
	// Instead, cap by frame count: clipDuration(ticks) * sampleRate / (samplesPerFrame * tickRate).
	maxAudioPerLoop := len(audioFrames) // default: no cap
	if len(audioFrames) > 0 {
		sampleRate := int64(audioFrames[0].SampleRate)
		if sampleRate <= 0 {
			sampleRate = 48000
		}
		maxAudioPerLoop = int(clipDuration * sampleRate / (1024 * 90000))
		if maxAudioPerLoop > len(audioFrames) {
			maxAudioPerLoop = len(audioFrames)
		}
	}

	var (
		vidIdx            int
		audIdx            int
		audioSentThisLoop int
		tsOffset          int64
		groupID           uint32 = 1 // start at 1; Prism uses 0 as "not damaged" sentinel
		startTime                = time.Now()
		baseDTS                  = videoFrames[0].DTS
	)

	for {
		if ctx.Err() != nil {
			slog.Debug("demo: file source stopped", "key", key)
			return
		}

		// Use DTS for timing — it's monotonic in decode order.
		relDTS := videoFrames[vidIdx].DTS - baseDTS

		// Wait until wall-clock time matches the frame's decode time.
		elapsed := time.Since(startTime)
		targetTime := time.Duration(relDTS) * time.Second / 90000
		if targetTime > elapsed {
			sleepCtx(ctx, targetTime-elapsed)
			if ctx.Err() != nil {
				return
			}
		}

		// Send any audio frames with PTS <= current video frame's DTS,
		// up to the per-loop audio cap.
		for audIdx < len(audioFrames) && audioSentThisLoop < maxAudioPerLoop && audioFrames[audIdx].PTS <= videoFrames[vidIdx].DTS {
			af := audioFrames[audIdx]
			af.PTS = af.PTS - baseDTS + tsOffset
			relay.BroadcastAudio(&af)
			if stats != nil {
				stats.Source(key).AudioSent.Add(1)
			}
			audIdx++
			audioSentThisLoop++
		}

		// Send video frame with adjusted timestamps.
		vf := videoFrames[vidIdx]
		vf.PTS = vf.PTS - baseDTS + tsOffset
		vf.DTS = vf.DTS - baseDTS + tsOffset
		if vf.IsKeyframe && vidIdx > 0 {
			groupID++
		}
		vf.GroupID = groupID
		relay.BroadcastVideo(&vf)
		if stats != nil {
			stats.Source(key).VideoSent.Add(1)
		}

		vidIdx++

		// Loop wrap: reset indices, bump timestamp offset.
		if vidIdx >= len(videoFrames) {
			// Send remaining audio frames up to the per-loop cap.
			for audIdx < len(audioFrames) && audioSentThisLoop < maxAudioPerLoop {
				af := audioFrames[audIdx]
				af.PTS = af.PTS - baseDTS + tsOffset
				relay.BroadcastAudio(&af)
				if stats != nil {
					stats.Source(key).AudioSent.Add(1)
				}
				audIdx++
				audioSentThisLoop++
			}

			vidIdx = 0
			audIdx = 0
			audioSentThisLoop = 0
			tsOffset += clipDuration
			startTime = time.Now()
			if stats != nil {
				stats.Source(key).LoopsCompleted.Add(1)
			}
			slog.Debug("demo: clip looped", "key", key, "tsOffset", tsOffset)
		}
	}
}

// sleepCtx sleeps for the given duration or until ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
