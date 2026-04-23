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

	sw.EnableWallClockVideoPTS()
	hs = sw.hotState.Load()
	require.Equal(t, sw.wallClockVideoEnabled, hs.wallClockVideoEnabled, "EnableWallClockVideoPTS mirror divergence")

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

func TestMirrorWrites_SourcesMap_SnapshotMatchesLegacy(t *testing.T) {
	relay := newTestRelay()
	sw := newTestSwitcher(t, relay)

	relayA := newTestRelay()
	sw.RegisterSource("camA", relayA)
	snap := *sw.sourcesAtomic.Load()
	require.Len(t, snap, len(sw.sources), "atomic snapshot size mismatch after RegisterSource")
	_, hasA := snap["camA"]
	require.True(t, hasA, "atomic snapshot missing camA after RegisterSource")

	sw.UnregisterSource("camA")
	snap = *sw.sourcesAtomic.Load()
	_, hasA = snap["camA"]
	require.False(t, hasA, "atomic snapshot still has camA after UnregisterSource")
	require.Len(t, snap, len(sw.sources), "atomic snapshot size mismatch after UnregisterSource")
}
