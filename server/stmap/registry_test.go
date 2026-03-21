package stmap

import (
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistry_StoreAndGet(t *testing.T) {
	r := NewRegistry()

	m := Identity(8, 8)
	m.Name = "barrel"
	require.NoError(t, r.Store(m))

	got, ok := r.Get("barrel")
	require.True(t, ok)
	require.Equal(t, m, got)

	// Not found.
	_, ok = r.Get("nonexistent")
	require.False(t, ok)
}

func TestRegistry_Store_ValidatesName(t *testing.T) {
	r := NewRegistry()

	m := Identity(8, 8)
	m.Name = "../bad"
	err := r.Store(m)
	require.ErrorIs(t, err, ErrInvalidName)

	m.Name = ""
	err = r.Store(m)
	require.ErrorIs(t, err, ErrInvalidName)
}

func TestRegistry_Store_RebuildsCachedProcessors(t *testing.T) {
	r := NewRegistry()

	// Store initial map and assign to source.
	m := Identity(8, 8)
	m.Name = "barrel"
	require.NoError(t, r.Store(m))
	require.NoError(t, r.AssignSource("cam1", "barrel"))

	proc1 := r.SourceProcessor("cam1")
	require.NotNil(t, proc1)

	// Re-store the same name with a different map (update).
	m2 := Identity(16, 16)
	m2.Name = "barrel"
	require.NoError(t, r.Store(m2))

	// The cached processor should have been rebuilt.
	proc2 := r.SourceProcessor("cam1")
	require.NotNil(t, proc2)
	require.NotSame(t, proc1, proc2)
}

func TestRegistry_StoreAnimated(t *testing.T) {
	r := NewRegistry()

	f0 := Identity(8, 8)
	f1 := Identity(8, 8)
	anim := NewAnimatedSTMap("warp", []*STMap{f0, f1}, 30)

	require.NoError(t, r.StoreAnimated(anim))

	got, ok := r.GetAnimated("warp")
	require.True(t, ok)
	require.Equal(t, anim, got)

	// Not found.
	_, ok = r.GetAnimated("nonexistent")
	require.False(t, ok)
}

func TestRegistry_StoreAnimated_ValidatesName(t *testing.T) {
	r := NewRegistry()

	f0 := Identity(8, 8)
	anim := NewAnimatedSTMap("", []*STMap{f0}, 30)
	err := r.StoreAnimated(anim)
	require.ErrorIs(t, err, ErrInvalidName)
}

func TestRegistry_AssignSource(t *testing.T) {
	r := NewRegistry()

	m := Identity(8, 8)
	m.Name = "barrel"
	require.NoError(t, r.Store(m))

	require.NoError(t, r.AssignSource("cam1", "barrel"))

	name, ok := r.SourceMap("cam1")
	require.True(t, ok)
	require.Equal(t, "barrel", name)

	proc := r.SourceProcessor("cam1")
	require.NotNil(t, proc)
	require.True(t, proc.Active())
}

func TestRegistry_AssignSource_Animated(t *testing.T) {
	r := NewRegistry()

	f0 := Identity(8, 8)
	anim := NewAnimatedSTMap("warp", []*STMap{f0}, 30)
	require.NoError(t, r.StoreAnimated(anim))

	// Animated maps can be assigned to sources too; processor built from current frame.
	require.NoError(t, r.AssignSource("cam1", "warp"))

	name, ok := r.SourceMap("cam1")
	require.True(t, ok)
	require.Equal(t, "warp", name)

	proc := r.SourceProcessor("cam1")
	require.NotNil(t, proc)
	require.True(t, proc.Active())
}

func TestRegistry_AssignSource_NotFound(t *testing.T) {
	r := NewRegistry()

	err := r.AssignSource("cam1", "nonexistent")
	require.ErrorIs(t, err, ErrNotFound)

	// No assignment should have been created.
	_, ok := r.SourceMap("cam1")
	require.False(t, ok)

	proc := r.SourceProcessor("cam1")
	require.Nil(t, proc)
}

func TestRegistry_RemoveSource(t *testing.T) {
	r := NewRegistry()

	m := Identity(8, 8)
	m.Name = "barrel"
	require.NoError(t, r.Store(m))
	require.NoError(t, r.AssignSource("cam1", "barrel"))

	r.RemoveSource("cam1")

	_, ok := r.SourceMap("cam1")
	require.False(t, ok)

	proc := r.SourceProcessor("cam1")
	require.Nil(t, proc)
}

func TestRegistry_RemoveSource_Idempotent(t *testing.T) {
	r := NewRegistry()
	// Should not panic when removing a source that was never assigned.
	r.RemoveSource("nonexistent")
}

func TestRegistry_ProgramMap_Static(t *testing.T) {
	r := NewRegistry()

	m := Identity(8, 8)
	m.Name = "barrel"
	require.NoError(t, r.Store(m))

	require.NoError(t, r.AssignProgram("barrel"))
	require.True(t, r.HasProgramMap())

	proc := r.ProgramProcessor()
	require.NotNil(t, proc)
	require.True(t, proc.Active())

	anim := r.ProgramAnimatedFrame()
	require.Nil(t, anim)
}

func TestRegistry_ProgramMap_Animated(t *testing.T) {
	r := NewRegistry()

	f0 := Identity(8, 8)
	f1 := Identity(8, 8)
	anim := NewAnimatedSTMap("warp", []*STMap{f0, f1}, 30)
	require.NoError(t, r.StoreAnimated(anim))

	require.NoError(t, r.AssignProgram("warp"))
	require.True(t, r.HasProgramMap())

	// Animated program: ProgramProcessor is nil, ProgramAnimatedFrame is set.
	proc := r.ProgramProcessor()
	require.Nil(t, proc)

	got := r.ProgramAnimatedFrame()
	require.NotNil(t, got)
	require.Equal(t, anim, got)
}

func TestRegistry_AssignProgram_NotFound(t *testing.T) {
	r := NewRegistry()

	err := r.AssignProgram("nonexistent")
	require.ErrorIs(t, err, ErrNotFound)
	require.False(t, r.HasProgramMap())
}

func TestRegistry_RemoveProgram(t *testing.T) {
	r := NewRegistry()

	m := Identity(8, 8)
	m.Name = "barrel"
	require.NoError(t, r.Store(m))
	require.NoError(t, r.AssignProgram("barrel"))
	require.True(t, r.HasProgramMap())

	r.RemoveProgram()

	require.False(t, r.HasProgramMap())
	require.Nil(t, r.ProgramProcessor())
	require.Nil(t, r.ProgramAnimatedFrame())
}

func TestRegistry_Delete(t *testing.T) {
	r := NewRegistry()

	m := Identity(8, 8)
	m.Name = "barrel"
	require.NoError(t, r.Store(m))
	require.NoError(t, r.AssignSource("cam1", "barrel"))
	require.NoError(t, r.AssignProgram("barrel"))

	require.NoError(t, r.Delete("barrel"))

	// Map should be gone.
	_, ok := r.Get("barrel")
	require.False(t, ok)

	// Source assignment should be cleared.
	_, ok = r.SourceMap("cam1")
	require.False(t, ok)
	require.Nil(t, r.SourceProcessor("cam1"))

	// Program assignment should be cleared.
	require.False(t, r.HasProgramMap())
	require.Nil(t, r.ProgramProcessor())
}

func TestRegistry_Delete_Animated(t *testing.T) {
	r := NewRegistry()

	f0 := Identity(8, 8)
	anim := NewAnimatedSTMap("warp", []*STMap{f0}, 30)
	require.NoError(t, r.StoreAnimated(anim))
	require.NoError(t, r.AssignProgram("warp"))

	require.NoError(t, r.Delete("warp"))

	_, ok := r.GetAnimated("warp")
	require.False(t, ok)
	require.False(t, r.HasProgramMap())
}

func TestRegistry_Delete_NotFound(t *testing.T) {
	r := NewRegistry()

	err := r.Delete("nonexistent")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()

	// Empty registry.
	require.Empty(t, r.List())

	// Add some maps in non-alphabetical order.
	m1 := Identity(8, 8)
	m1.Name = "zoom"
	require.NoError(t, r.Store(m1))

	m2 := Identity(8, 8)
	m2.Name = "barrel"
	require.NoError(t, r.Store(m2))

	f0 := Identity(8, 8)
	anim := NewAnimatedSTMap("anim-warp", []*STMap{f0}, 30)
	require.NoError(t, r.StoreAnimated(anim))

	names := r.List()
	require.Equal(t, []string{"anim-warp", "barrel", "zoom"}, names)
	// Verify sorted.
	require.True(t, sort.StringsAreSorted(names))
}

func TestRegistry_State(t *testing.T) {
	r := NewRegistry()

	// Store maps.
	m := Identity(8, 8)
	m.Name = "barrel"
	require.NoError(t, r.Store(m))

	f0 := Identity(8, 8)
	anim := NewAnimatedSTMap("warp", []*STMap{f0}, 30)
	require.NoError(t, r.StoreAnimated(anim))

	// Assign source.
	require.NoError(t, r.AssignSource("cam1", "barrel"))

	// Assign static program map.
	require.NoError(t, r.AssignProgram("barrel"))

	st := r.State()
	require.Equal(t, map[string]string{"cam1": "barrel"}, st.Sources)
	require.NotNil(t, st.Program)
	require.Equal(t, "barrel", st.Program.Map)
	require.Equal(t, "static", st.Program.Type)
	require.Equal(t, 0, st.Program.Frame)
	require.Equal(t, []string{"barrel", "warp"}, st.Available)

	// Switch to animated program map.
	require.NoError(t, r.AssignProgram("warp"))

	st = r.State()
	require.NotNil(t, st.Program)
	require.Equal(t, "warp", st.Program.Map)
	require.Equal(t, "animated", st.Program.Type)

	// No program map.
	r.RemoveProgram()
	st = r.State()
	require.Nil(t, st.Program)
}

func TestRegistry_OnStateChange(t *testing.T) {
	r := NewRegistry()

	var mu sync.Mutex
	callCount := 0

	r.SetOnStateChange(func(state STMapState) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
	})

	m := Identity(8, 8)
	m.Name = "barrel"
	require.NoError(t, r.Store(m))

	mu.Lock()
	require.Equal(t, 1, callCount, "Store should trigger callback")
	mu.Unlock()

	// AssignSource triggers callback.
	require.NoError(t, r.AssignSource("cam1", "barrel"))
	mu.Lock()
	require.Equal(t, 2, callCount, "AssignSource should trigger callback")
	mu.Unlock()

	// RemoveSource triggers callback.
	r.RemoveSource("cam1")
	mu.Lock()
	require.Equal(t, 3, callCount, "RemoveSource should trigger callback")
	mu.Unlock()

	// AssignProgram triggers callback.
	require.NoError(t, r.AssignProgram("barrel"))
	mu.Lock()
	require.Equal(t, 4, callCount, "AssignProgram should trigger callback")
	mu.Unlock()

	// RemoveProgram triggers callback.
	r.RemoveProgram()
	mu.Lock()
	require.Equal(t, 5, callCount, "RemoveProgram should trigger callback")
	mu.Unlock()

	// Delete triggers callback.
	require.NoError(t, r.Delete("barrel"))
	mu.Lock()
	require.Equal(t, 6, callCount, "Delete should trigger callback")
	mu.Unlock()

	// StoreAnimated triggers callback.
	f0 := Identity(8, 8)
	anim := NewAnimatedSTMap("warp", []*STMap{f0}, 30)
	require.NoError(t, r.StoreAnimated(anim))
	mu.Lock()
	require.Equal(t, 7, callCount, "StoreAnimated should trigger callback")
	mu.Unlock()
}

