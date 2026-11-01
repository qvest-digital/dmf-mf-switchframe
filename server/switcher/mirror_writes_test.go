package switcher

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/graphics"
)

// TestMirrorWrites_HotState_StayInSyncWithLegacyFields asserts the Phase-1
// invariant: every mutator that writes a legacy s.mu-guarded HOT field also
// publishes the matching value into the atomic slot. Phase 2 deletes the
// legacy reads; this test is the tripwire that fires if a mirror write is
// missed or drifts.
func TestMirrorWrites_HotState_StayInSyncWithLegacyFields(t *testing.T) {
	relay := newTestRelay()
	sw := newTestSwitcher(t, relay)

	// Register two user sources so Cut/SetPreview have targets.
	relayA := newTestRelay()
	relayB := newTestRelay()
	sw.RegisterSource("camA", relayA)
	sw.RegisterSource("camB", relayB)

	require.NoError(t, sw.Cut(context.Background(), "camA"))
	hs := sw.hotState.Load()
	require.Equal(t, sw.programSource, hs.programSource, "Cut: programSource mirror divergence")
	require.Equal(t, sw.previewSource, hs.previewSource, "Cut: previewSource mirror divergence")
	require.Equal(t, sw.state, hs.state, "Cut: state mirror divergence")

	require.NoError(t, sw.SetPreview(context.Background(), "camB"))
	hs = sw.hotState.Load()
	require.Equal(t, sw.previewSource, hs.previewSource, "SetPreview: previewSource mirror divergence")

	require.NoError(t, sw.SetFTBDuration(1234))
	require.EqualValues(t, sw.ftbDurationMs, sw.ftbDurationMsAtomic.Load(), "SetFTBDuration mirror divergence")

	require.NoError(t, sw.SetTransitionAbortThreshold(0.42))
	gotThresh := math.Float64frombits(sw.transitionAbortThresholdAtomic.Load())
	require.InDelta(t, sw.transitionAbortThreshold, gotThresh, 1e-12, "SetTransitionAbortThreshold mirror divergence")
}

func TestMirrorWrites_GraphicsAndAudioHandlers(t *testing.T) {
	relay := newTestRelay()
	sw := newTestSwitcher(t, relay)

	kb := &graphics.KeyProcessorBridge{}
	sw.SetKeyBridge(kb)
	gh := sw.graphicsHandlers.Load()
	require.Equal(t, sw.keyBridge, gh.keyBridge, "SetKeyBridge mirror divergence")
	require.Same(t, kb, gh.keyBridge)

	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {})
	ah := sw.audioHandlers.Load()
	require.NotNil(t, ah.audioHandler, "SetAudioHandler did not publish to audioHandlers")
}

func TestMirrorWrites_TransitionLifecycle_MirrorsProgramAndEngine(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)

	// StartTransition → hotState.transEngine and transTypeOrig published, preview swaps.
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))
	hs := sw.hotState.Load()
	require.Equal(t, sw.transEngine, hs.transEngine, "StartTransition: transEngine mirror divergence")
	require.Equal(t, sw.transTypeOrig, hs.transTypeOrig, "StartTransition: transTypeOrig mirror divergence")
	require.Equal(t, sw.previewSource, hs.previewSource, "StartTransition: previewSource mirror divergence")
	require.Equal(t, sw.state, hs.state, "StartTransition: state mirror divergence")

	// handleTransitionComplete(false) → programSource advances, transEngine cleared.
	sw.handleTransitionComplete(false)
	hs = sw.hotState.Load()
	require.Equal(t, sw.programSource, hs.programSource, "handleTransitionComplete: programSource mirror divergence")
	require.Nil(t, hs.transEngine, "handleTransitionComplete: transEngine should be nil")
	require.Empty(t, hs.transTypeOrig, "handleTransitionComplete: transTypeOrig should be cleared")
	require.Equal(t, sw.state, hs.state, "handleTransitionComplete: state mirror divergence")
}

func TestMirrorWrites_AbortTransition_MirrorsEngineNil(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)

	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))
	require.NotNil(t, sw.hotState.Load().transEngine)

	sw.AbortTransition()
	hs := sw.hotState.Load()
	require.Nil(t, hs.transEngine, "AbortTransition: transEngine should be nil in hotState")
	require.Equal(t, sw.transEngine, hs.transEngine, "AbortTransition: transEngine mirror divergence")
	require.Equal(t, sw.state, hs.state, "AbortTransition: state mirror divergence")
}

func TestMirrorWrites_FTBLifecycle_MirrorsEngine(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)

	require.NoError(t, sw.FadeToBlack(context.Background()))
	hs := sw.hotState.Load()
	require.Equal(t, sw.transEngine, hs.transEngine, "FadeToBlack forward: transEngine mirror divergence")
	require.Equal(t, sw.state, hs.state, "FadeToBlack forward: state mirror divergence")

	sw.handleFTBComplete(false)
	hs = sw.hotState.Load()
	require.Nil(t, hs.transEngine, "handleFTBComplete: transEngine should be nil")
	require.Equal(t, sw.state, hs.state, "handleFTBComplete: state mirror divergence")
}

