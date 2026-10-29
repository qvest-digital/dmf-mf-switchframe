package output

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/asticode/go-astits"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/internal/mpegts"
)

// tsPESPTS returns the PTS of every video and audio PES in ts.
func tsPESPTS(t *testing.T, ts []byte) (video, audio []int64) {
	t.Helper()
	dmx := astits.NewDemuxer(context.Background(), bytes.NewReader(ts))
	for {
		d, err := dmx.NextData()
		if err != nil {
			return video, audio
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
}

// recordingFrameAdapter keeps the PTS of every frame handed to it.
type recordingFrameAdapter struct {
	mu           sync.Mutex
	video, audio []int64
}

func (r *recordingFrameAdapter) ID() string                  { return "recording" }
func (r *recordingFrameAdapter) Start(context.Context) error { return nil }
func (r *recordingFrameAdapter) Close() error                { return nil }
func (r *recordingFrameAdapter) Status() AdapterStatus       { return AdapterStatus{State: StateActive} }
func (r *recordingFrameAdapter) WriteAudio(f *media.AudioFrame) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audio = append(r.audio, f.PTS)
	return nil
}
func (r *recordingFrameAdapter) WriteVideo(f *media.VideoFrame) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.video = append(r.video, f.PTS)
	return nil
}

func withFrameAdapter(m *Manager) *recordingFrameAdapter {
	fa := &recordingFrameAdapter{}
	m.frameAdapters.Store(&[]FrameAdapter{fa})
	return fa
}

// The TS muxer delays video by the lip-sync offset; an RTMP output gets the
// same delay, so both carry one A/V alignment. A video and an audio frame
// with the same upstream PTS land lipSync apart on both.
func TestManager_FrameAdaptersGetTheTSLipSyncDelay(t *testing.T) {
	const lipSync = int64(9000)
	m := NewManager()
	defer func() { _ = m.Close() }()
	m.LatchLipSync(lipSync)
	fa := withFrameAdapter(m)
	mux := NewTSMuxer()
	mux.SetLipSyncOffset90k(lipSync)
	var ts []byte
	mux.SetOutput(func(b []byte) { ts = append(ts, b...) })
	m.directMuxer.Store(mux)

	const pts = int64(900000)
	frame := makeKeyframe(pts)
	m.DirectWriteVideo(frame)
	m.DirectWriteAudio(makeAudioFrame(pts))
	require.Equal(t, pts, frame.PTS, "the caller's frame is shared and must stay as it was")

	video, audio := tsPESPTS(t, ts)
	require.Len(t, video, 1)
	require.Len(t, audio, 1)
	require.Equal(t, lipSync, video[0]-audio[0], "TS")
	require.Equal(t, []int64{pts + lipSync}, fa.video)
	require.Equal(t, []int64{pts}, fa.audio)
	require.Equal(t, lipSync, fa.video[0]-fa.audio[0], "frame adapter")
}

// With only an RTMP output there is no TS muxer; the offset still follows
// its source.
func TestManager_LipSyncFollowsSourceWithoutTSMuxer(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()
	m.SetLipSyncSource(func() int64 { return 4500 })
	fa := withFrameAdapter(m)

	m.DirectWriteVideo(makeKeyframe(900000))
	require.Equal(t, int64(4500), m.LipSyncOffset())
	require.Equal(t, []int64{904500}, fa.video)

	// A TS muxer created later starts with the same offset.
	require.NoError(t, m.StartRecording(RecorderConfig{Dir: t.TempDir()}))
	require.Equal(t, int64(4500), m.directMuxer.Load().LipSyncOffset90k())
}
