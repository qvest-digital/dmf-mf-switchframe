//go:build cgo && !noffmpeg

package codec

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

// ptsSteps are 90 kHz timestamps with uneven gaps, as a jittery source or a
// dropped frame produces. A packet stamped from a frame counter or from the
// next frame's PTS would not reproduce them.
var ptsSteps = []int64{0, 3000, 4500, 10500, 10501, 30000, 33003, 36003, 36004, 90000}

func ptsTestFrame(w, h int, i int) []byte {
	yuv := make([]byte, video.YUV420FrameSize(w, h))
	for j := range yuv {
		yuv[j] = byte(i*7 + j)
	}
	return yuv
}

// libx264 runs with zerolatency and no B-frames, so every send yields
// exactly one packet, and that packet carries the PTS of its own frame.
func TestFFmpegEncoder_EncodePacketsCarryFramePTS(t *testing.T) {
	const w, h = 320, 240
	enc, err := NewFFmpegEncoder("libx264", w, h, 500_000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	for i, pts := range ptsSteps {
		pkts, err := enc.EncodePackets(ptsTestFrame(w, h, i), pts, i == 0)
		require.NoError(t, err)
		require.Len(t, pkts, 1, "frame %d", i)
		require.NotEmpty(t, pkts[0].Data)
		require.Equal(t, pts, pkts[0].PTS, "frame %d PTS", i)
		require.Equal(t, pts, pkts[0].DTS, "frame %d DTS (no reordering)", i)
		if i == 0 {
			require.True(t, pkts[0].IsKeyframe)
		}
	}
}

// libvpx-vp9 holds frames for its lookahead, so the packet a send releases
// encodes an earlier frame. Each packet must still carry that earlier
// frame's PTS, in order, with none skipped.
func TestFFmpegEncoder_EncodePacketsDelayedEncoderKeepsFramePTS(t *testing.T) {
	const w, h = 160, 120
	enc, err := NewFFmpegEncoder("libvpx-vp9", w, h, 200_000, 30, 1, 2, nil)
	if err != nil {
		t.Skipf("libvpx-vp9 not available: %v", err)
	}
	defer enc.Close()

	var inputs []int64
	for i := 0; i < 60; i++ {
		inputs = append(inputs, ptsSteps[i%len(ptsSteps)]+int64(i/len(ptsSteps))*100_000)
	}

	var got []int64
	held := false
	for i, pts := range inputs {
		pkts, err := enc.EncodePackets(ptsTestFrame(w, h, i), pts, false)
		require.NoError(t, err)
		if len(got)+len(pkts) < i+1 {
			held = true
		}
		for _, p := range pkts {
			got = append(got, p.PTS)
		}
	}

	require.True(t, held, "libvpx-vp9 must hold frames for this test to exercise a delay")
	require.NotEmpty(t, got)
	require.Equal(t, inputs[:len(got)], got,
		"packet i carries frame i's PTS even though a later send released it")
}
