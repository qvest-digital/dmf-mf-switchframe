package colorgrade

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourceCorrectorManager_GetDefault(t *testing.T) {
	m := NewSourceCorrectorManager()
	state := m.Get("cam1")
	require.True(t, state.IsDefault())
}

func TestSourceCorrectorManager_SetAndGet(t *testing.T) {
	m := NewSourceCorrectorManager()
	s := DefaultCorrectorState()
	s.Temperature = 50
	m.Set("cam1", s)
	got := m.Get("cam1")
	require.Equal(t, float32(50), got.Temperature)
}

func TestSourceCorrectorManager_Reset(t *testing.T) {
	m := NewSourceCorrectorManager()
	s := DefaultCorrectorState()
	s.Temperature = 50
	m.Set("cam1", s)
	m.Reset("cam1")
	require.True(t, m.Get("cam1").IsDefault())
	require.False(t, m.HasCorrection("cam1"))
}

func TestSourceCorrectorManager_HasCorrection(t *testing.T) {
	m := NewSourceCorrectorManager()
	require.False(t, m.HasCorrection("cam1"))
	s := DefaultCorrectorState()
	s.Temperature = 50
	m.Set("cam1", s)
	require.True(t, m.HasCorrection("cam1"))
}

func TestSourceCorrectorManager_SetDefault_Removes(t *testing.T) {
	m := NewSourceCorrectorManager()
	s := DefaultCorrectorState()
	s.Temperature = 50
	m.Set("cam1", s)
	require.True(t, m.HasCorrection("cam1"))
	m.Set("cam1", DefaultCorrectorState())
	require.False(t, m.HasCorrection("cam1"))
}

func TestSourceCorrectorManager_LUTData(t *testing.T) {
	m := NewSourceCorrectorManager()
	require.Nil(t, m.SourceLUTData("cam1")) // default = nil
	s := DefaultCorrectorState()
	s.Temperature = 50
	m.Set("cam1", s)
	data := m.SourceLUTData("cam1")
	require.NotNil(t, data)
	require.Equal(t, 33*33*33*3, len(data))
}

func TestSourceCorrectorManager_LUTGen(t *testing.T) {
	m := NewSourceCorrectorManager()
	require.Equal(t, uint64(0), m.SourceLUTGen("cam1"))
	s := DefaultCorrectorState()
	s.Temperature = 50
	m.Set("cam1", s)
	gen1 := m.SourceLUTGen("cam1")
	require.Greater(t, gen1, uint64(0))
	s.Temperature = 60
	m.Set("cam1", s)
	gen2 := m.SourceLUTGen("cam1")
	require.Greater(t, gen2, gen1)
}

func TestSourceCorrectorManager_LUTCaching(t *testing.T) {
	m := NewSourceCorrectorManager()
	s := DefaultCorrectorState()
	s.Temperature = 50
	m.Set("cam1", s)
	d1 := m.SourceLUTData("cam1")
	d2 := m.SourceLUTData("cam1")
	// Same slice header = cached
	require.Equal(t, &d1[0], &d2[0])
}

func TestSourceCorrectorManager_AllCorrected(t *testing.T) {
	m := NewSourceCorrectorManager()
	s := DefaultCorrectorState()
	s.Temperature = 50
	m.Set("cam1", s)
	m.Set("cam3", s)
	keys := m.AllCorrected()
	require.ElementsMatch(t, []string{"cam1", "cam3"}, keys)
}

func TestSourceCorrectorManager_Remove(t *testing.T) {
	m := NewSourceCorrectorManager()
	s := DefaultCorrectorState()
	s.Temperature = 50
	m.Set("cam1", s)
	m.Remove("cam1")
	require.False(t, m.HasCorrection("cam1"))
	require.Nil(t, m.SourceLUTData("cam1"))
}

func TestSourceCorrectorManager_ThreadSafe(t *testing.T) {
	m := NewSourceCorrectorManager()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("cam%d", i%4)
			s := DefaultCorrectorState()
			s.Temperature = float32(i * 10)
			m.Set(key, s)
			_ = m.Get(key)
			_ = m.SourceLUTData(key)
			_ = m.SourceLUTGen(key)
			_ = m.HasCorrection(key)
		}(i)
	}
	wg.Wait()
}

func TestSourceCorrectorManager_LUTSize(t *testing.T) {
	m := NewSourceCorrectorManager()
	// Default source returns 33.
	require.Equal(t, 33, m.SourceLUTSize("cam1"))
	// After setting and generating LUT, still 33.
	s := DefaultCorrectorState()
	s.Temperature = 50
	m.Set("cam1", s)
	_ = m.SourceLUTData("cam1") // force LUT generation
	require.Equal(t, 33, m.SourceLUTSize("cam1"))
}

func TestSourceCorrectorManager_LUTInvalidatedOnSet(t *testing.T) {
	m := NewSourceCorrectorManager()
	s := DefaultCorrectorState()
	s.Temperature = 50
	m.Set("cam1", s)
	d1 := m.SourceLUTData("cam1")
	require.NotNil(t, d1)

	// Change state — LUT should be regenerated (different pointer).
	s.Temperature = -30
	m.Set("cam1", s)
	d2 := m.SourceLUTData("cam1")
	require.NotNil(t, d2)
	require.NotEqual(t, &d1[0], &d2[0], "LUT should be regenerated after Set")
}

func TestSourceCorrectorManager_ResetNonexistent(t *testing.T) {
	m := NewSourceCorrectorManager()
	// Should not panic.
	m.Reset("nonexistent")
	m.Remove("nonexistent")
	require.False(t, m.HasCorrection("nonexistent"))
}
