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
	src := make([]byte, yuvFrameSize(64, 48))
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

	src := make([]byte, yuvFrameSize(64, 48))
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

func TestAnimatedSTMap_STArraysAt_FromMmap(t *testing.T) {
	params := map[string]float64{"intensity": 0.3, "frequency": 2.0}
	w, h, frameCount := 64, 48, 5

	// Generate with frames in memory — capture original S/T data.
	orig, err := GenerateAnimated("heat_shimmer", params, w, h, frameCount)
	require.NoError(t, err)

	origS := make([][]float32, frameCount)
	origT := make([][]float32, frameCount)
	for i := 0; i < frameCount; i++ {
		origS[i] = make([]float32, len(orig.Frames[i].S))
		origT[i] = make([]float32, len(orig.Frames[i].T))
		copy(origS[i], orig.Frames[i].S)
		copy(origT[i], orig.Frames[i].T)
	}

	// STArraysAt before cache build reads from Frames.
	s, tArr := orig.STArraysAt(2)
	require.NotNil(t, s)
	require.Equal(t, origS[2], s)
	require.Equal(t, origT[2], tArr)

	// Build cached — writes .st files and mmap's them.
	dir := t.TempDir()
	orig.BuildProcessorsCached(dir)
	require.Nil(t, orig.Frames) // float32 data freed

	// .st files should exist.
	for i := 0; i < frameCount; i++ {
		path := filepath.Join(dir, fmt.Sprintf("frame_%03d.st", i))
		_, statErr := os.Stat(path)
		require.NoError(t, statErr, "missing .st file for frame %d", i)
	}

	// STArraysAt should serve from mmap'd .st files.
	for i := 0; i < frameCount; i++ {
		s, tArr := orig.STArraysAt(i)
		require.NotNil(t, s, "frame %d S should not be nil", i)
		require.NotNil(t, tArr, "frame %d T should not be nil", i)
		require.Equal(t, origS[i], s, "frame %d S mismatch", i)
		require.Equal(t, origT[i], tArr, "frame %d T mismatch", i)
	}

	// Wrapping works.
	s, _ = orig.STArraysAt(frameCount + 2)
	require.Equal(t, origS[2], s)

	require.NoError(t, orig.Close())
}

func TestAnimatedSTMap_STArraysAt_NoCache(t *testing.T) {
	// AnimatedSTMap without cached build — STArraysAt falls back to Frames.
	f0 := Identity(8, 8)
	f1 := Identity(8, 8)
	anim := NewAnimatedSTMap("manual", []*STMap{f0, f1}, 30)

	s, tArr := anim.STArraysAt(0)
	require.NotNil(t, s)
	require.NotNil(t, tArr)
	require.Len(t, s, 64) // 8*8

	// After niling Frames with no stArrays, returns nil.
	anim.Frames = nil
	s, tArr = anim.STArraysAt(0)
	require.Nil(t, s)
	require.Nil(t, tArr)
}

func TestAnimatedSTMap_FrameCount(t *testing.T) {
	f0 := Identity(8, 8)
	f1 := Identity(8, 8)
	anim := NewAnimatedSTMap("test", []*STMap{f0, f1}, 30)

	// Before BuildProcessors: FrameCount from Frames.
	require.Equal(t, 2, anim.FrameCount())

	// After BuildProcessors: FrameCount from processors.
	anim.BuildProcessors()
	require.Equal(t, 2, anim.FrameCount())

	// After niling Frames (simulating BuildProcessorsCached): still correct.
	anim.Frames = nil
	require.Equal(t, 2, anim.FrameCount())
}

func TestAnimatedSTMap_GenerateAnimated_SetsMetadata(t *testing.T) {
	anim, err := GenerateAnimated("heat_shimmer",
		map[string]float64{"intensity": 0.5},
		128, 96, 10)
	require.NoError(t, err)

	require.Equal(t, "heat_shimmer", anim.Generator)
	require.Equal(t, 128, anim.Width)
	require.Equal(t, 96, anim.Height)
	require.NotNil(t, anim.GenParams)
	require.Equal(t, 0.5, anim.GenParams["intensity"])
	// frequency should have been resolved to its default.
	require.Equal(t, 2.0, anim.GenParams["frequency"])
}

func TestRegistry_AnimatedSTArraysAt_AfterBuildProcessorsCached(t *testing.T) {
	r := NewRegistry()
	dir := t.TempDir()

	params := map[string]float64{"intensity": 0.3, "frequency": 2.0}
	anim, err := GenerateAnimated("heat_shimmer", params, 64, 48, 5)
	require.NoError(t, err)

	// Capture original frame 2 S/T data before cache build frees them.
	origS := make([]float32, len(anim.Frames[2].S))
	origT := make([]float32, len(anim.Frames[2].T))
	copy(origS, anim.Frames[2].S)
	copy(origT, anim.Frames[2].T)

	require.NoError(t, r.StoreAnimated(anim))
	require.NoError(t, r.AssignProgram("heat_shimmer"))

	// Build cached processors — this frees Frames.
	anim.BuildProcessorsCached(dir)
	require.Nil(t, anim.Frames)

	// AnimatedSTArraysAt should return valid data from mmap'd .st files.
	s, tArr := r.AnimatedSTArraysAt(2)
	require.NotNil(t, s)
	require.NotNil(t, tArr)
	require.Equal(t, origS, s)
	require.Equal(t, origT, tArr)

	// Negative index returns nil.
	s, tArr = r.AnimatedSTArraysAt(-1)
	require.Nil(t, s)
	require.Nil(t, tArr)

	require.NoError(t, anim.Close())
}
