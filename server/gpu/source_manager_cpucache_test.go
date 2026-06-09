//go:build cgo && darwin

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// entryForSource fetches the internal entry for white-box assertions.
func entryForSource(m *GPUSourceManager, key string) *gpuSourceEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sources[key]
}

// TestGPUSourceManager_IngestYUV_NoCPUCacheWithoutReader verifies finding #39:
// IngestYUV must not allocate and copy a full CPU frame (~3.1 MB at 1080p)
// per ingest for the lastCPU cache when nothing ever reads it. The cache is
// only populated once a ReadCachedFrame caller (playout live-source tap) has
// shown interest in the source.
func TestGPUSourceManager_IngestYUV_NoCPUCacheWithoutReader(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	yuv := createTestYUV420p(w, h, 200, 100, 150)
	for i := 0; i < 5; i++ {
		mgr.IngestYUV("cam1", yuv, w, h, int64(i+1)*3000)
	}

	entry := entryForSource(mgr, "cam1")
	require.NotNil(t, entry)
	assert.Nil(t, entry.lastCPU.Load(),
		"IngestYUV must not populate the CPU frame cache when no ReadCachedFrame caller exists")

	// GPU side is unaffected.
	frame := mgr.GetFrame("cam1")
	require.NotNil(t, frame)
	frame.Release()
}

// TestGPUSourceManager_ReadCachedFrame_EnablesCache verifies the lazy-enable
// contract: the first ReadCachedFrame call for a source marks the cache as
// wanted (and may return nil); subsequent ingests populate it.
func TestGPUSourceManager_ReadCachedFrame_EnablesCache(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	yuv := createTestYUV420p(w, h, 200, 100, 150)
	mgr.IngestYUV("cam1", yuv, w, h, 42000)

	// First read: enables caching for this source (may return nil — no
	// cache existed at ingest time).
	mgr.ReadCachedFrame("cam1")

	// Next ingest populates the cache; the read after it must succeed.
	mgr.IngestYUV("cam1", yuv, w, h, 84000)
	got, rw, rh, pts := mgr.ReadCachedFrame("cam1")
	require.NotNil(t, got, "cache must be populated after a reader showed interest")
	assert.Equal(t, w, rw)
	assert.Equal(t, h, rh)
	assert.Equal(t, int64(84000), pts)

	centerIdx := (h/2)*w + w/2
	assert.Equal(t, byte(200), got[centerIdx])
}

// TestGPUSourceManager_AliasSource_PropagatesCacheInterest verifies that a
// tap reading the alias destination (ReadCachedFrame on the playout channel
// key) causes the alias SOURCE to start populating its CPU cache, so the
// aliased cache becomes available.
func TestGPUSourceManager_AliasSource_PropagatesCacheInterest(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	mgr.RegisterSource("cam1", w, h, nil)
	mgr.RegisterSource("playout:1", w, h, nil)

	// Tap shows interest in the destination only.
	mgr.ReadCachedFrame("playout:1")

	yuv := createTestYUV420p(w, h, 200, 100, 150)
	mgr.IngestYUV("cam1", yuv, w, h, 42000)

	// First alias propagates the interest dst -> src (cache may still be
	// empty at this point).
	require.True(t, mgr.AliasSource("playout:1", "cam1"))

	// Next ingest populates cam1's cache; the next alias copies it to dst.
	mgr.IngestYUV("cam1", yuv, w, h, 84000)
	require.True(t, mgr.AliasSource("playout:1", "cam1"))

	got, rw, rh, pts := mgr.ReadCachedFrame("playout:1")
	require.NotNil(t, got, "aliased CPU cache should reach the destination")
	assert.Equal(t, w, rw)
	assert.Equal(t, h, rh)
	assert.Equal(t, int64(84000), pts)
}
