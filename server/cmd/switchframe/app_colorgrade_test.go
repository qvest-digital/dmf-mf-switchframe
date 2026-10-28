package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/colorgrade"
)

func newTestColorGradeController(t *testing.T) (*colorGradeController, *int) {
	t.Helper()
	store, err := colorgrade.NewStore(t.TempDir())
	require.NoError(t, err)
	c := newColorGradeController(store)
	changes := 0
	c.onEnabledChange = func() { changes++ }
	return c, &changes
}

// The CPU pipeline reads the program grade through ProgramGrader; it has to
// follow every way the operator turns grading on, changes it and turns it off.
func TestColorGradeController_ProgramGrader(t *testing.T) {
	c, changes := newTestColorGradeController(t)

	g, _ := c.ProgramGrader()
	require.Nil(t, g, "grading starts off")

	require.NoError(t, c.SetColorGrade("noir", 0.5))
	g, intensity := c.ProgramGrader()
	require.NotNil(t, g)
	require.Equal(t, float32(0.5), intensity)
	require.Equal(t, 1, *changes, "turning grading on rebuilds the CPU pipeline")

	require.NoError(t, c.SetColorGrade("sepia", 1))
	g2, intensity := c.ProgramGrader()
	require.NotSame(t, g, g2, "a new look needs a new grader")
	require.Equal(t, float32(1), intensity)
	require.Equal(t, 1, *changes, "changing the look while on needs no rebuild")

	c.DisableColorGrade()
	g, _ = c.ProgramGrader()
	require.Nil(t, g)
	require.Equal(t, 2, *changes, "turning grading off rebuilds the CPU pipeline")
}

func TestColorGradeController_CorrectorAloneGrades(t *testing.T) {
	c, changes := newTestColorGradeController(t)
	cs := colorgrade.DefaultCorrectorState()
	cs.Saturation = 0
	c.SetCorrector(cs)
	g, intensity := c.ProgramGrader()
	require.NotNil(t, g, "a corrector change with no look still grades")
	require.Equal(t, float32(1), intensity)
	require.Equal(t, 1, *changes)

	// Monochrome corrector: a saturated colour comes out neutral.
	f := make([]byte, 6)
	f[0], f[1], f[2], f[3], f[4], f[5] = 81, 81, 81, 81, 90, 240 // 2x2 red
	require.NoError(t, g.ApplyYUV420(f, 2, 2, intensity))
	require.InDelta(t, 128, int(f[4]), 2)
	require.InDelta(t, 128, int(f[5]), 2)
}
