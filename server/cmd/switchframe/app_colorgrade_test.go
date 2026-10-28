package main

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/colorgrade"
)

func newTestColorGradeController(t *testing.T) *colorGradeController {
	t.Helper()
	store, err := colorgrade.NewStore(t.TempDir())
	require.NoError(t, err)
	return newColorGradeController(store)
}

// The CPU pipeline reads the program grade through ProgramGrader; it has to
// follow every way the operator turns grading on, changes it and turns it off.
func TestColorGradeController_ProgramGrader(t *testing.T) {
	c := newTestColorGradeController(t)

	g, _ := c.ProgramGrader()
	require.Nil(t, g, "grading starts off")

	require.NoError(t, c.SetColorGrade("noir", 0.5))
	g, intensity := c.ProgramGrader()
	require.NotNil(t, g)
	require.Equal(t, float32(0.5), intensity)

	require.NoError(t, c.SetColorGrade("sepia", 1))
	g2, intensity := c.ProgramGrader()
	require.NotSame(t, g, g2, "a new look needs a new grader")
	require.Equal(t, float32(1), intensity)

	c.DisableColorGrade()
	g, _ = c.ProgramGrader()
	require.Nil(t, g)
}

// TestColorGradeController_ConcurrentToggles: however enable and disable
// interleave, the CPU pipeline grades exactly when the API reports grading
// on.
func TestColorGradeController_ConcurrentToggles(t *testing.T) {
	c := newTestColorGradeController(t)
	cs := colorgrade.DefaultCorrectorState()
	cs.Saturation = 0.5
	for round := 0; round < 50; round++ {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				switch (round + i) % 4 {
				case 0:
					_ = c.SetColorGrade("noir", 1)
				case 1:
					c.DisableColorGrade()
				case 2:
					c.SetCorrector(cs)
				default:
					c.ResetCorrector()
				}
			}()
		}
		wg.Wait()
		g, _ := c.ProgramGrader()
		require.Equal(t, c.IsEnabled(), g != nil, "round %d", round)
	}
}

func TestColorGradeController_CorrectorAloneGrades(t *testing.T) {
	c := newTestColorGradeController(t)
	cs := colorgrade.DefaultCorrectorState()
	cs.Saturation = 0
	c.SetCorrector(cs)
	g, intensity := c.ProgramGrader()
	require.NotNil(t, g, "a corrector change with no look still grades")
	require.Equal(t, float32(1), intensity)

	// Monochrome corrector: a saturated colour comes out neutral.
	f := []byte{81, 81, 81, 81, 90, 240} // 2x2 red
	require.NoError(t, g.ApplyYUV420(f, 2, 2, intensity))
	require.InDelta(t, 128, int(f[4]), 2)
	require.InDelta(t, 128, int(f[5]), 2)
}
