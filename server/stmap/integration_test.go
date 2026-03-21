package stmap

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// makeGradientYUV420 creates a YUV420 frame with a horizontal gradient in Y
// and flat chroma. Useful for verifying that warps actually change pixel data.
func makeGradientYUV420(w, h int) []byte {
	ySize := w * h
	cw := w / 2
	ch := h / 2
	cSize := cw * ch
	buf := make([]byte, ySize+2*cSize)

	// Y plane: horizontal gradient 0..255
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			buf[y*w+x] = byte(x * 255 / (w - 1))
		}
	}
	// Cb plane: 128 (neutral)
	for i := 0; i < cSize; i++ {
		buf[ySize+i] = 128
	}
	// Cr plane: 128 (neutral)
	for i := 0; i < cSize; i++ {
		buf[ySize+cSize+i] = 128
	}
	return buf
}

func TestIntegration_GenerateAndAssignSource(t *testing.T) {
	reg := NewRegistry()

	// Generate barrel correction map.
	m, err := Generate("barrel", map[string]float64{"k1": -0.3}, 64, 64)
	require.NoError(t, err)
	m.Name = "barrel"
	require.NoError(t, reg.Store(m))

	// Assign to source.
	require.NoError(t, reg.AssignSource("cam1", "barrel"))

	// Verify source processor exists.
	proc := reg.SourceProcessor("cam1")
	require.NotNil(t, proc)
	require.True(t, proc.Active())

	// Create a test YUV frame and apply the warp.
	src := makeGradientYUV420(64, 64)
	dst := make([]byte, len(src))
	proc.ProcessYUV(dst, src, 64, 64)

	// Barrel distortion should change pixel positions -- the output should
	// differ from the input (except for edge clamping).
	differ := false
	for i := range dst {
		if dst[i] != src[i] {
			differ = true
			break
		}
	}
	require.True(t, differ, "barrel warp should modify the frame")
}

func TestIntegration_AnimatedProgramMap(t *testing.T) {
	reg := NewRegistry()

	// Generate heat_shimmer animated map.
	anim, err := GenerateAnimated("heat_shimmer", map[string]float64{
		"intensity": 0.5,
		"frequency": 2.0,
	}, 64, 64, 30)
	require.NoError(t, err)
	anim.Name = "shimmer"
	require.NoError(t, reg.StoreAnimated(anim))

	// Assign to program.
	require.NoError(t, reg.AssignProgram("shimmer"))
	require.True(t, reg.HasProgramMap())

	// Verify animated frame reference.
	animRef := reg.ProgramAnimatedFrame()
	require.NotNil(t, animRef)

	// Advance multiple frames and verify they differ (animation cycles).
	// Compare frames at widely separated phase positions.
	frame0 := animRef.FrameAt(0)
	frame15 := animRef.FrameAt(15) // half cycle

	// Compare T coordinate values -- heat_shimmer displaces vertically.
	differCount := 0
	for i := range frame0.T {
		if frame0.T[i] != frame15.T[i] {
			differCount++
		}
	}
	require.Greater(t, differCount, 0,
		"animated frames at different phases should have different T coordinates")

	// Verify ProcessorAt works and is cached.
	proc0 := animRef.ProcessorAt(0)
	proc0b := animRef.ProcessorAt(0)
	require.Same(t, proc0, proc0b, "ProcessorAt should return cached instance")
}

