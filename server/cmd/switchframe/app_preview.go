package main

import "github.com/zsiec/switchframe/server/preview"

// sourcePreviewConfig configures the CPU encoder of a source's browser
// relay under the preview proxy: the source scaled down to the preview
// size at the preview bitrate, as the GPU preview proxy encodes it. The
// relay only feeds the UI's tiles and monitors; at the source's own size
// it cost a 1080p encode per source on the engine and a 1080p decode per
// tile in every browser.
func (a *App) sourcePreviewConfig(key string, relay preview.Relay, fpsNum, fpsDen int) preview.Config {
	pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)
	return preview.Config{
		SourceKey:     key,
		Width:         pw,
		Height:        ph,
		Bitrate:       a.cfg.PreviewBitrate,
		FPSNum:        fpsNum,
		FPSDen:        fpsDen,
		Relay:         relay,
		FrameInterval: a.cfg.PreviewFrameInterval,
		ScaleDown:     true,
	}
}

// parsePreviewResolution converts a resolution string to width and height.
func parsePreviewResolution(res string) (int, int) {
	switch res {
	case "360p":
		return 640, 360
	case "480p":
		return 852, 480 // even-aligned (854 is odd, breaks NV12 UV plane math on GPU)
	case "720p":
		return 1280, 720
	default:
		return 852, 480
	}
}

// parseRelayResolution converts a resolution string to width and height.
// Returns (0, 0) for "source" meaning no scaling (use source resolution).
func parseRelayResolution(res string) (int, int) {
	switch res {
	case "source":
		return 0, 0
	case "360p":
		return 640, 360
	case "480p":
		return 852, 480
	case "720p":
		return 1280, 720
	default:
		return 1280, 720
	}
}
