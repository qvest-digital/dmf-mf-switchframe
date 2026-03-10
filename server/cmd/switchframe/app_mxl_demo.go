package main

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/moq"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/mxl"
	"github.com/zsiec/switchframe/server/switcher"
)

// startMXLDemo creates synthetic raw YUV420p + PCM sources that exercise
// the MXL pipeline path (IngestRawVideo, IngestPCM) under --demo mode.
// Returns a stop function that halts all demo sources.
//
// This proves the raw media path works end-to-end: V210→YUV420p→switcher
// pipeline→encode→program relay (browser), and optionally the output sink
// path: RawVideoSink→YUV420p→V210.
func (a *App) startMXLDemo(ctx context.Context) func() {
	const (
		// 360 is divisible by 6 (V210 requirement), 240 is even (YUV420p requirement).
		width  = 360
		height = 240
	)

	pf := a.sw.PipelineFormat()
	fps := pf.FPS() // float64 for demo reader pacing

	names := []string{"raw1", "raw2"}

	// Per-source pattern selection: raw2 gets green screen for keying demo.
	patterns := []mxl.DemoPattern{mxl.PatternColorBars, mxl.PatternGreenScreen}
	labels := []string{"", "Green Screen"} // empty = use default key

	for i, name := range names {
		key := "mxl:" + name

		// Register as MXL source (uses IngestRawVideo path, not relay viewer).
		a.sw.RegisterMXLSource(key)
		a.mixer.AddChannel(key)
		_ = a.mixer.SetAFV(key, true)

		// Relay for browser viewing (H.264 encoded by Source orchestrator).
		relay := a.server.RegisterStream(key)

		// Create demo flow readers.
		videoReader := mxl.NewDemoVideoReaderWithPattern(width, height, fps, i, patterns[i])
		audioReader := mxl.NewDemoAudioReader(48000, 2)

		// Capture relay in closure for OnVideoInfo callback.
		sourceRelay := relay
		src := mxl.NewSource(mxl.SourceConfig{
			FlowName:            key,
			Width:               width,
			Height:              height,
			FPSNum:              pf.FPSNum,
			FPSDen:              pf.FPSDen,
			SampleRate:          48000,
			Channels:            2,
			Relay:               relay,
			EncoderFactory:      encoderFactory(),
			AudioEncoderFactory: audioEncoderFactoryForMXL(),
			OnVideoInfo: func(sps, pps []byte, w, h int) {
				// Set VideoInfo on the relay so browsers can init their decoder.
				avcC := moq.BuildAVCDecoderConfig(sps, pps)
				if avcC != nil {
					sourceRelay.SetVideoInfo(distribution.VideoInfo{
						Codec:         codec.ParseSPSCodecString(sps),
						Width:         w,
						Height:        h,
						DecoderConfig: avcC,
					})
					slog.Info("MXL demo: relay VideoInfo set", "key", key, "w", w, "h", h)
				}
			},
			OnRawVideo: func(sourceKey string, yuv []byte, w, h int, pts int64) {
				pf := &switcher.ProcessingFrame{
					YUV:    yuv,
					Width:  w,
					Height: h,
					PTS:    pts,
					DTS:    pts,
					Codec:  "h264",
				}
				a.sw.IngestRawVideo(sourceKey, pf)
			},
			OnRawAudio: func(sourceKey string, pcm []float32, pts int64, channels int) {
				a.mixer.IngestPCM(sourceKey, pcm, pts, channels)
			},
		})

		src.Start(ctx, videoReader, audioReader)
		a.mxlSources = append(a.mxlSources, src)

		// Set label if specified.
		if labels[i] != "" {
			_ = a.sw.SetLabel(ctx, key, labels[i])
		}

		slog.Info("MXL demo source started", "key", key, "pattern", patterns[i], "resolution", [2]int{width, height})
	}

	// Pre-configure chroma key on the green screen source (disabled by default).
	// The user enables it in the Keys tab to see keying in action.
	// Uses BT.709 green values matching the UI "Green Screen" preset.
	greenScreenKey := "mxl:" + names[1]
	a.keyProcessor.SetKey(greenScreenKey, graphics.KeyConfig{
		Type:          graphics.KeyTypeChroma,
		Enabled:       false,
		KeyColorY:     173,
		KeyColorCb:    42,
		KeyColorCr:    26,
		Similarity:    0.4,
		Smoothness:    0.1,
		SpillSuppress: 0.5,
	})
	slog.Info("MXL demo: chroma key pre-configured (enable in Keys tab)", "source", greenScreenKey)

	// Wire output sink logging. If an MXL output writer is already configured
	// (via --mxl-output), wrap it so frames flow through to the real writer.
	var sinkFrameCount atomic.Int64
	existingWriter := a.mxlOutput
	a.sw.SetRawVideoSink(switcher.RawVideoSink(func(pf *switcher.ProcessingFrame) {
		count := sinkFrameCount.Add(1)
		// Log every 150 frames (~5 seconds at 30fps).
		if count%150 == 1 {
			slog.Info("MXL output sink active",
				"frames", count,
				"width", pf.Width,
				"height", pf.Height,
				"pts", pf.PTS,
			)
		}
		// Forward to the real MXL writer if configured.
		if existingWriter != nil {
			existingWriter.Writer().WriteVideo(pf.YUV, pf.Width, pf.Height, pf.PTS)
		}
	}))

	// Periodic stats logger.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				slog.Info("MXL demo stats",
					"raw_sources", len(names),
					"output_sink_frames", sinkFrameCount.Load(),
				)
			}
		}
	}()

	return func() {
		for _, src := range a.mxlSources {
			src.Stop()
		}
		slog.Info("MXL demo sources stopped", "total_output_frames", sinkFrameCount.Load())
	}
}
