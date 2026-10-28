//go:build cgo && !noffmpeg

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// One decoder thread decodes a high-bitrate 1080p H.264 clip below real
// time on a server core, so a clip player opened single-threaded plays its
// clip slowed down. Each clip decoder must be frame-threaded.
func TestClipDecoderFactory_FrameThreaded(t *testing.T) {
	for _, is10bit := range []bool{false, true} {
		for _, codecName := range []string{"h264", "hevc"} {
			dec, err := clipDecoderFactory(is10bit)(codecName)
			require.NoError(t, err, "10-bit pipeline %v, %s", is10bit, codecName)
			threaded, ok := dec.(interface{ ThreadCount() int })
			require.True(t, ok, "%T reports no thread count", dec)
			require.Greater(t, threaded.ThreadCount(), 1, "10-bit pipeline %v, %s", is10bit, codecName)
			dec.Close()
		}
	}
}