func TestIntegration_StoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Generate a barrel map.
	m, err := Generate("barrel", map[string]float64{"k1": -0.25}, 32, 32)
	require.NoError(t, err)
	m.Name = "barrel-test"

	// Save to store.
	require.NoError(t, store.SaveStatic(m))

	// Load from store.
	loaded, err := store.LoadStatic("barrel-test")
	require.NoError(t, err)

	// Verify dimensions match.
	require.Equal(t, m.Width, loaded.Width)
	require.Equal(t, m.Height, loaded.Height)
	require.Equal(t, len(m.S), len(loaded.S))
	require.Equal(t, len(m.T), len(loaded.T))

	// Verify coordinate values match (within float32 precision).
	for i := range m.S {
		require.InDelta(t, m.S[i], loaded.S[i], 1e-6, "S[%d] mismatch", i)
		require.InDelta(t, m.T[i], loaded.T[i], 1e-6, "T[%d] mismatch", i)
	}

	// Apply both to the same frame and verify identical output.
	src := makeGradientYUV420(32, 32)
	dst1 := make([]byte, len(src))
	dst2 := make([]byte, len(src))

	proc1 := NewProcessor(m)
	proc2 := NewProcessor(loaded)
	proc1.ProcessYUV(dst1, src, 32, 32)
	proc2.ProcessYUV(dst2, src, 32, 32)

	require.Equal(t, dst1, dst2, "original and loaded map should produce identical output")
}

func TestIntegration_IdentityMapPassthrough(t *testing.T) {
	reg := NewRegistry()

	// Generate an identity map.
	m, err := Generate("identity", nil, 32, 32)
	require.NoError(t, err)
	m.Name = "id"
	require.NoError(t, reg.Store(m))

	// Apply to frame -- identity should produce near-identical output
	// (bilinear interpolation may introduce tiny rounding).
	src := makeGradientYUV420(32, 32)
	dst := make([]byte, len(src))

	proc := NewProcessor(m)
	proc.ProcessYUV(dst, src, 32, 32)

	// Check Y plane values are within 1 of original (bilinear rounding).
	ySize := 32 * 32
	for i := 0; i < ySize; i++ {
		diff := int(dst[i]) - int(src[i])
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, diff, 1,
			"identity map pixel %d: src=%d dst=%d", i, src[i], dst[i])
	}
}

func TestIntegration_RegistryPersistence(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// === Phase 1: populate registry and persist ===

	reg1 := NewRegistry()

	// Static map.
	barrel, err := Generate("barrel", map[string]float64{"k1": -0.2}, 32, 32)
	require.NoError(t, err)
	barrel.Name = "barrel"
	require.NoError(t, reg1.Store(barrel))
	require.NoError(t, store.SaveStatic(barrel))

	// Another static map.
	pin, err := Generate("pincushion", map[string]float64{"k1": 0.2}, 32, 32)
	require.NoError(t, err)
	pin.Name = "pincushion"
	require.NoError(t, reg1.Store(pin))
	require.NoError(t, store.SaveStatic(pin))

	// Animated map.
	shimmer, err := GenerateAnimated("heat_shimmer", map[string]float64{
		"intensity": 0.3,
	}, 32, 32, 10)
	require.NoError(t, err)
	shimmer.Name = "shimmer"
	require.NoError(t, reg1.StoreAnimated(shimmer))
	require.NoError(t, store.SaveAnimatedMeta(shimmer, "heat_shimmer", map[string]float64{
		"intensity": 0.3,
	}))

	// Assign to sources and program.
	require.NoError(t, reg1.AssignSource("cam1", "barrel"))
	require.NoError(t, reg1.AssignSource("cam2", "pincushion"))
	require.NoError(t, reg1.AssignProgram("shimmer"))

	state1 := reg1.State()
	require.Equal(t, "barrel", state1.Sources["cam1"])
	require.Equal(t, "pincushion", state1.Sources["cam2"])
	require.NotNil(t, state1.Program)
	require.Equal(t, "shimmer", state1.Program.Map)
	require.Len(t, state1.Available, 3)

	// === Phase 2: simulate restart — new registry, load from store ===

	reg2 := NewRegistry()

	// Load static maps.
	staticNames, err := store.ListStatic()
	require.NoError(t, err)
	for _, name := range staticNames {
		m, loadErr := store.LoadStatic(name)
		require.NoError(t, loadErr)
		require.NoError(t, reg2.Store(m))
	}

	// Load animated maps.
	animNames, err := store.ListAnimated()
	require.NoError(t, err)
	for _, name := range animNames {
		meta, loadErr := store.LoadAnimatedMeta(name)
		require.NoError(t, loadErr)
		anim, genErr := GenerateAnimated(meta.Generator, meta.Params, meta.Width, meta.Height, meta.FrameCount)
		require.NoError(t, genErr)
		anim.Name = name
		require.NoError(t, reg2.StoreAnimated(anim))
	}

	// Verify all maps are available in the new registry.
	state2 := reg2.State()
	require.Len(t, state2.Available, 3)
	require.Contains(t, state2.Available, "barrel")
	require.Contains(t, state2.Available, "pincushion")
	require.Contains(t, state2.Available, "shimmer")

	// Re-assign sources (assignments are runtime state, not persisted).
	require.NoError(t, reg2.AssignSource("cam1", "barrel"))
	proc := reg2.SourceProcessor("cam1")
	require.NotNil(t, proc)
	require.True(t, proc.Active())

	// Verify the loaded barrel map produces the same warp as original.
	src := makeGradientYUV420(32, 32)
	dst1 := make([]byte, len(src))
	dst2 := make([]byte, len(src))

	reg1.SourceProcessor("cam1").ProcessYUV(dst1, src, 32, 32)
	reg2.SourceProcessor("cam1").ProcessYUV(dst2, src, 32, 32)

	require.Equal(t, dst1, dst2, "reloaded map should produce identical output")
}

