//go:build cgo && !noffmpeg

package codec

// ffmpeg_cgo.go provides the cgo link directives for FFmpeg libavcodec/libavutil.
// This is separated into its own file so the linker flags are specified once,
// avoiding duplicate library warnings.

/*
#cgo pkg-config: libavcodec libavutil
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libavutil/opt.h>
*/
import "C"
