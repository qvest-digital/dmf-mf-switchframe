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
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/graphics/textrender"
	"github.com/zsiec/switchframe/server/htmlgfx"
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

// A channel pair selected on one engine reaches the other through the
// state it captures and the other applies.
func TestSnapshotRoundTripCarriesChannelPair(t *testing.T) {
	ctx := context.Background()
	a, b := roundTripApp(t), roundTripApp(t)
	require.NoError(t, a.sw.Cut(ctx, "cam1"))
	require.NoError(t, b.sw.Cut(ctx, "cam1"))
	require.NoError(t, a.mixer.SetChannelPair("cam2", 3))

	require.Equal(t, []string{"audioChannels"}, internal.SnapshotDiff(a.captureFullSnapshot(), b.captureFullSnapshot()))
	if w := b.applyPeerState(ctx, a.captureFullSnapshot(), 7); len(w) > 0 {
		t.Fatalf("apply warnings: %v", w)
	}
	require.Equal(t, 3, b.mixer.ChannelPair("cam2"))
	require.Empty(t, internal.SnapshotDiff(a.captureFullSnapshot(), b.captureFullSnapshot()))
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

// An engine taking its peer's state takes the squeeze-back with it: a
// restarted engine comes back with the L-frame the peer shows, not full
// frame, and one whose peer has none drops its own.
func TestSnapshotRoundTripCarriesSqueezeback(t *testing.T) {
	ctx := context.Background()
	a, b := roundTripApp(t), roundTripApp(t)
	a.squeeze, b.squeeze = dve.NewSqueeze(1920, 1080), dve.NewSqueeze(1920, 1080)
	require.NoError(t, a.sw.Cut(ctx, "cam1"))
	require.NoError(t, b.sw.Cut(ctx, "cam1"))
	offset := -300
	cfg := dve.SqueezeConfig{Rect: dve.Rect{X: 480, Y: 0, W: 1440, H: 810}, Background: "cam2", DurationMs: 800, Easing: "linear",
		GraphicsOffsetMs: &offset}
	require.NoError(t, a.squeeze.Set(cfg))
	require.NoError(t, a.squeeze.On(dve.SqueezeMove{}))

	require.Equal(t, []string{"squeezeback"}, internal.SnapshotDiff(a.captureFullSnapshot(), b.captureFullSnapshot()))
	if w := b.applyPeerState(ctx, a.captureFullSnapshot(), 7); len(w) > 0 {
		t.Fatalf("apply warnings: %v", w)
	}
	st := b.squeeze.Snapshot()
	require.Equal(t, cfg, st.SqueezeConfig)
	require.True(t, st.On)
	require.False(t, st.Animating, "the engine taking the state lands where the move arrives")
	require.Empty(t, internal.SnapshotDiff(a.captureFullSnapshot(), b.captureFullSnapshot()))

	require.NoError(t, a.squeeze.Off(dve.SqueezeMove{}))
	b.applyPeerState(ctx, a.captureFullSnapshot(), 8)
	require.False(t, b.squeeze.Snapshot().On)

	a.squeeze.Reset()
	b.applyPeerState(ctx, a.captureFullSnapshot(), 9)
	require.False(t, b.squeeze.Snapshot().Configured)
	require.Empty(t, internal.SnapshotDiff(a.captureFullSnapshot(), b.captureFullSnapshot()))
}

// A snapshot from an engine that predates the squeeze-back in its state
// leaves this engine's alone rather than removing it.
func TestSnapshotFromOlderEngineKeepsSqueezeback(t *testing.T) {
	ctx := context.Background()
	b := roundTripApp(t)
	b.squeeze = dve.NewSqueeze(1920, 1080)
	require.NoError(t, b.squeeze.Set(dve.SqueezeConfig{Rect: dve.Rect{X: 480, W: 1440, H: 810}}))
	require.NoError(t, b.squeeze.On(dve.SqueezeMove{DurationMs: new(int)}))

	older := b.captureFullSnapshot()
	older.Squeezeback, older.SqueezebackSynced = nil, false
	b.applyPeerState(ctx, older, 3)
	require.True(t, b.squeeze.Snapshot().On)
}

// oneLayerGfx is an HTML5 graphics sidecar with a graphic on layer 1.
type oneLayerGfx struct{}

func (oneLayerGfx) Started() bool                    { return true }
func (oneLayerGfx) AllocateBrowser(int) (int, error) { return 0, nil }
func (oneLayerGfx) FreeBrowser(int)                  {}
func (oneLayerGfx) BrowserForLayer(layer int) int {
	return map[bool]int{true: 0, false: -1}[layer == 1]
}
func (oneLayerGfx) CheckURL(string) error                     { return nil }
func (oneLayerGfx) LoadURL(int, string) error                 { return nil }
func (oneLayerGfx) Play(int) error                            { return nil }
func (oneLayerGfx) Stop(int) error                            { return nil }
func (oneLayerGfx) Next(int) error                            { return nil }
func (oneLayerGfx) Update(int, string) error                  { return nil }
func (oneLayerGfx) CustomAction(int, string, string) error    { return nil }
func (oneLayerGfx) DiscoverTemplates() []htmlgfx.TemplateInfo { return nil }

// Taking the peer's squeezeback drops a move still waiting on its
// graphics' head start, so the engine ends where the peer is.
func TestApplySqueezebackCancelsPendingMove(t *testing.T) {
	ctx := context.Background()
	a, b := roundTripApp(t), roundTripApp(t)
	a.squeeze, b.squeeze = dve.NewSqueeze(1920, 1080), dve.NewSqueeze(1920, 1080)
	b.api = control.NewAPI(b.sw, control.WithSqueeze(b.squeeze))
	b.api.SetHTMLGfxManager(oneLayerGfx{})
	offset := -150
	cfg := dve.SqueezeConfig{Rect: dve.Rect{X: 480, W: 1440, H: 810}, GraphicsOffsetMs: &offset}
	require.NoError(t, a.squeeze.Set(cfg))
	require.NoError(t, b.squeeze.Set(cfg))
	require.NoError(t, b.api.MoveSqueeze(true, []byte(`{"durationMs":0,"graphics":[{"layerId":1,"data":{}}]}`)))

	b.applyPeerState(ctx, a.captureFullSnapshot(), 5)
	time.Sleep(300 * time.Millisecond)
	require.False(t, b.squeeze.Snapshot().On, "the move waiting before the apply ran after it")
}
