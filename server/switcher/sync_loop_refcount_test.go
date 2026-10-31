package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clock"
)

// Finding #23: popProgramFrame used to return a borrowed value copy that
// shared the refs pointer with frame_sync's lastRawVideo without taking a
// reference. The sync-loop transition path then ran normalizeResolution on
// that borrowed copy; when the source resolution differed from the pipeline
// format, normalizeResolution's internal ReleaseYUV dropped the shared
// refcount 1→0 and returned the buffer to the FramePool while lastRawVideo
// still aliased it — freeze-repeat then served recycled pool memory and the
// eventual lastRawVideo.ReleaseYUV underflowed the refcount.
//
// These tests pin the corrected contract: popProgramFrame returns a counted
// reference that the caller releases, and lastRawVideo keeps its own ref.

// ingestSyncTestFrame pushes a managed pool-backed frame into the frame sync.
func ingestSyncTestFrame(s *Switcher, key string, w, h int, yVal byte, pts int64) {
	pool := s.framePool.Load()
	size := w * h * 3 / 2
	buf := pool.Acquire()[:size]
	for j := 0; j < w*h; j++ {
		buf[j] = yVal
	}
	for j := w * h; j < size; j++ {
		buf[j] = 128
	}
	pf := &ProcessingFrame{Data: buf, Width: w, Height: h, PTS: pts, pool: pool}
	pf.SetRefs(1)
	s.frameSync.IngestRawVideo(key, pf)
}

func TestSyncLoopPopAndNormalizeDoesNotDoubleReleaseLastRawVideo(t *testing.T) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.configMu.Lock()
	s.frameSync.AddSource("cam1")
	fs := s.frameSync
	s.configMu.Unlock()

	// Ingest at 160x120 — different from the 320x240 pipeline format, so
	// normalizeResolution scales (and releases the popped reference), as the
	// sync-loop transition path does for mismatched-resolution sources.
	ingestSyncTestFrame(s, "cam1", 160, 120, 40, 0)

	got, _ := s.popProgramFrame(fs, "cam1", clock.Tick{VideoPTS: 3000, ReadPTS: 3000})
	require.NotNil(t, got)
	got = s.normalizeResolution(got)
	require.NotNil(t, got)
	require.Equal(t, 320, got.Width, "frame should be scaled to pipeline format")
	got.ReleaseYUV() // transition tick releases its popped frames when done

	fs.mu.Lock()
	ss := fs.sources["cam1"]
	fs.mu.Unlock()
	require.NotNil(t, ss)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	require.NotNil(t, ss.lastRawVideo, "freeze-repeat frame must survive the transition tick")
	require.Equal(t, int32(1), ss.lastRawVideo.Refs(),
		"lastRawVideo must retain its own reference (no double release into the pool)")
	require.NotNil(t, ss.lastRawVideo.Data,
		"lastRawVideo buffer must not have been recycled")
}

func TestSyncLoopPopProgramFrameReturnsCountedRef(t *testing.T) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.configMu.Lock()
	s.frameSync.AddSource("cam1")
	fs := s.frameSync
	s.configMu.Unlock()

	pool := s.framePool.Load()
	ingestSyncTestFrame(s, "cam1", 320, 240, 40, 0)

	// Fresh-frame pop: the returned copy must own a reference so the
	// pipeline's MakeWritable detects the share with lastRawVideo and
	// copies before mutating (frames retained by frame_sync are never
	// mutated in place).
	got, _ := s.popProgramFrame(fs, "cam1", clock.Tick{VideoPTS: 3000, ReadPTS: 3000})
	require.NotNil(t, got)
	require.Equal(t, int32(2), got.Refs(),
		"popped frame must hold its own reference alongside lastRawVideo")

	fs.mu.Lock()
	ss := fs.sources["cam1"]
	fs.mu.Unlock()
	require.NotNil(t, ss)

	got.MakeWritable(pool)
	got.Data[0] = 99

	ss.mu.Lock()
	require.Equal(t, byte(40), ss.lastRawVideo.Data[0],
		"mutating the popped frame must not corrupt the retained freeze-repeat frame")
	ss.mu.Unlock()

	got.ReleaseYUV()

	// Freeze-repeat pop (no fresh frame): same contract.
	frozen, _ := s.popProgramFrame(fs, "cam1", clock.Tick{VideoPTS: 6000, ReadPTS: 6000})
	require.NotNil(t, frozen)
	require.Equal(t, int32(2), frozen.Refs(),
		"freeze-repeat copy must also hold its own reference")
	frozen.ReleaseYUV()

	ss.mu.Lock()
	require.Equal(t, int32(1), ss.lastRawVideo.Refs())
	ss.mu.Unlock()
}

