//go:build cgo && !noffmpeg

package codec

/*
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <string.h>

typedef struct {
	int video_codec_id;
	int audio_codec_id;
	int width;
	int height;
	int has_video;
	int has_audio;
} ff_probe_result_t;

// ff_probe_file opens a media file with avformat and extracts stream info.
// Returns 0 on success, -1 on error.
static int ff_probe_file(const char* path, ff_probe_result_t* result) {
	memset(result, 0, sizeof(ff_probe_result_t));

	AVFormatContext* fmt_ctx = NULL;
	int ret = avformat_open_input(&fmt_ctx, path, NULL, NULL);
	if (ret < 0) {
		return -1;
	}

	ret = avformat_find_stream_info(fmt_ctx, NULL);
	if (ret < 0) {
		avformat_close_input(&fmt_ctx);
		return -1;
	}

	if (fmt_ctx->nb_streams == 0) {
		avformat_close_input(&fmt_ctx);
		return -1;
	}

	int video_idx = av_find_best_stream(fmt_ctx, AVMEDIA_TYPE_VIDEO, -1, -1, NULL, 0);
	if (video_idx >= 0) {
		AVCodecParameters* par = fmt_ctx->streams[video_idx]->codecpar;
		// Only count as valid video if dimensions are known.
		// Empty or truncated files may probe as h264 with w=0, h=0.
		if (par->width > 0 && par->height > 0) {
			result->has_video = 1;
			result->video_codec_id = (int)par->codec_id;
			result->width = par->width;
			result->height = par->height;
		}
	}

	int audio_idx = av_find_best_stream(fmt_ctx, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0);
	if (audio_idx >= 0) {
		result->has_audio = 1;
		result->audio_codec_id = (int)fmt_ctx->streams[audio_idx]->codecpar->codec_id;
	}

	avformat_close_input(&fmt_ctx);
	return 0;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// FileProbeResult holds the result of probing a media file with avformat.
type FileProbeResult struct {
	VideoCodecID int
	AudioCodecID int
	Width        int
	Height       int
	HasVideo     bool
	HasAudio     bool
}

// ProbeFile opens a media file with FFmpeg's avformat and extracts stream
// information including codec IDs and dimensions.
func ProbeFile(path string) (*FileProbeResult, error) {
	initFFmpegLogLevel()

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var result C.ff_probe_result_t
	ret := C.ff_probe_file(cPath, &result)
	if ret < 0 {
		return nil, fmt.Errorf("failed to probe file: %s", path)
	}

	r := &FileProbeResult{
		VideoCodecID: int(result.video_codec_id),
		AudioCodecID: int(result.audio_codec_id),
		Width:        int(result.width),
		Height:       int(result.height),
		HasVideo:     result.has_video != 0,
		HasAudio:     result.has_audio != 0,
	}

	if !r.HasVideo {
		return nil, fmt.Errorf("no video stream found in: %s", path)
	}

	return r, nil
}

// IsH264 returns true if the probed video codec is H.264.
func (r *FileProbeResult) IsH264() bool {
	return r.VideoCodecID == 27 // AV_CODEC_ID_H264
}
