//go:build cgo && (cuda || darwin)

package gpu

import "github.com/zsiec/switchframe/server/internal/video"

// cpuFrameSize returns the CPU-side planar buffer size for the given format.
// NV12 -> YUV420p (w*h*3/2). P210 -> YUV422P10LE (w*h*4).
func cpuFrameSize(w, h int, format GPUFrameFormat) int {
	switch format {
	case GPUFormatP210:
		return video.YUV422_10bitFrameSize(w, h)
	default:
		return video.YUV420FrameSize(w, h)
	}
}
