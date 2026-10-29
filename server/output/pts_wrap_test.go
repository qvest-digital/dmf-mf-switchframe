package output

import (
	"bytes"
	"context"
	"testing"

	"github.com/asticode/go-astits"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/mpegts"
)

func TestUnwrapPTS(t *testing.T) {
	const p = ptsPeriod
	for _, tc := range []struct{ pts, ref, want int64 }{
		{1800, 0, 1800},
		{0, 1800, 0},
		{900, p - 900, p + 900},         // video wrapped
		{p + 900, p - 900, p + 900},     // audio did not
		{p - 900, 900, -900},            // a frame from before a wrap
		{2*p + 900, p + 100, p + 900},   // audio far past the period
		{p - 100, 2*p + 100, 2*p - 100}, // video behind audio across the wrap
	} {
		require.Equal(t, tc.want, unwrapPTS(tc.pts, tc.ref), "pts %d ref %d", tc.pts, tc.ref)
	}
}

// demuxPTS returns the PTS of every video and audio PES in ts.
func demuxPTS(t *testing.T, ts []byte) (video, audio []int64) {
	t.Helper()
	dmx := astits.NewDemuxer(context.Background(), bytes.NewReader(ts))
	for {
		d, err := dmx.NextData()
		if err != nil {
			break
		}
		if d == nil || d.PES == nil || d.PES.Header == nil || d.PES.Header.OptionalHeader == nil || d.PES.Header.OptionalHeader.PTS == nil {
			continue
		}
		switch d.PID {
		case mpegts.VideoPID:
			video = append(video, d.PES.Header.OptionalHeader.PTS.Base)
		case mpegts.AudioPID:
			audio = append(audio, d.PES.Header.OptionalHeader.PTS.Base)
		}
	}
	return video, audio
}

// wrapFrames feeds 50 fps video whose 33-bit PTS wraps after 25 frames and
// audio whose PTS counts on past 2^33, as the reference clock and the mixer
// produce them.
func wrapFrames(t *testing.T, writeVideo func(int64, bool) error, writeAudio func(int64) error) {
	t.Helper()
	base := ptsPeriod - 25*1800
	for i := int64(0); i < 50; i++ {
		require.NoError(t, writeVideo((base+i*1800)&(ptsPeriod-1), i == 0))
		if i < 40 {
			require.NoError(t, writeAudio(base+i*1920))
		}
	}
}

// SRT and recordings carry MPEG-TS, whose PTS is 33 bits: the muxer output
// continues across the upstream wrap, and wraps itself without a step.
func TestTSMuxer_PTSContinuesAcrossWrap(t *testing.T) {
	for _, tc := range []struct {
		name  string
		epoch int64
	}{
		{"upstream wraps", 90000},
		{"output wraps", ptsPeriod - 10*1800},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewTSMuxer()
			m.muxerEpoch = tc.epoch
			var out []byte
			m.SetOutput(func(b []byte) { out = append(out, b...) })
			wrapFrames(t,
				func(pts int64, key bool) error {
					if key {
						return m.WriteVideo(makeKeyframe(pts))
					}
					return m.WriteVideo(makePFrame(pts))
				},
				func(pts int64) error { return m.WriteAudio(makeAudioFrame(pts)) })

			video, audio := demuxPTS(t, out)
			require.Len(t, video, 50)
			require.Len(t, audio, 40)
			for i, pts := range video {
				require.Equal(t, (tc.epoch+int64(i)*1800)&(ptsPeriod-1), pts, "video PES %d", i)
			}
			for i, pts := range audio {
				require.Equal(t, (tc.epoch+int64(i)*1920)&(ptsPeriod-1), pts, "audio PES %d", i)
			}
		})
	}
}
