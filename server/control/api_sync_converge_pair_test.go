package control

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

// lockedProvider is a snapshot provider whose state tests change while
// the engine serves it.
type lockedProvider struct {
	mu   sync.Mutex
	snap internal.FullStateSnapshot
}

func (p *lockedProvider) CaptureFullSnapshot() internal.FullStateSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snap
}

func (p *lockedProvider) ApplyTarget() internal.SnapshotApplyTarget {
	return &mockApplyTarget{provider: &mockSnapshotProvider{}}
}

func (p *lockedProvider) SnapshotReady() bool { return true }

func (p *lockedProvider) set(fn func(*internal.FullStateSnapshot)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(&p.snap)
}

// convergeEngine is an engine's API, its state and its converger, with the
// change times its compare reads report.
type convergeEngine struct {
	api      *API
	state    *lockedProvider
	conv     *peer.Converger
	applies  int
	lacksPIP bool // the engine cannot take the "pip" layout preset
}

// apply takes the peer's state as an engine does, marking the apply, and
// leaves out a layout preset the engine lacks, reporting it.
func (e *convergeEngine) apply(_ context.Context, snap internal.FullStateSnapshot, _ uint64) []string {
	defer e.api.ApplyingPeerState()()
	e.applies++
	var warnings []string
	e.state.set(func(s *internal.FullStateSnapshot) {
		keep := s.Layout.ActivePreset
		*s = snap
		if e.lacksPIP && snap.Layout.ActivePreset == "pip" {
			s.Layout.ActivePreset = keep
			warnings = append(warnings, "layout preset pip not available here")
		}
	})
	return warnings
}

// An apply of the peer's state that leaves part of a difference in place
// is no write of the engine's own: it does not move the engine's change
// time, so the engine whose write the difference is keeps authority over
// it and its write is never taken back.
func TestConvergePairPartialApplyKeepsTheWritersAuthority(t *testing.T) {
	base := internal.FullStateSnapshot{ProgramSource: "cam1", PreviewSource: "cam2"}
	engines := map[string]*convergeEngine{}
	for _, label := range []string{"a", "b"} {
		e := &convergeEngine{state: &lockedProvider{snap: base}}
		e.api = NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()))
		e.api.SetSnapshotProvider(e.state)
		engines[label] = e
	}
	a, b := engines["a"], engines["b"]
	a.lacksPIP = true
	b.api.startTime = a.api.startTime
	srvA, srvB := httptest.NewServer(a.api.Mux()), httptest.NewServer(b.api.Mux())
	t.Cleanup(srvA.Close)
	t.Cleanup(srvB.Close)
	cfg := peer.ConvergeConfig{Tick: time.Second, Settle: time.Millisecond, Backoff: 20 * time.Millisecond,
		MaxBackoff: 20 * time.Millisecond, StartTie: time.Minute, ChangeTie: 30 * time.Millisecond}
	pa := peer.NewHealthPoller(srvB.URL, peer.WithLocalIdentity("a"))
	pa.SetPeerStateForTest(true, "b", 0) // a leads
	pb := peer.NewHealthPoller(srvA.URL, peer.WithLocalIdentity("b"))
	pb.SetPeerStateForTest(true, "a", 0)
	a.conv = peer.NewConverger(pa, cfg, a.api.CompareState, a.apply)
	b.conv = peer.NewConverger(pb, cfg, b.api.CompareState, b.apply)

	step := func(n int) {
		for i := 0; i < n; i++ {
			a.conv.Step(context.Background())
			b.conv.Step(context.Background())
			time.Sleep(40 * time.Millisecond)
		}
	}
	step(3)
	require.Zero(t, a.applies+b.applies)

	// A write without a command sequence reaches b only; a can take its
	// preview but not its layout preset.
	b.state.set(func(s *internal.FullStateSnapshot) { s.PreviewSource = "cam3"; s.Layout.ActivePreset = "pip" })
	step(25)

	require.NotZero(t, a.applies, "a never took b's write")
	require.Zero(t, b.applies, "b took a's state back over its own later write")
	require.Equal(t, "pip", b.state.CaptureFullSnapshot().Layout.ActivePreset)
	require.Equal(t, "cam3", a.state.CaptureFullSnapshot().PreviewSource)
}

// A snapshot a browser pushes is no write of the engine's own either.
func TestApplyStateSnapshot_DoesNotDateAChange(t *testing.T) {
	state := &lockedProvider{snap: internal.FullStateSnapshot{ProgramSource: "camera1"}}
	api := NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()))
	api.SetSnapshotProvider(&applyingProvider{lockedProvider: state})
	before := getCompareState(t, api)
	time.Sleep(5 * time.Millisecond)

	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("POST", "/api/sync/state-snapshot",
		strings.NewReader(`{"programSource":"camera1","previewSource":"camera2"}`)))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	after := getCompareState(t, api)
	require.Equal(t, "camera2", after.Snapshot.PreviewSource)
	require.Equal(t, before.ChangedAtMs, after.ChangedAtMs, "a pushed snapshot was dated as this engine's write")
}