func TestRegistry_OnStateChange_NotCalledOnError(t *testing.T) {
	r := NewRegistry()

	callCount := 0
	r.SetOnStateChange(func(state STMapState) {
		callCount++
	})

	// AssignSource with nonexistent map should not trigger callback.
	_ = r.AssignSource("cam1", "nonexistent")
	require.Equal(t, 0, callCount)

	// AssignProgram with nonexistent map should not trigger callback.
	_ = r.AssignProgram("nonexistent")
	require.Equal(t, 0, callCount)

	// Delete nonexistent should not trigger callback.
	_ = r.Delete("nonexistent")
	require.Equal(t, 0, callCount)

	// Store with invalid name should not trigger callback.
	m := Identity(8, 8)
	m.Name = ""
	_ = r.Store(m)
	require.Equal(t, 0, callCount)
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewRegistry()

	// Pre-store a map.
	m := Identity(8, 8)
	m.Name = "barrel"
	require.NoError(t, r.Store(m))

	var wg sync.WaitGroup
	const goroutines = 10

	// Concurrent reads and writes.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Get("barrel")
			r.List()
			r.State()
			r.HasProgramMap()
			r.SourceProcessor("cam1")
			r.ProgramProcessor()
			r.ProgramAnimatedFrame()
		}()
	}

	wg.Wait()
}
