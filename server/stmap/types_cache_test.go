package stmap

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnimatedSTMap_BuildProcessorsCached(t *testing.T) {
	dir := t.TempDir()

	anim, err := GenerateAnimated("heat_shimmer",
		map[string]float64{"intensity": 0.3, "frequency": 2.0},
		64, 48, 5)
	require.NoError(t, err)
	require.Len(t, anim.Frames, 5)

	anim.BuildProcessorsCached(dir)

	// Frames should be freed.
	require.Nil(t, anim.Frames)

	// Processors should work.
	src := make([]byte, 64*48*3/2)
	dst := make([]byte, len(src))
	p := anim.ProcessorAt(0)
	require.True(t, p.Active())
	p.ProcessYUV(dst, src, 64, 48)

	// Cache files should exist.
	for i := 0; i < 5; i++ {
		path := filepath.Join(dir, fmt.Sprintf("frame_%03d.lut", i))
		_, statErr := os.Stat(path)
		require.NoError(t, statErr)
	}

	require.NoError(t, anim.Close())
}

func TestAnimatedSTMap_BuildProcessorsCached_LoadsExisting(t *testing.T) {
	dir := t.TempDir()
	params := map[string]float64{"intensity": 0.3, "frequency": 2.0}

	// First: generate and cache.
	anim1, err := GenerateAnimated("heat_shimmer", params, 64, 48, 5)
	require.NoError(t, err)
	anim1.BuildProcessorsCached(dir)

	src := make([]byte, 64*48*3/2)
	for i := range src {
		src[i] = byte(i % 256)
	}
	ref := make([]byte, len(src))
	anim1.ProcessorAt(2).ProcessYUV(ref, src, 64, 48)
	require.NoError(t, anim1.Close())

	// Second: load from cache.
	anim2, err := GenerateAnimated("heat_shimmer", params, 64, 48, 5)
	require.NoError(t, err)
	anim2.BuildProcessorsCached(dir)

	out := make([]byte, len(src))
	anim2.ProcessorAt(2).ProcessYUV(out, src, 64, 48)
	require.Equal(t, ref, out)
	require.NoError(t, anim2.Close())
}

func TestAnimatedSTMap_Close_Idempotent(t *testing.T) {
	dir := t.TempDir()
	anim, err := GenerateAnimated("heat_shimmer",
		map[string]float64{"intensity": 0.3, "frequency": 2.0},
		64, 48, 3)
	require.NoError(t, err)
	anim.BuildProcessorsCached(dir)
	require.NoError(t, anim.Close())
	require.NoError(t, anim.Close()) // idempotent
}

func TestAnimatedSTMap_ProcessorAt_AfterCachedBuild(t *testing.T) {
	dir := t.TempDir()
	anim, err := GenerateAnimated("heat_shimmer",
		map[string]float64{"intensity": 0.3, "frequency": 2.0},
		64, 48, 3)
	require.NoError(t, err)
	anim.BuildProcessorsCached(dir)

	// Frames is nil, but ProcessorAt should still work via len(processors).
	require.Nil(t, anim.Frames)
	require.NotNil(t, anim.ProcessorAt(0))
	require.NotNil(t, anim.ProcessorAt(1))
	require.NotNil(t, anim.ProcessorAt(2))
	// Wrapping: index 3 should return processor 0.
	require.Equal(t, anim.ProcessorAt(0), anim.ProcessorAt(3))

	require.NoError(t, anim.Close())
}

func TestAnimatedSTMap_BuildProcessorsCached_OnceSemantics(t *testing.T) {
	dir := t.TempDir()
	anim, err := GenerateAnimated("heat_shimmer",
		map[string]float64{"intensity": 0.3, "frequency": 2.0},
		64, 48, 3)
	require.NoError(t, err)

	// Build twice — second call is a no-op.
	anim.BuildProcessorsCached(dir)
	anim.BuildProcessorsCached(dir)

	require.Len(t, anim.processors, 3)
	require.NoError(t, anim.Close())
}
