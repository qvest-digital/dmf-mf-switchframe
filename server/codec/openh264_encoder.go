//go:build cgo && openh264

package codec

/*
#include <wels/codec_api.h>
#include <wels/codec_def.h>
#include <wels/codec_app_def.h>
#include <string.h>
#include <stdlib.h>

// oh264enc wraps an OpenH264 encoder instance.
typedef struct {
	ISVCEncoder* enc;
	int width;
	int height;
} oh264enc_t;

static int oh264enc_open(oh264enc_t* h, int width, int height, int bitrate, float fps) {
	int rc = WelsCreateSVCEncoder(&h->enc);
	if (rc != 0 || h->enc == NULL) {
		return -1;
	}

	// Suppress verbose OpenH264 logging.
	int logLevel = WELS_LOG_QUIET;
	(*h->enc)->SetOption(h->enc, ENCODER_OPTION_TRACE_LEVEL, &logLevel);

	h->width = width;
	h->height = height;

	// Use SEncParamExt for full control over encoder settings.
	// Transition frames (dissolves, dips, fades) are the hardest content
	// for H.264 encoders: every frame differs from its neighbors, motion
	// estimation has reduced reference value, and scene-change detection
	// can incorrectly trigger mid-transition.
	SEncParamExt param;
	memset(&param, 0, sizeof(SEncParamExt));
	(*h->enc)->GetDefaultParams(h->enc, &param);

	param.iUsageType = CAMERA_VIDEO_REAL_TIME;
	param.iPicWidth = width;
	param.iPicHeight = height;
	param.iTargetBitrate = bitrate;
	param.iRCMode = RC_QUALITY_MODE;
	param.fMaxFrameRate = fps;
	param.iMinQp = 18;
	param.iMaxQp = 28;

	// Disable scene-change detection: the dissolve IS the content change,
	// and unplanned IDR insertions spike bitrate at the worst moment.
	param.bEnableSceneChangeDetect = false;

	// Never skip frames during transitions — skipping produces visible
	// stutter because the blend position jumps non-linearly.
	param.bEnableFrameSkip = false;

	// Adaptive quantization allocates more bits to perceptually important
	// regions, which helps preserve edges from both sources during blending.
	param.bEnableAdaptiveQuant = true;

	// Disable denoise — adds latency with no benefit for synthetic blend content.
	param.bEnableDenoise = false;

	// Single spatial layer at full resolution.
	param.iSpatialLayerNum = 1;
	param.sSpatialLayers[0].iVideoWidth = width;
	param.sSpatialLayers[0].iVideoHeight = height;
	param.sSpatialLayers[0].fFrameRate = fps;
	param.sSpatialLayers[0].iSpatialBitrate = bitrate;
	param.sSpatialLayers[0].iMaxSpatialBitrate = bitrate * 2;

	rc = (*h->enc)->InitializeExt(h->enc, &param);
	if (rc != 0) {
		WelsDestroySVCEncoder(h->enc);
		h->enc = NULL;
		return -2;
	}

	// Set video format to I420.
	int videoFormat = videoFormatI420;
	(*h->enc)->SetOption(h->enc, ENCODER_OPTION_DATAFORMAT, &videoFormat);

	return 0;
}

static void oh264enc_close(oh264enc_t* h) {
	if (h->enc) {
		(*h->enc)->Uninitialize(h->enc);
		WelsDestroySVCEncoder(h->enc);
		h->enc = NULL;
	}
}

// oh264enc_force_idr requests the next frame be encoded as IDR.
static int oh264enc_force_idr(oh264enc_t* h) {
	return (*h->enc)->ForceIntraFrame(h->enc, 1);
}

// oh264enc_encode encodes one YUV420 frame.
// yuv_data points to packed planar YUV420 (Y: w*h, U: w/2*h/2, V: w/2*h/2).
// On success, out_buf/out_len contain the Annex B bitstream, and is_idr is set.
// The caller must free out_buf with free().
// Returns 0 on success, 1 if frame was skipped, negative on error.
static int oh264enc_encode(oh264enc_t* h, unsigned char* yuv_data,
                           unsigned char** out_buf, int* out_len, int* is_idr) {
	SSourcePicture pic;
	memset(&pic, 0, sizeof(SSourcePicture));
	pic.iColorFormat = videoFormatI420;
	pic.iPicWidth = h->width;
	pic.iPicHeight = h->height;
	pic.iStride[0] = h->width;
	pic.iStride[1] = h->width >> 1;
	pic.iStride[2] = h->width >> 1;
	pic.pData[0] = yuv_data;
	pic.pData[1] = yuv_data + h->width * h->height;
	pic.pData[2] = pic.pData[1] + (h->width >> 1) * (h->height >> 1);

	SFrameBSInfo info;
	memset(&info, 0, sizeof(SFrameBSInfo));

	int rc = (*h->enc)->EncodeFrame(h->enc, &pic, &info);
	if (rc != cmResultSuccess) {
		return -1;
	}

	if (info.eFrameType == videoFrameTypeSkip) {
		*out_buf = NULL;
		*out_len = 0;
		*is_idr = 0;
		return 1; // skipped
	}

	*is_idr = (info.eFrameType == videoFrameTypeIDR) ? 1 : 0;

	// Collect all NALUs from all layers into a single buffer.
	int total = 0;
	for (int i = 0; i < info.iLayerNum; i++) {
		SLayerBSInfo* layer = &info.sLayerInfo[i];
		for (int j = 0; j < layer->iNalCount; j++) {
			total += layer->pNalLengthInByte[j];
		}
	}

	unsigned char* buf = (unsigned char*)malloc(total);
	if (!buf) {
		return -2;
	}

	int offset = 0;
	for (int i = 0; i < info.iLayerNum; i++) {
		SLayerBSInfo* layer = &info.sLayerInfo[i];
		int layer_size = 0;
		for (int j = 0; j < layer->iNalCount; j++) {
			layer_size += layer->pNalLengthInByte[j];
		}
		memcpy(buf + offset, layer->pBsBuf, layer_size);
		offset += layer_size;
	}

	*out_buf = buf;
	*out_len = total;
	return 0;
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/zsiec/switchframe/server/transition"
)

// Compile-time check that OpenH264Encoder implements transition.VideoEncoder.
var _ transition.VideoEncoder = (*OpenH264Encoder)(nil)

// OpenH264Encoder wraps the OpenH264 encoder and implements transition.VideoEncoder.
// It encodes packed YUV420 planar frames to Annex B H.264 bitstream.
type OpenH264Encoder struct {
	handle C.oh264enc_t
	closed bool
}

// NewOpenH264Encoder creates a new OpenH264 encoder with the given parameters.
func NewOpenH264Encoder(width, height, bitrate int, fps float32) (*OpenH264Encoder, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d", width, height)
	}
	if bitrate <= 0 {
		return nil, fmt.Errorf("invalid bitrate: %d", bitrate)
	}
	if fps <= 0 {
		return nil, fmt.Errorf("invalid fps: %f", fps)
	}

	e := &OpenH264Encoder{}
	rc := C.oh264enc_open(&e.handle, C.int(width), C.int(height), C.int(bitrate), C.float(fps))
	if rc != 0 {
		return nil, fmt.Errorf("failed to create OpenH264 encoder: code %d", int(rc))
	}
	return e, nil
}

// Encode encodes a packed YUV420 planar frame to Annex B H.264 data.
// If forceIDR is true, the encoder forces an IDR keyframe.
// Returns the encoded bitstream, whether the frame is a keyframe, and any error.
func (e *OpenH264Encoder) Encode(yuv []byte, forceIDR bool) ([]byte, bool, error) {
	if e.closed {
		return nil, false, fmt.Errorf("encoder is closed")
	}

	w := int(e.handle.width)
	h := int(e.handle.height)
	expected := w * h * 3 / 2
	if len(yuv) != expected {
		return nil, false, fmt.Errorf("YUV buffer must be %d bytes (%dx%d*3/2), got %d",
			expected, w, h, len(yuv))
	}

	if forceIDR {
		C.oh264enc_force_idr(&e.handle)
	}

	var outBuf *C.uchar
	var outLen C.int
	var isIDR C.int

	rc := C.oh264enc_encode(
		&e.handle,
		(*C.uchar)(unsafe.Pointer(&yuv[0])),
		&outBuf, &outLen, &isIDR,
	)
	if rc < 0 {
		return nil, false, fmt.Errorf("OpenH264 encode error: code %d", int(rc))
	}
	if rc == 1 {
		// Frame was skipped by encoder rate control.
		return nil, false, fmt.Errorf("frame skipped by encoder")
	}

	n := int(outLen)
	if n == 0 || outBuf == nil {
		return nil, false, fmt.Errorf("encoder produced no output")
	}

	// Copy from C-allocated buffer to Go slice, then free.
	result := C.GoBytes(unsafe.Pointer(outBuf), outLen)
	C.free(unsafe.Pointer(outBuf))

	return result, isIDR != 0, nil
}

// Close releases the encoder resources. Safe to call multiple times.
func (e *OpenH264Encoder) Close() {
	if !e.closed {
		C.oh264enc_close(&e.handle)
		e.closed = true
	}
}
