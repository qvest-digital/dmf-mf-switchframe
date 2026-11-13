package operator

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The peer engine's record of an operator is stored as it is, token
// included, so the operator's token authenticates here too, and a changed
// token replaces the old one.
func TestPut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operators.json")
	s, err := NewStore(path)
	require.NoError(t, err)

	op := Operator{ID: "op-from-peer", Name: "Ada", Role: RoleDirector, Token: strings.Repeat("a1", 32)}
	require.NoError(t, s.Put(op))
	got, err := s.GetByToken(op.Token)
	require.NoError(t, err)
	require.Equal(t, op, got)

	op.Token = strings.Repeat("b2", 32)
	op.Role = RoleViewer
	require.NoError(t, s.Put(op))
	_, err = s.GetByToken(strings.Repeat("a1", 32))
	require.Error(t, err, "the replaced token still authenticates")
	require.Equal(t, 1, s.Count())

	reopened, err := NewStore(path)
	require.NoError(t, err)
	got, err = reopened.GetByToken(op.Token)
	require.NoError(t, err)
	require.Equal(t, RoleViewer, got.Role)

	require.Error(t, s.Put(Operator{ID: "x", Name: "Bad", Role: RoleViewer, Token: "short"}))
	require.Error(t, s.Put(Operator{ID: "y", Name: "Bad", Role: "nobody"}))
}
