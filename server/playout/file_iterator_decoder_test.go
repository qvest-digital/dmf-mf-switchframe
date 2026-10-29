//go:build cgo && !noffmpeg

package playout

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A channel decodes on its tick, and one decoder thread decodes a
// high-bitrate 1080p H.264 clip below real time on a server core: a
// single-threaded decoder plays such a clip slowed down.
func TestDefaultDecoderFactory_FrameThreaded(t *testing.T) {
	dec, err := DefaultDecoderFactory()
	require.NoError(t, err)
	defer dec.Close()
	threaded, ok := dec.(interface{ ThreadCount() int })
	require.True(t, ok, "%T reports no thread count", dec)
	require.Greater(t, threaded.ThreadCount(), 1)
	// Two players and playout share the engine's cores.
	require.LessOrEqual(t, threaded.ThreadCount(), 4)
}