// applyingProvider applies a pushed preview to its state.
type applyingProvider struct{ *lockedProvider }

func (p *applyingProvider) ApplyTarget() internal.SnapshotApplyTarget {
	return &previewTarget{mockApplyTarget: &mockApplyTarget{provider: &mockSnapshotProvider{}}, p: p.lockedProvider}
}

type previewTarget struct {
	*mockApplyTarget
	p *lockedProvider
}

// midApply, when set, runs once the preview is set, before the apply ends.
var midApply func()

func (t *previewTarget) SetPreview(_ context.Context, source string) error {
	t.p.set(func(s *internal.FullStateSnapshot) { s.PreviewSource = source })
	if midApply != nil {
		midApply()
	}
	return nil
}

// The peer reads the compared state every few seconds, also while a
// snapshot is applied here: a change it sees then is not dated either.
func TestGetStateCompare_ReadDuringApplyIsNotDated(t *testing.T) {
	state := &lockedProvider{snap: internal.FullStateSnapshot{ProgramSource: "camera1"}}
	api := NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()))
	api.SetSnapshotProvider(&applyingProvider{lockedProvider: state})
	before := getCompareState(t, api)
	time.Sleep(5 * time.Millisecond)
	var during peer.CompareState
	midApply = func() { during = api.CompareState() }
	t.Cleanup(func() { midApply = nil })

	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("POST", "/api/sync/state-snapshot",
		strings.NewReader(`{"programSource":"camera1","previewSource":"camera2"}`)))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, "camera2", during.Snapshot.PreviewSource)
	require.Equal(t, before.ChangedAtMs, during.ChangedAtMs, "a change read during an apply was dated")
	require.Equal(t, before.ChangedAtMs, getCompareState(t, api).ChangedAtMs)
}

// Once an apply has ended, a write here is dated again, also after an
// apply that panicked.
func TestApplyStateSnapshot_WriteAfterApplyIsDated(t *testing.T) {
	for name, panics := range map[string]bool{"apply ended": false, "apply panicked": true} {
		t.Run(name, func(t *testing.T) {
			state := &lockedProvider{snap: internal.FullStateSnapshot{ProgramSource: "camera1"}}
			api := NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()))
			api.SetSnapshotProvider(&applyingProvider{lockedProvider: state})
			before := getCompareState(t, api)
			if panics {
				midApply = func() { panic("apply failed") }
				t.Cleanup(func() { midApply = nil })
			}
			func() {
				defer func() { _ = recover() }()
				api.Mux().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/sync/state-snapshot",
					strings.NewReader(`{"programSource":"camera1","previewSource":"camera2"}`)))
			}()
			midApply = nil
			getCompareState(t, api)
			time.Sleep(5 * time.Millisecond)
			state.set(func(s *internal.FullStateSnapshot) { s.PreviewSource = "camera3" })
			after := getCompareState(t, api)
			require.Greater(t, after.ChangedAtMs, before.ChangedAtMs, "a write after the apply was not dated")
		})
	}
}

type preparedKey struct{}

// preparingProvider prepares a pushed snapshot, noting whether the apply's
// window was open meanwhile, and its apply target sees the context the
// preparation returned.
type preparingProvider struct {
	*applyingProvider
	api         *API
	prepared    bool
	windowOpen  bool
	sawPrepared bool
}

func (p *preparingProvider) PrepareSnapshot(ctx context.Context, _ internal.FullStateSnapshot) context.Context {
	p.prepared = true
	p.api.compareChange.mu.Lock()
	p.windowOpen = p.api.compareChange.applying > 0
	p.api.compareChange.mu.Unlock()
	return context.WithValue(ctx, preparedKey{}, true)
}

func (p *preparingProvider) ApplyTarget() internal.SnapshotApplyTarget {
	return &ctxTarget{previewTarget: p.applyingProvider.ApplyTarget().(*previewTarget), p: p}
}

type ctxTarget struct {
	*previewTarget
	p *preparingProvider
}

func (t *ctxTarget) SetPreview(ctx context.Context, source string) error {
	t.p.sawPrepared, _ = ctx.Value(preparedKey{}).(bool)
	return t.previewTarget.SetPreview(ctx, source)
}

// What a pushed snapshot takes from the peer is read before the apply's
// window opens, and the apply runs with what was read.
func TestApplyStateSnapshot_PreparesBeforeTheApply(t *testing.T) {
	state := &lockedProvider{snap: internal.FullStateSnapshot{ProgramSource: "camera1"}}
	api := NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()))
	p := &preparingProvider{applyingProvider: &applyingProvider{lockedProvider: state}, api: api}
	api.SetSnapshotProvider(p)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("POST", "/api/sync/state-snapshot",
		strings.NewReader(`{"programSource":"camera1","previewSource":"camera2"}`)))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.True(t, p.prepared, "the snapshot was not prepared")
	require.False(t, p.windowOpen, "prepared inside the apply's window")
	require.True(t, p.sawPrepared, "the apply did not run with what was prepared")
}
