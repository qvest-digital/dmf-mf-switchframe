package stmap

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMmapCacheLifecycle(t *testing.T) {
	cacheDir := t.TempDir()
	registry := NewRegistry()

	params := map[string]float64{"intensity": 0.3, "frequency": 2.0}
	frameCount := 5
	w, h := 64, 48

	// Step 1: Generate and cache.
	anim, err := GenerateAnimated("heat_shimmer", params, w, h, frameCount)
	require.NoError(t, err)

	key := CacheKey("heat_shimmer", params, w, h, frameCount)
	lutDir := filepath.Join(cacheDir, "heat_shimmer_"+key)
	anim.BuildProcessorsCached(lutDir)

	// Verify Frames freed.
	require.Nil(t, anim.Frames)

	// Verify cache files exist.
	require.True(t, DirComplete(lutDir, frameCount))

	// Step 2: Store in registry and assign to program.
	err = registry.StoreAnimated(anim)
	require.NoError(t, err)

	err = registry.AssignProgram("heat_shimmer")
	require.NoError(t, err)
	require.True(t, registry.HasProgramMap())

	// Step 3: Use processors (simulate pipeline frames).
	src := make([]byte, w*h*3/2)
	for i := range src {
		src[i] = byte(i % 256)
	}
	dst := make([]byte, len(src))

	idx := registry.AdvanceAnimatedIndex()
	require.GreaterOrEqual(t, idx, 0)

	animRef := registry.ProgramAnimatedFrame()
	require.NotNil(t, animRef)
	p := animRef.ProcessorAt(idx)
	require.True(t, p.Active())
	p.ProcessYUV(dst, src, w, h)

	// Verify GPU path (float32 regeneration) works.
	s, tArr := registry.AnimatedSTArraysAt(idx)
	require.NotNil(t, s)
	require.NotNil(t, tArr)
	require.Len(t, s, w*h)

	// Step 4: Remove from program (disable — processors stay mapped).
	registry.RemoveProgram()
	require.False(t, registry.HasProgramMap())

	// Step 5: Re-enable (instant — processors already mapped).
	err = registry.AssignProgram("heat_shimmer")
	require.NoError(t, err)
	require.True(t, registry.HasProgramMap())

	// Step 6: Delete from registry (munmaps processors).
	err = registry.Delete("heat_shimmer")
	require.NoError(t, err)
	require.False(t, registry.HasProgramMap())

	// Cache files should still exist on disk (for reuse).
	require.True(t, DirComplete(lutDir, frameCount))

	// Step 7: Re-generate and load from cache (instant — files on disk).
	anim2, err := GenerateAnimated("heat_shimmer", params, w, h, frameCount)
	require.NoError(t, err)
	anim2.BuildProcessorsCached(lutDir) // should load from cache, not recompute

	// Verify it produces same output.
	dst2 := make([]byte, len(src))
	p2 := anim2.ProcessorAt(idx % frameCount)
	p2.ProcessYUV(dst2, src, w, h)
	require.Equal(t, dst, dst2)

	require.NoError(t, anim2.Close())
}

func TestRegistryDelete_ClosesAnimatedMmap(t *testing.T) {
	cacheDir := t.TempDir()
	registry := NewRegistry()

	params := map[string]float64{"intensity": 0.5}
	frameCount := 3
	w, h := 32, 32

	// Generate, cache, store, assign.
	anim, err := GenerateAnimated("heat_shimmer", params, w, h, frameCount)
	require.NoError(t, err)

	key := CacheKey("heat_shimmer", params, w, h, frameCount)
	lutDir := filepath.Join(cacheDir, "heat_shimmer_"+key)
	anim.BuildProcessorsCached(lutDir)

	require.NoError(t, registry.StoreAnimated(anim))
	require.NoError(t, registry.AssignProgram("heat_shimmer"))

	// Verify processor is active before delete.
	animRef := registry.ProgramAnimatedFrame()
	require.NotNil(t, animRef)
	p := animRef.ProcessorAt(0)
	require.True(t, p.Active())

	// Delete should close mmap-backed processors.
	require.NoError(t, registry.Delete("heat_shimmer"))

	// After Close(), the processor's LUT slices should be nil.
	require.False(t, p.Active())
}

func TestRegistryDelete_StaticMap_NoMmapClose(t *testing.T) {
	// Verify that deleting a static map does not panic
	// (static maps have no mmap backing to close).
	registry := NewRegistry()

	m := Identity(32, 32)
	m.Name = "barrel"
	require.NoError(t, registry.Store(m))
	require.NoError(t, registry.AssignProgram("barrel"))

	require.NoError(t, registry.Delete("barrel"))
	require.False(t, registry.HasProgramMap())
}

func TestRemoveProgram_DoesNotCloseMmap(t *testing.T) {
	cacheDir := t.TempDir()
	registry := NewRegistry()

	params := map[string]float64{"intensity": 0.3}
	frameCount := 3
	w, h := 32, 32

	anim, err := GenerateAnimated("heat_shimmer", params, w, h, frameCount)
	require.NoError(t, err)

	key := CacheKey("heat_shimmer", params, w, h, frameCount)
	lutDir := filepath.Join(cacheDir, fmt.Sprintf("heat_shimmer_%s", key))
	anim.BuildProcessorsCached(lutDir)

	require.NoError(t, registry.StoreAnimated(anim))
	require.NoError(t, registry.AssignProgram("heat_shimmer"))

	// RemoveProgram should NOT close mmap processors.
	registry.RemoveProgram()
	require.False(t, registry.HasProgramMap())

	// Processors should still be active (mmap still mapped).
	p := anim.ProcessorAt(0)
	require.True(t, p.Active())

	// Re-assign should work without regeneration.
	require.NoError(t, registry.AssignProgram("heat_shimmer"))
	require.True(t, registry.HasProgramMap())

	// Clean up.
	require.NoError(t, registry.Delete("heat_shimmer"))
}
