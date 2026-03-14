package transition

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetLanczosKernel_ConcurrentAccess(t *testing.T) {
	// NOT parallel — mutates global kernelCache, races with other cache tests.

	// Clear cache to start fresh
	for i := range kernelCache {
		kernelCache[i].Store(nil)
	}

	// Run many goroutines requesting different kernel dimensions concurrently.
	// With -race, this will catch any data races in the cache logic.
	var wg sync.WaitGroup
	dimensions := [][2]int{
		{720, 1080},
		{1080, 720},
		{1920, 1280},
		{1280, 1920},
		{480, 360},
		{360, 480},
		{640, 320},
		{320, 640},
		{100, 200},
		{200, 100},
	}

	// Each dimension pair is requested by multiple goroutines simultaneously
	for _, dim := range dimensions {
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func(srcSize, dstSize int) {
				defer wg.Done()
				k := getLanczosKernel(srcSize, dstSize)
				require.NotNil(t, k)
				require.Equal(t, dstSize, k.size)
			}(dim[0], dim[1])
		}
	}

	wg.Wait()
}

func TestGetLanczosKernel_CacheFull_CompareAndSwap(t *testing.T) {
	// NOT parallel — mutates global kernelCache, races with other cache tests.

	// Clear cache
	for i := range kernelCache {
		kernelCache[i].Store(nil)
	}

	// Fill all cache slots
	for i := 0; i < kernelCacheSize; i++ {
		k := getLanczosKernel(100+i, 200+i)
		require.NotNil(t, k)
	}

	// Verify all slots are occupied
	for i := 0; i < kernelCacheSize; i++ {
		require.NotNil(t, kernelCache[i].Load(), "slot %d should be occupied", i)
	}

	// Request a new dimension pair that isn't cached — should evict via CAS
	k := getLanczosKernel(999, 888)
	require.NotNil(t, k)
	require.Equal(t, 888, k.size)

	// The eviction slot should contain the new entry
	slot := (999*31 + 888) % kernelCacheSize
	entry := kernelCache[slot].Load()
	require.NotNil(t, entry)
	require.Equal(t, 999, entry.srcSize)
	require.Equal(t, 888, entry.dstSize)
}

func TestGetLanczosKernel_CacheHit(t *testing.T) {
	// NOT parallel — mutates global kernelCache.

	// Clear cache
	for i := range kernelCache {
		kernelCache[i].Store(nil)
	}

	// First call computes and caches
	k1 := getLanczosKernel(320, 640)
	require.NotNil(t, k1)

	// Second call should return the same pointer (cache hit)
	k2 := getLanczosKernel(320, 640)
	require.NotNil(t, k2)

	// Same kernel object (pointer equality)
	require.True(t, k1 == k2, "second call should return cached kernel")
}

func TestGetLanczosKernel_EmptySlotFill_CompareAndSwap(t *testing.T) {
	// NOT parallel — mutates global kernelCache.

	// Clear cache
	for i := range kernelCache {
		kernelCache[i].Store(nil)
	}

	// Concurrent requests for the same dimensions should all succeed
	// and the cache should contain exactly one entry for those dimensions.
	var wg sync.WaitGroup
	kernels := make([]*lanczosKernel, 8)

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			kernels[idx] = getLanczosKernel(256, 512)
		}(i)
	}
	wg.Wait()

	// All should be valid
	for i, k := range kernels {
		require.NotNil(t, k, "kernel %d should not be nil", i)
		require.Equal(t, 512, k.size, "kernel %d should have correct size", i)
	}

	// At least one cache slot should hold (256, 512). With concurrent CAS,
	// multiple goroutines may each claim a different empty slot before any
	// cache hit is possible, so duplicates are expected and harmless.
	count := 0
	for i := range kernelCache {
		if c := kernelCache[i].Load(); c != nil && c.srcSize == 256 && c.dstSize == 512 {
			count++
		}
	}
	require.GreaterOrEqual(t, count, 1, "cache should contain at least one entry for (256, 512)")
}
