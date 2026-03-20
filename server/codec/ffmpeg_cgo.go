//go:build cgo && !noffmpeg

package codec

// ffmpeg_cgo.go provides the cgo link directives for FFmpeg libraries.
// This is separated into its own file so the linker flags are specified once,
// avoiding duplicate library warnings.

/*
#cgo pkg-config: libavcodec libavutil libavformat libswscale libswresample
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libavutil/opt.h>
#include <libavutil/log.h>
#include <libavformat/avformat.h>
#include <libswscale/swscale.h>
#include <libswresample/swresample.h>
*/
import "C"

import "sync"

// ffmpegLogOnce ensures av_log_set_level is called exactly once, preventing
// a data race when multiple encoders/decoders initialize concurrently.
var ffmpegLogOnce sync.Once

// initFFmpegLogLevel sets the FFmpeg log level to AV_LOG_FATAL. This is
// called from both encoder and decoder init paths via sync.Once.
func initFFmpegLogLevel() {
	ffmpegLogOnce.Do(func() {
		C.av_log_set_level(C.AV_LOG_FATAL)
	})
}

// FFmpegOpenMu serializes all avcodec_open2 and avformat_open_input calls.
// NVENC initializes the CUDA runtime inside avcodec_open2. When the SRT
// decoder calls avformat_open_input (which probes codecs) concurrently with
// an NVENC encoder open, the CUDA driver crashes with SIGSEGV. This mutex
// ensures only one FFmpeg codec/format initialization runs at a time.
// Once open, encode/decode operations are thread-safe per-context.
var FFmpegOpenMu sync.Mutex
