package preset

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The peer engine's presets of a name replace this engine's in place, IDs
// included, persist, and an empty set deletes them.
func TestReplaceByName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	s, err := NewStore(path)
	require.NoError(t, err)
	_, err = s.Create("First", ControlRoomSnapshot{})
	require.NoError(t, err)
	_, err = s.Create("Show", ControlRoomSnapshot{ProgramSource: "cam1"})
	require.NoError(t, err)
	_, err = s.Create("Last", ControlRoomSnapshot{})
	require.NoError(t, err)

	peers := []Preset{{ID: "peer-id", Name: "Show", ProgramSource: "cam2"}}
	require.NoError(t, s.ReplaceByName("Show", peers))
	names := func(st *Store) []string {
		var out []string
		for _, p := range st.List() {
			out = append(out, p.Name+"/"+p.ProgramSource)
		}
		return out
	}
	require.Equal(t, []string{"First/", "Show/cam2", "Last/"}, names(s))
	got, ok := s.Get("peer-id")
	require.True(t, ok)
	require.Equal(t, "cam2", got.ProgramSource)

	reopened, err := NewStore(path)
	require.NoError(t, err)
	require.Equal(t, names(s), names(reopened))

	require.NoError(t, s.ReplaceByName("Show", nil))
	require.Equal(t, []string{"First/", "Last/"}, names(s))
	require.NoError(t, s.ReplaceByName("New", []Preset{{ID: "n", Name: "New"}}))
	require.Equal(t, []string{"First/", "Last/", "New/"}, names(s))
	require.Error(t, s.ReplaceByName("Other", []Preset{{ID: "x", Name: "Mismatch"}}))
}