func TestIntegration_AnimatedMeta_StoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Generate animated map.
	params := map[string]float64{"intensity": 0.5, "frequency": 3.0}
	anim, err := GenerateAnimated("heat_shimmer", params, 48, 48, 20)
	require.NoError(t, err)
	anim.Name = "my-shimmer"

	// Save meta.
	require.NoError(t, store.SaveAnimatedMeta(anim, "heat_shimmer", params))

	// Load meta.
	meta, err := store.LoadAnimatedMeta("my-shimmer")
	require.NoError(t, err)
	require.Equal(t, "heat_shimmer", meta.Generator)
	require.Equal(t, 48, meta.Width)
	require.Equal(t, 48, meta.Height)
	require.Equal(t, 20, meta.FrameCount)
	require.InDelta(t, 0.5, meta.Params["intensity"], 1e-9)
	require.InDelta(t, 3.0, meta.Params["frequency"], 1e-9)

	// Regenerate from meta.
	anim2, err := GenerateAnimated(meta.Generator, meta.Params, meta.Width, meta.Height, meta.FrameCount)
	require.NoError(t, err)
	require.Len(t, anim2.Frames, 20)
	require.Equal(t, 48, anim2.Frames[0].Width)
}

func TestIntegration_DeleteCleansAssignments(t *testing.T) {
	reg := NewRegistry()

	m, err := Generate("barrel", map[string]float64{"k1": -0.3}, 32, 32)
	require.NoError(t, err)
	m.Name = "barrel"
	require.NoError(t, reg.Store(m))
	require.NoError(t, reg.AssignSource("cam1", "barrel"))
	require.NoError(t, reg.AssignProgram("barrel"))

	// Verify assignments exist.
	_, ok := reg.SourceMap("cam1")
	require.True(t, ok)
	require.True(t, reg.HasProgramMap())

	// Delete the map.
	require.NoError(t, reg.Delete("barrel"))

	// Verify assignments are cleared.
	_, ok = reg.SourceMap("cam1")
	require.False(t, ok)
	require.False(t, reg.HasProgramMap())
	require.Nil(t, reg.SourceProcessor("cam1"))
	require.Nil(t, reg.ProgramProcessor())
}

