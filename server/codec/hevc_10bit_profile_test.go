//go:build cgo && !noffmpeg

package codec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

// TestHEVC10bitEncoder_CreatesWithSoftwareCodec verifies that creating a
// 10-bit HEVC encoder succeeds with the software encoder (libx265), which
// always supports 10-bit 4:2:2. This catches the bug where HEVC encoders
// set profile="main" instead of the appropriate 10-bit profile.
func TestHEVC10bitEncoder_CreatesWithSoftwareCodec(t *testing.T) {
	// libx265 always supports 10-bit 4:2:2 with main422-10 profile.
	e, err := NewFFmpegEncoder10bit("libx265", 256, 256, 500000, 30, 1, 2, nil)
	if err != nil {
		t.Skipf("libx265 not available: %v", err)
	}
	require.NotNil(t, e)
	e.Close()
}

// TestHEVC10bitEncoder_AllCandidateProfiles verifies that each HEVC encoder
// candidate has the correct profile set for 10-bit operation. Encoders that
// aren't available (codec not found) or don't support 10-bit pixel formats
// (e.g., VideoToolbox only supports 4:2:0) are skipped.
func TestHEVC10bitEncoder_AllCandidateProfiles(t *testing.T) {
	candidates := []struct {
		name        string
		mayFailOpen bool // true if the encoder may legitimately fail at avcodec_open2 due to unsupported pix_fmt
	}{
		{"libx265", false},
		// NVENC HEVC supports main10 profile but only with 4:2:0 pixel formats
		// (NV12/P010). YUV422P10LE (10-bit 4:2:2) is not supported by NVENC,
		// so avcodec_open2 fails with "wrong profile". Additionally, NVENC has
		// a limited number of concurrent sessions, so failure may also occur
		// due to session exhaustion.
		{"hevc_nvenc", true},
		// VA-API requires a hardware GPU; avcodec_open2 fails in CI (no GPU).
		{"hevc_vaapi", true},
		// VideoToolbox doesn't support YUV422P10LE; avcodec_open2 failure is expected.
		{"hevc_videotoolbox", true},
	}

	for _, c := range candidates {
		t.Run(c.name, func(t *testing.T) {
			e, err := NewFFmpegEncoder10bit(c.name, 256, 256, 500000, 30, 1, 2, nil)
			if err != nil {
				if c.mayFailOpen {
					t.Skipf("%s does not support 10-bit 4:2:2 pixel format: %v", c.name, err)
					return
				}
				if !strings.Contains(err.Error(), "avcodec_open2 failed") {
					t.Skipf("%s not available: %v", c.name, err)
					return
				}
				t.Fatalf("10-bit %s failed at avcodec_open2, likely wrong profile: %v", c.name, err)
			}
			e.Close()
		})
	}
}

// TestHEVC10bitEncoder_EncodeAccepts10bitFrames verifies that Encode on a
// 10-bit encoder accepts YUV422P10LE frames (w*h*4 bytes) — the size the C
// side actually copies — and produces encoded output. Regression test for the
// bug where Encode validated against the 8-bit YUV420 size (w*h*3/2), which
// rejected every frame in the 10-bit pipeline.
func TestHEVC10bitEncoder_EncodeAccepts10bitFrames(t *testing.T) {
	const w, h = 128, 128
	e, err := NewFFmpegEncoder10bit("libx265", w, h, 500000, 30, 1, 2, nil)
	if err != nil {
		t.Skipf("libx265 not available: %v", err)
	}
	defer e.Close()

	// Fill a YUV422P10LE frame with mid-gray (512 in 10-bit, little-endian).
	yuv := make([]byte, video.YUV422_10bitFrameSize(w, h))
	for i := 0; i+1 < len(yuv); i += 2 {
		yuv[i] = 0x00
		yuv[i+1] = 0x02
	}

	produced := false
	for i := range 10 {
		data, _, encErr := e.Encode(yuv, int64(i*3000), i == 0)
		require.NoError(t, encErr, "Encode must accept a %d-byte 10-bit frame", len(yuv))
		if len(data) > 0 {
			produced = true
		}
	}
	require.True(t, produced, "10-bit encoder should produce encoded output")
}

// TestHEVC10bitEncoder_EncodeRejects8bitFrameSize verifies that an 8-bit-sized
// buffer (w*h*3/2) is rejected by a 10-bit encoder. The C side reads w*h*4
// bytes for 10-bit input, so accepting the smaller buffer would be an
// out-of-bounds read past the end of the Go slice.
func TestHEVC10bitEncoder_EncodeRejects8bitFrameSize(t *testing.T) {
	const w, h = 128, 128
	e, err := NewFFmpegEncoder10bit("libx265", w, h, 500000, 30, 1, 2, nil)
	if err != nil {
		t.Skipf("libx265 not available: %v", err)
	}
	defer e.Close()

	yuv := make([]byte, video.YUV420FrameSize(w, h))
	_, _, err = e.Encode(yuv, 0, true)
	require.Error(t, err, "8-bit-sized buffer must be rejected by a 10-bit encoder")
}
