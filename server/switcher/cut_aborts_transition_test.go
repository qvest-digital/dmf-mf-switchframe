package switcher

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/transition"
)

// A cut to the source on program while a transition takes it away keeps
// it on program: the two engines of a pair, taking the transition and the
// cut in either order, then end alike.
func TestCutToProgramAbortsTransition(t *testing.T) {
	s := NewTestSwitcher(t, distribution.NewRelay())
	for _, key := range []string{"cam1", "cam2"} {
		s.RegisterSource(key, distribution.NewRelay())
	}
	s.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(320, 240), nil },
	})
	ctx := context.Background()
	require.NoError(t, s.Cut(ctx, "cam1"))
	require.NoError(t, s.StartTransition(ctx, "cam2", "mix", 5000, ""))
	require.True(t, s.State().InTransition)

	require.NoError(t, s.Cut(ctx, "cam1"))
	st := s.State()
	require.False(t, st.InTransition, "the transition ran on")
	require.Equal(t, "cam1", st.ProgramSource)
}

// Outside a transition a cut to the source on program changes nothing.
func TestCutToProgramIdleIsNoOp(t *testing.T) {
	s := NewTestSwitcher(t, distribution.NewRelay())
	s.RegisterSource("cam1", distribution.NewRelay())
	ctx := context.Background()
	require.NoError(t, s.Cut(ctx, "cam1"))
	seq := s.State().Seq
	require.NoError(t, s.Cut(ctx, "cam1"))
	require.Equal(t, seq, s.State().Seq)
}