// TestMirrorWrites_SourceState_AllConstructorsSeedAtomics asserts every
// sourceState construction path seeds labelAtomic and positionAtomic. The
// invariant is a Phase-5 prerequisite: once the sources map promotes to
// atomic-primary, lock-free readers that dereference ss.labelAtomic /
// ss.positionAtomic must never see nil/zero on a freshly-registered source.
// If a new constructor sibling is added without calling initAtomicMirrors,
// this test fails.
func TestMirrorWrites_SourceState_AllConstructorsSeedAtomics(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())

	// Built-in sources are seeded in registerBuiltinSourcesLocked, which
	// runs inside newTestSwitcher. Verify directly.
	sw.configMu.Lock()
	builtinBlack := (*sw.sourcesAtomic.Load())[BuiltinSourceKeyBlack]
	builtinBars := (*sw.sourcesAtomic.Load())[BuiltinSourceKeyColorbars]
	sw.configMu.Unlock()
	for _, bs := range []*sourceState{builtinBlack, builtinBars} {
		if lbl := bs.labelAtomic.Load(); lbl == nil || *lbl != bs.label {
			t.Errorf("built-in %q: labelAtomic not seeded (got %v, want %q)", bs.key, lbl, bs.label)
		}
		if pos := bs.positionAtomic.Load(); pos != int32(bs.position) {
			t.Errorf("built-in %q: positionAtomic not seeded (got %d, want %d)", bs.key, pos, bs.position)
		}
	}

	// Each production register path.
	sw.RegisterSource("camA", newTestRelay())
	sw.RegisterVirtualSource("vReplay", newTestRelay())
	sw.RegisterMXLSource("mxl-cam")     // calls registerRawSource
	sw.RegisterSRTSource("srt:srt-cam") // calls registerRawSource
	sw.RegisterReplaySource("replay-1")

	for _, key := range []string{"camA", "vReplay", "mxl-cam", "srt:srt-cam", "replay-1"} {
		sw.configMu.Lock()
		ss, ok := (*sw.sourcesAtomic.Load())[key]
		sw.configMu.Unlock()
		if !ok {
			t.Errorf("%q: source not registered", key)
			continue
		}
		if lbl := ss.labelAtomic.Load(); lbl == nil || *lbl != ss.label {
			t.Errorf("%q: labelAtomic not seeded (got %v, want %q)", key, lbl, ss.label)
		}
		if pos := ss.positionAtomic.Load(); pos != int32(ss.position) {
			t.Errorf("%q: positionAtomic not seeded (got %d, want %d)", key, pos, ss.position)
		}
	}
}

func TestMirrorWrites_SourceState_LabelAndPositionAtomics(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())

	relayA := newTestRelay()
	sw.RegisterSource("camA", relayA)

	// SetLabel mirrors.
	require.NoError(t, sw.SetLabel(context.Background(), "camA", "Camera Alpha"))
	sw.configMu.Lock()
	ss := (*sw.sourcesAtomic.Load())["camA"]
	sw.configMu.Unlock()
	if got := ss.labelAtomic.Load(); got == nil || *got != "Camera Alpha" || ss.label != *got {
		t.Errorf("SetLabel mirror divergence: legacy=%q atomic=%v", ss.label, got)
	}

	// SetSourcePosition mirrors (also swap partner).
	relayB := newTestRelay()
	sw.RegisterSource("camB", relayB)
	sw.configMu.Lock()
	ssB := (*sw.sourcesAtomic.Load())["camB"]
	bOriginalPos := ssB.position
	sw.configMu.Unlock()

	require.NoError(t, sw.SetSourcePosition("camA", bOriginalPos))
	sw.configMu.Lock()
	ssA := (*sw.sourcesAtomic.Load())["camA"]
	ssB = (*sw.sourcesAtomic.Load())["camB"]
	sw.configMu.Unlock()
	if ssA.positionAtomic.Load() != int32(ssA.position) {
		t.Errorf("SetSourcePosition: camA atomic divergence legacy=%d atomic=%d", ssA.position, ssA.positionAtomic.Load())
	}
	if ssB.positionAtomic.Load() != int32(ssB.position) {
		t.Errorf("SetSourcePosition: swap-partner camB atomic divergence legacy=%d atomic=%d", ssB.position, ssB.positionAtomic.Load())
	}
}

func TestSourcesMap_RegisterAndUnregisterPublishAtomically(t *testing.T) {
	relay := newTestRelay()
	sw := newTestSwitcher(t, relay)

	// Captures built-in source count (black, colorbars) seeded by New().
	baseline := len(*sw.sourcesAtomic.Load())

	relayA := newTestRelay()
	sw.RegisterSource("camA", relayA)
	snap := *sw.sourcesAtomic.Load()
	require.Len(t, snap, baseline+1, "atomic snapshot should grow by 1 after RegisterSource")
	_, hasA := snap["camA"]
	require.True(t, hasA, "atomic snapshot missing camA after RegisterSource")

	sw.UnregisterSource("camA")
	snap = *sw.sourcesAtomic.Load()
	_, hasA = snap["camA"]
	require.False(t, hasA, "atomic snapshot still has camA after UnregisterSource")
	require.Len(t, snap, baseline, "atomic snapshot should return to baseline after UnregisterSource")
}
