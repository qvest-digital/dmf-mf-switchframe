package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/demo"
	"github.com/zsiec/switchframe/server/preview"
	"github.com/zsiec/switchframe/server/switcher"
)

// startDemoWithPreview wires demo sources through the preview proxy path:
//
//	demo.generateFrames → H.264 → internal relay → decode viewer → YUV
//	                                                              → IngestRawVideo (switcher)
//	                                                              → preview encoder → browser relay
//
// Instead of the standard path where onStreamRegistered creates a sourceViewer
// (which uses the always-decode architecture), this path mirrors the SRT/MXL
// raw ingest pattern: the switcher receives decoded YUV directly, and the
// browser relay gets a low-bitrate preview encode.
func (a *App) startDemoWithPreview(ctx context.Context, demoStats *demo.Stats, nCams int) func() {
	pf := a.sw.PipelineFormat()
	pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)

	// Initialize the filter set so onStreamRegistered skips these keys.
	if a.previewDemoKeys == nil {
		a.previewDemoKeys = make(map[string]bool, nCams)
	}

	// Phase 1: Register raw sources and create browser-facing relays.
	browserRelays := make([]*distribution.Relay, nCams)
	var previewEncoders []*preview.Encoder

	for i := range nCams {
		key := fmt.Sprintf("cam%d", i+1)

		// Register in filter BEFORE creating relay (RegisterStream triggers onStreamRegistered).
		a.previewDemoKeys[key] = true

		// Register with switcher as raw source (SRT-style, no sourceViewer).
		a.sw.RegisterSRTSource(key)
		a.mixer.AddChannel(key)
		_ = a.mixer.SetAFV(key, true)

		// Create browser-facing relay (filtered by previewDemoKeys in onStreamRegistered).
		browserRelays[i] = a.server.RegisterStream(key)

		// Create preview encoder for this source.
		pe, err := preview.NewEncoder(preview.Config{
			SourceKey: key,
			Width:     pw,
			Height:    ph,
			Bitrate:   a.cfg.PreviewBitrate,
			FPSNum:    pf.FPSNum,
			FPSDen:    pf.FPSDen,
			Relay:     browserRelays[i],
		})
		if err != nil {
			slog.Error("demo preview: encoder failed", "key", key, "error", err)
			continue
		}
		previewEncoders = append(previewEncoders, pe)
	}

	// Phase 2: Create internal relays for demo H.264 generation.
	// The _demo: prefix is filtered by onStreamRegistered.
	internalRelays := make([]*distribution.Relay, nCams)
	for i := range nCams {
		internalKey := fmt.Sprintf("_demo:cam%d", i+1)
		internalRelays[i] = a.server.RegisterStream(internalKey)
		if a.cfg.DemoVideoDir == "" {
			internalRelays[i].SetVideoInfo(distribution.VideoInfo{
				Codec:  "avc1.42C01E",
				Width:  320,
				Height: 240,
			})
		}
	}

	// Phase 3: Start demo generators broadcasting to internal relays.
	stopDemo := demo.StartSources(ctx, a.sw, internalRelays, demoStats, a.cfg.DemoVideoDir, pf.FrameDuration())

	// Phase 4: Start per-source decode goroutines that fan out to
	// switcher pipeline (IngestRawVideo) and preview encoder.
	decodeCtx, decodeCancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	framePool := a.sw.GetFramePool()

	for i := range nCams {
		if i >= len(previewEncoders) {
			continue
		}
		key := fmt.Sprintf("cam%d", i+1)
		pe := previewEncoders[i]
		internalRelay := internalRelays[i]

		wg.Add(1)
		go func() {
			defer wg.Done()
			a.demoDecodeLoop(decodeCtx, key, internalRelay, pe, framePool)
		}()
	}

	// Copy video info from first internal relay to program relay.
	if len(internalRelays) > 0 {
		a.programRelay.SetVideoInfo(internalRelays[0].VideoInfo())
	}

	return func() {
		stopDemo()
		decodeCancel()
		wg.Wait()
		for _, pe := range previewEncoders {
			pe.Stop()
		}
	}
}

// demoDecodeLoop reads H.264 from an internal relay, decodes to YUV,
// and fans out to switcher pipeline (IngestRawVideo) and preview encoder.
func (a *App) demoDecodeLoop(ctx context.Context, key string, relay *distribution.Relay, pe *preview.Encoder, pool *switcher.FramePool) {
	// Create a viewer on the internal relay to receive H.264 frames.
	ch := make(chan *media.VideoFrame, 4)
	viewer := &demoDecodeViewer{id: "demo-decode:" + key, ch: ch}
	relay.AddViewer(viewer)
	defer relay.RemoveViewer(viewer.ID())

	dec, err := codec.NewVideoDecoderSingleThread()
	if err != nil {
		slog.Error("demo preview: decoder failed", "key", key, "error", err)
		return
	}
	defer dec.Close()

	var annexBBuf, prependBuf []byte

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-ch:
			if !ok {
				return
			}

			// AVC1 → AnnexB for the decoder.
			annexBBuf = codec.AVC1ToAnnexBInto(frame.WireData, annexBBuf)
			if frame.IsKeyframe && frame.SPS != nil && frame.PPS != nil {
				prependBuf = codec.PrependSPSPPSInto(frame.SPS, frame.PPS, annexBBuf, prependBuf)
				annexBBuf = prependBuf
			}

			yuv, w, h, err := dec.Decode(annexBBuf)
			if err != nil || yuv == nil {
				continue
			}

			// Feed switcher pipeline via IngestRawVideo.
			poolBuf := pool.Acquire()
			copy(poolBuf, yuv)
			pf := &switcher.ProcessingFrame{
				YUV:    poolBuf,
				Width:  w,
				Height: h,
				PTS:    frame.PTS,
				DTS:    frame.PTS,
				Codec:  "h264",
			}
			pf.SetPool(pool)
			a.sw.IngestRawVideo(key, pf)

			// Feed preview encoder (deep-copies internally).
			pe.Send(yuv, w, h, frame.PTS)
		}
	}
}

// demoDecodeViewer implements distribution.Viewer for the decode goroutine.
// It receives H.264 frames from the internal demo relay and forwards them
// to the decode loop via a buffered channel.
type demoDecodeViewer struct {
	id string
	ch chan *media.VideoFrame
}

func (v *demoDecodeViewer) ID() string { return v.id }

func (v *demoDecodeViewer) SendVideo(frame *media.VideoFrame) {
	// Newest-wins drop policy: if the channel is full, drain oldest and send.
	select {
	case v.ch <- frame:
		return
	default:
	}
	select {
	case <-v.ch:
	default:
	}
	select {
	case v.ch <- frame:
	default:
	}
}

func (v *demoDecodeViewer) SendAudio(_ *media.AudioFrame)    {}
func (v *demoDecodeViewer) SendCaptions(_ *ccx.CaptionFrame) {}
func (v *demoDecodeViewer) Stats() distribution.ViewerStats  { return distribution.ViewerStats{} }