// TestSyncLoopPopProgramFrameUnmanagedFrameSingleOwner pins the Ref/ReleaseYUV
// asymmetry guard for UNMANAGED pool-backed frames (refs == nil): Ref() is a
// no-op but ReleaseYUV really frees, so popProgramFrame's borrowed copy must
// not be able to return the pool buffer while frame_sync's lastRawVideo still
// aliases it. Producers are expected to ingest pool frames as managed
// (SetRefs(1) — see startSRTPipelineIngest), but the sync loop must stay
// memory-safe for any legacy unmanaged producer: borrowRawVideoRef detaches
// the borrowed copy from the pool, leaving lastRawVideo the single owner.
func TestSyncLoopPopProgramFrameUnmanagedFrameSingleOwner(t *testing.T) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	cam1Relay := distribution.NewRelay()
	s.RegisterSource("cam1", cam1Relay)
	s.configMu.Lock()
	s.frameSync.AddSource("cam1")
	fs := s.frameSync
	s.configMu.Unlock()

	// Ingest an UNMANAGED pool-backed frame (SetPool but no SetRefs), the
	// shape startSRTPipelineIngest used to produce.
	pool := s.framePool.Load()
	const w, h = 320, 240
	size := w * h * 3 / 2
	buf := pool.Acquire()[:size]
	for j := 0; j < size; j++ {
		buf[j] = 40
	}
	pf := &ProcessingFrame{Data: buf, Width: w, Height: h, PTS: 0}
	pf.SetPool(pool)
	s.frameSync.IngestRawVideo("cam1", pf)

	got, _ := s.popProgramFrame(fs, "cam1", clock.Tick{VideoPTS: 3000, ReadPTS: 3000})
	require.NotNil(t, got)

	// Caller-side release, as the transition tick and the lastEncodedFrame
	// swap path do. For an unmanaged frame this used to call
	// pool.Release(Data) while lastRawVideo still aliased the buffer.
	got.ReleaseYUV()

	fs.mu.Lock()
	ss := fs.sources["cam1"]
	fs.mu.Unlock()
	require.NotNil(t, ss)

	ss.mu.Lock()
	require.NotNil(t, ss.lastRawVideo, "freeze-repeat frame must survive the caller-side release")
	require.NotNil(t, ss.lastRawVideo.Data, "lastRawVideo buffer must not have been released")
	retained := &ss.lastRawVideo.Data[0]
	ss.mu.Unlock()

	// Drain the entire pool: if the borrowed copy's release had returned the
	// buffer to the pool, one of these acquires would hand out the memory
	// lastRawVideo still aliases (use-after-release).
	for i := 0; i < 8; i++ {
		acq := pool.Acquire()
		require.NotSame(t, retained, &acq[0],
			"pool handed out a buffer still retained by lastRawVideo (use-after-release)")
	}

	// Freeze-repeat pop must still serve the intact frame.
	frozen, _ := s.popProgramFrame(fs, "cam1", clock.Tick{VideoPTS: 6000, ReadPTS: 6000})
	require.NotNil(t, frozen)
	require.Equal(t, byte(40), frozen.Data[0], "freeze-repeat frame content must be intact")
	frozen.ReleaseYUV()
}
