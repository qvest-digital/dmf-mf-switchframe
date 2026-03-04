package transition

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTransitionTypeConstants(t *testing.T) {
	require.Equal(t, TransitionType("mix"), TransitionMix)
	require.Equal(t, TransitionType("dip"), TransitionDip)
	require.Equal(t, TransitionType("ftb"), TransitionFTB)
}

func TestTransitionStateConstants(t *testing.T) {
	require.Equal(t, TransitionState(0), StateIdle)
	require.Equal(t, TransitionState(1), StateActive)
}