func TestIntegration_StoreDeleteFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	m, err := Generate("identity", nil, 16, 16)
	require.NoError(t, err)
	m.Name = "temp"

	require.NoError(t, store.SaveStatic(m))

	// Verify file exists.
	_, err = os.Stat(filepath.Join(dir, "temp.stmap"))
	require.NoError(t, err)

	// Delete via store.
	require.NoError(t, store.Delete("temp"))

	// Verify file is gone.
	_, err = os.Stat(filepath.Join(dir, "temp.stmap"))
	require.True(t, os.IsNotExist(err))

	// Delete again should return not found.
	require.ErrorIs(t, store.Delete("temp"), ErrNotFound)
}

func TestIntegration_StateCallbackFires(t *testing.T) {
	reg := NewRegistry()

	callCount := 0
	var lastState STMapState
	reg.SetOnStateChange(func(s STMapState) {
		callCount++
		lastState = s
	})

	m, err := Generate("identity", nil, 16, 16)
	require.NoError(t, err)
	m.Name = "ident"
	require.NoError(t, reg.Store(m))

	require.Equal(t, 1, callCount)
	require.Contains(t, lastState.Available, "ident")

	require.NoError(t, reg.AssignSource("cam1", "ident"))
	require.Equal(t, 2, callCount)
	require.Equal(t, "ident", lastState.Sources["cam1"])

	require.NoError(t, reg.AssignProgram("ident"))
	require.Equal(t, 3, callCount)
	require.NotNil(t, lastState.Program)
	require.Equal(t, "ident", lastState.Program.Map)
}

func TestIntegration_AllGeneratorsProduceValidMaps(t *testing.T) {
	w, h := 32, 32

	// Test all static generators.
	for _, name := range ListGenerators() {
		t.Run("static/"+name, func(t *testing.T) {
			m, err := Generate(name, nil, w, h) // defaults
			require.NoError(t, err)
			require.Equal(t, w, m.Width)
			require.Equal(t, h, m.Height)
			require.Len(t, m.S, w*h)
			require.Len(t, m.T, w*h)

			// Verify coordinates are valid (no NaN or Inf).
			for i, s := range m.S {
				require.False(t, math.IsNaN(float64(s)), "S[%d] is NaN", i)
				require.False(t, math.IsInf(float64(s), 0), "S[%d] is Inf", i)
			}
			for i, tv := range m.T {
				require.False(t, math.IsNaN(float64(tv)), "T[%d] is NaN", i)
				require.False(t, math.IsInf(float64(tv), 0), "T[%d] is Inf", i)
			}

			// Verify ProcessYUV doesn't panic.
			src := makeGradientYUV420(w, h)
			dst := make([]byte, len(src))
			proc := NewProcessor(m)
			proc.ProcessYUV(dst, src, w, h)
		})
	}

	// Test all animated generators.
	for _, name := range ListAnimatedGenerators() {
		t.Run("animated/"+name, func(t *testing.T) {
			anim, err := GenerateAnimated(name, nil, w, h, 10) // defaults
			require.NoError(t, err)
			require.NotEmpty(t, anim.Frames)

			for fi, frame := range anim.Frames {
				require.Equal(t, w, frame.Width, "frame %d width", fi)
				require.Equal(t, h, frame.Height, "frame %d height", fi)
				require.Len(t, frame.S, w*h, "frame %d S length", fi)
				require.Len(t, frame.T, w*h, "frame %d T length", fi)
			}

			// Verify ProcessorAt doesn't panic on all frames.
			src := makeGradientYUV420(w, h)
			dst := make([]byte, len(src))
			for i := 0; i < len(anim.Frames); i++ {
				proc := anim.ProcessorAt(i)
				proc.ProcessYUV(dst, src, w, h)
			}
		})
	}
}

// sumFloat32 computes the sum of a float32 slice, used for quick difference checks.
func sumFloat32(s []float32) float64 {
	var sum float64
	for _, v := range s {
		sum += float64(v)
	}
	return sum
}
