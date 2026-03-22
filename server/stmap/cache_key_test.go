package stmap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheKey_Deterministic(t *testing.T) {
	params := map[string]float64{"intensity": 0.3, "frequency": 2.0}
	k1 := CacheKey("heat_shimmer", params, 1920, 1080, 90)
	k2 := CacheKey("heat_shimmer", params, 1920, 1080, 90)
	require.Equal(t, k1, k2)
	require.Len(t, k1, 16)
}

func TestCacheKey_DifferentParams(t *testing.T) {
	p1 := map[string]float64{"intensity": 0.3, "frequency": 2.0}
	p2 := map[string]float64{"intensity": 0.5, "frequency": 2.0}
	k1 := CacheKey("heat_shimmer", p1, 1920, 1080, 90)
	k2 := CacheKey("heat_shimmer", p2, 1920, 1080, 90)
	require.NotEqual(t, k1, k2)
}

func TestCacheKey_DifferentResolution(t *testing.T) {
	params := map[string]float64{"intensity": 0.3}
	k1 := CacheKey("heat_shimmer", params, 1920, 1080, 90)
	k2 := CacheKey("heat_shimmer", params, 1280, 720, 90)
	require.NotEqual(t, k1, k2)
}

func TestCacheKey_ParamOrderIndependent(t *testing.T) {
	p1 := map[string]float64{"a": 1.0, "b": 2.0, "c": 3.0}
	p2 := map[string]float64{"c": 3.0, "a": 1.0, "b": 2.0}
	k1 := CacheKey("test", p1, 100, 100, 10)
	k2 := CacheKey("test", p2, 100, 100, 10)
	require.Equal(t, k1, k2)
}

func TestCacheKey_DifferentName(t *testing.T) {
	params := map[string]float64{"intensity": 0.3}
	k1 := CacheKey("heat_shimmer", params, 1920, 1080, 90)
	k2 := CacheKey("dream", params, 1920, 1080, 90)
	require.NotEqual(t, k1, k2)
}

func TestCacheKey_DifferentFrameCount(t *testing.T) {
	params := map[string]float64{"intensity": 0.3}
	k1 := CacheKey("heat_shimmer", params, 1920, 1080, 90)
	k2 := CacheKey("heat_shimmer", params, 1920, 1080, 60)
	require.NotEqual(t, k1, k2)
}

func TestCacheKey_NilParams(t *testing.T) {
	k1 := CacheKey("test", nil, 100, 100, 10)
	k2 := CacheKey("test", map[string]float64{}, 100, 100, 10)
	require.Equal(t, k1, k2)
	require.Len(t, k1, 16)
}

func TestDirComplete(t *testing.T) {
	dir := t.TempDir()

	// Empty dir — not complete.
	require.False(t, DirComplete(dir, 3))

	// Create 2 of 3 files.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "frame_000.lut"), []byte("x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "frame_001.lut"), []byte("x"), 0644))
	require.False(t, DirComplete(dir, 3))

	// Create last file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "frame_002.lut"), []byte("x"), 0644))
	require.True(t, DirComplete(dir, 3))
}

func TestDirComplete_ZeroFrames(t *testing.T) {
	dir := t.TempDir()
	require.True(t, DirComplete(dir, 0))
}

func TestDirComplete_NonexistentDir(t *testing.T) {
	require.False(t, DirComplete("/nonexistent/dir", 1))
}
