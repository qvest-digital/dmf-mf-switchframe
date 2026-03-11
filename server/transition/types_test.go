package transition

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypeConstants(t *testing.T) {
	require.Equal(t, Type("mix"), Mix)
	require.Equal(t, Type("dip"), Dip)
	require.Equal(t, Type("ftb"), FTB)
}

func TestStateConstants(t *testing.T) {
	require.Equal(t, State(0), StateIdle)
	require.Equal(t, State(1), StateActive)
}
