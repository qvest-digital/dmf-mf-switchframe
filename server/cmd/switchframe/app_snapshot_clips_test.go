package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/output"
)

// writeTestClipTS writes a short H.264+AAC MPEG-TS file and returns its name.
func writeTestClipTS(t *testing.T, dir, name string) string {
	t.Helper()
	sps := []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88}
	pps := []byte{0x68, 0xCE, 0x38, 0x80}
	avc1 := func(nalu []byte) []byte {
		n := len(nalu)
		return append([]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}, nalu...)
	}
	idr := make([]byte, 64)
	idr[0] = 0x65
	slice := make([]byte, 32)
	slice[0] = 0x41

	muxer := output.NewTSMuxer()
	var ts []byte
	muxer.SetOutput(func(b []byte) { ts = append(ts, b...) })
	for i := range 60 {
		pts := int64(90000 + i*3000)
		f := &media.VideoFrame{PTS: pts, DTS: pts, WireData: avc1(slice)}
		if i == 0 {
			f = &media.VideoFrame{PTS: pts, DTS: pts, IsKeyframe: true, SPS: sps, PPS: pps, WireData: avc1(idr), Codec: "h264"}
		}
		require.NoError(t, muxer.WriteVideo(f))
		aac := make([]byte, 128)
		aac[0] = 0x01
		require.NoError(t, muxer.WriteAudio(&media.AudioFrame{
			PTS: pts, Data: codec.EnsureADTS(aac, 48000, 2), SampleRate: 48000, Channels: 2,
		}))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), ts, 0o644))
	return name
}

func newClipTestApp(t *testing.T) (*App, *clip.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)
	mgr := clip.NewManager(store, clip.ManagerConfig{})
	t.Cleanup(mgr.Close)
	return &App{clipMgr: mgr, clipStore: store}, store
}

func addClip(t *testing.T, store *clip.Store, id, name string) *clip.Clip {
	t.Helper()
	c := &clip.Clip{
		Name:     name,
		Filename: writeTestClipTS(t, store.Dir(), name+".ts"),
		Source:   clip.SourceUpload,
		Codec:    "h264",
		ByteSize: 1,
	}
	if id != "" {
		require.NoError(t, store.AddWithID(id, c))
	} else {
		require.NoError(t, store.Add(c))
	}
	return c
}

// A snapshot captured on one engine and applied to the other leaves both
// engines with the same clip players, whether the clip has the same ID on
// both engines or a different one.
func TestClipPlayersSnapshotRoundTrip(t *testing.T) {
	appA, storeA := newClipTestApp(t)
	appB, storeB := newClipTestApp(t)

	const shared = "0123456789abcdef0123456789abcdef"
	addClip(t, storeA, shared, "shared")
	addClip(t, storeB, shared, "shared")
	legacyA := addClip(t, storeA, "", "legacy")
	addClip(t, storeB, "", "legacy")

	require.NoError(t, appA.clipMgr.Load(1, shared))
	require.NoError(t, appA.clipMgr.Load(3, legacyA.ID))
	require.NoError(t, appA.clipMgr.Play(3, 0.25, true))
	require.Eventually(t, func() bool {
		return appA.clipMgr.PlayerStates()[2].State == clip.StatePlaying
	}, 2*time.Second, 5*time.Millisecond)

	// B starts out of step: a clip in a slot that is empty on A.
	require.NoError(t, appB.clipMgr.Load(2, shared))

	var snap internal.FullStateSnapshot
	snap.ClipPlayers, snap.ClipPlayerPositions = appA.captureClipPlayers()
	require.Empty(t, internal.ApplyClipPlayers(snap, appB))

	type slot struct {
		name  string
		state clip.PlayerState
	}
	view := func(app *App) []slot {
		var out []slot
		for _, ps := range app.clipMgr.PlayerStates() {
			out = append(out, slot{ps.ClipName, ps.State})
		}
		return out
	}
	require.Equal(t, []slot{
		{"shared", clip.StateLoaded},
		{"", clip.StateEmpty},
		{"legacy", clip.StatePlaying},
		{"", clip.StateEmpty},
	}, view(appA))
	// Playback starts asynchronously on B.
	require.Eventually(t, func() bool {
		a, b := view(appA), view(appB)
		return len(a) == len(b) && a[0] == b[0] && a[1] == b[1] && a[2] == b[2] && a[3] == b[3]
	}, 2*time.Second, 5*time.Millisecond, "A %v, B %v", view(appA), view(appB))
	require.Equal(t, shared, appB.clipMgr.PlayerStates()[0].ClipID)
}
