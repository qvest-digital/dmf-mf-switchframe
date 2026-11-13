package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/graphics/textrender"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

// roundTripApp is an engine with the subsystems a state snapshot covers
// that run without media.
func roundTripApp(t *testing.T) *App {
	t.Helper()
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	sw.SetMixer(m)
	for _, key := range []string{"cam1", "cam2"} {
		sw.RegisterSource(key, distribution.NewRelay())
		m.AddChannel(key)
	}
	a := &App{sw: sw, mixer: m, compositor: graphics.NewCompositor(), outputMgr: output.NewManager(),
		sessionMgr: operator.NewSessionManager()}
	t.Cleanup(a.sessionMgr.Close)
	t.Cleanup(a.compositor.Close)
	tr, err := textrender.NewRenderer()
	require.NoError(t, err)
	a.tickerEngine = graphics.NewTickerEngine(a.compositor, tr)
	t.Cleanup(a.tickerEngine.Close)
	a.sequenceEngine = graphics.NewSequenceEngine(a.compositor)
	t.Cleanup(a.sequenceEngine.Close)
	a.api = control.NewAPI(sw, control.WithMixer(m), control.WithCompositor(a.compositor))
	return a
}

// What one engine captures, another applies to the point where the two
// compare equal, and applying it again changes nothing: a difference the
// engines repair between themselves is repaired by one apply, and engines
// that agree stay agreeing.
func TestSnapshotRoundTripConverges(t *testing.T) {
	ctx := context.Background()
	a, b := roundTripApp(t), roundTripApp(t)

	require.NoError(t, a.sw.Cut(ctx, "cam1"))
	require.NoError(t, a.sw.SetPreview(ctx, "cam2"))
	require.NoError(t, a.mixer.SetLevel("cam1", -6))
	require.NoError(t, a.mixer.SetMuted("cam2", true))
	require.NoError(t, a.mixer.SetMasterLevel(-3))
	require.NoError(t, a.compositor.AddLayerWithID(4))
	require.NoError(t, a.compositor.SetImage(4, "logo.png", graphicsTestPNG(t)))
	require.NoError(t, a.compositor.On(4))
	require.NoError(t, a.tickerEngine.Start(4, graphics.TickerConfig{Text: "news", FontSize: 24, Speed: 60}))

	b.api.SetSnapshotProvider(&snapshotProviderAdapter{app: b})
	before := b.api.CompareState()
	time.Sleep(5 * time.Millisecond)

	require.NotEmpty(t, internal.SnapshotDiff(a.captureFullSnapshot(), b.captureFullSnapshot()))
	if w := b.applyPeerState(ctx, a.captureFullSnapshot(), 7); len(w) > 0 {
		t.Fatalf("apply warnings: %v", w)
	}
	require.Empty(t, internal.SnapshotDiff(a.captureFullSnapshot(), b.captureFullSnapshot()))
	// Taking the peer's state is no write of b's own: it keeps b's change
	// time, which the engines' authority over a difference is decided on.
	require.Equal(t, before.ChangedAtMs, b.api.CompareState().ChangedAtMs)

	digest := internal.SnapshotDigest(b.captureFullSnapshot())
	b.applyPeerState(ctx, a.captureFullSnapshot(), 7)
	require.Equal(t, digest, internal.SnapshotDigest(b.captureFullSnapshot()), "a second apply changed the state")
}

// The graphics sequences an apply takes from the peer are read before the
// apply starts: a write here while they are read is dated as this
// engine's own, not hidden in the apply's window.
func TestApplyPeerStateReadsSequencesBeforeTheApply(t *testing.T) {
	ctx := context.Background()
	src, b := roundTripApp(t), roundTripApp(t)
	require.NoError(t, src.compositor.AddLayerWithID(2))
	require.NoError(t, src.sequenceEngine.UploadAsync(2, motionTestZIP(t, 120), 25))
	srcAPI := control.NewAPI(src.sw, control.WithCompositor(src.compositor), control.WithSequenceEngine(src.sequenceEngine))
	srcAPI.SetSnapshotProvider(&snapshotProviderAdapter{app: src})

	b.api.SetSnapshotProvider(&snapshotProviderAdapter{app: b})
	before := b.api.CompareState()
	time.Sleep(5 * time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/graphics-sequence" {
			// An operator write here, seen by the peer's compare read.
			require.NoError(t, b.mixer.SetMasterLevel(-9))
			b.api.CompareState()
		}
		srcAPI.Mux().ServeHTTP(w, r)
	}))
	defer srv.Close()
	b.cfg.PeerEngineURL = srv.URL
	b.peerPoller = peer.NewHealthPoller(srv.URL)

	b.applyPeerState(ctx, src.captureFullSnapshot(), 0)
	require.Greater(t, b.api.CompareState().ChangedAtMs, before.ChangedAtMs,
		"a write while the sequence was read was not dated")
	require.Eventually(t, func() bool { return b.sequenceEngine.HasSequence(2) }, 5*time.Second, 10*time.Millisecond)
}
