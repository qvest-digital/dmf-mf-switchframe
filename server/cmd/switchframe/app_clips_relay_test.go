//go:build cgo && !noffmpeg

package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/codec"
)

// spsDimensions encodes one grey frame and returns the size the stream's
// SPS declares.
func spsDimensions(t *testing.T, enc clip.VideoEncoder, w, h int) (int, int) {
	t.Helper()
	yuv := make([]byte, w*h*3/2)
	for i := range yuv {
		yuv[i] = 128
	}
	out, kf, err := enc.Encode(yuv, 0, true)
	require.NoError(t, err)
	require.True(t, kf)
	for _, nalu := range codec.ExtractNALUs(codec.AnnexBToAVC1(out)) {
		if len(nalu) > 0 && nalu[0]&0x1F == 7 {
			return clip.ParseSPSDimensions(nalu)
		}
	}
	t.Fatal("no SPS in the first frame")
	return 0, 0
}

// A clip player's browser relay only feeds the UI's tiles: it is encoded
// at the preview size, the size the GPU preview proxy uses, not at the
// clip's 1080p, and a smaller clip is not scaled up.
func TestClipRelayEncoderFactory_EncodesAtPreviewSize(t *testing.T) {
	pw, ph := parsePreviewResolution("480p")
	factory := clipRelayEncoderFactory(pw, ph, 500000)

	enc, err := factory(1920, 1080, 30, 1)
	require.NoError(t, err)
	w, h := spsDimensions(t, enc, 1920, 1080)
	enc.Close()
	require.Equal(t, pw, w)
	require.Equal(t, ph, h)

	enc, err = factory(640, 360, 30, 1)
	require.NoError(t, err)
	w, h = spsDimensions(t, enc, 640, 360)
	enc.Close()
	require.Equal(t, 640, w)
	require.Equal(t, 360, h)

	// A frame that is not 8-bit 4:2:0 at the factory's size is refused
	// rather than scaled as if it were.
	enc, err = factory(1920, 1080, 30, 1)
	require.NoError(t, err)
	defer enc.Close()
	_, _, err = enc.Encode(make([]byte, 1920*1080*2), 0, true)
	require.Error(t, err)
}
