package operator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func tempStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "operators.json")
}

func TestNewStore_EmptyFile(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)
	require.Empty(t, s.List())
}

func TestStore_Register(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))

	op, err := s.Register("Alice", RoleDirector)
	require.NoError(t, err)
	require.Equal(t, "Alice", op.Name)
	require.Equal(t, RoleDirector, op.Role)
	require.NotEmpty(t, op.ID)
	require.NotEmpty(t, op.Token)
	require.Len(t, op.Token, 64)
}

func TestStore_Register_EmptyName(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, err := s.Register("", RoleDirector)
	require.ErrorIs(t, err, ErrEmptyName)
}

func TestStore_Register_InvalidRole(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, err := s.Register("Alice", Role("admin"))
	require.ErrorIs(t, err, ErrInvalidRole)
}

func TestStore_Register_DuplicateName(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, _ = s.Register("Alice", RoleDirector)
	_, err := s.Register("Alice", RoleAudio)
	require.ErrorIs(t, err, ErrDuplicateName)
}

func TestStore_Get(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	op, _ := s.Register("Alice", RoleDirector)

	got, err := s.Get(op.ID)
	require.NoError(t, err)
	require.Equal(t, "Alice", got.Name)
}

func TestStore_Get_NotFound(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, err := s.Get("nonexistent")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_GetByToken(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	op, _ := s.Register("Alice", RoleDirector)

	got, err := s.GetByToken(op.Token)
	require.NoError(t, err)
	require.Equal(t, op.ID, got.ID)
}

func TestStore_GetByToken_NotFound(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, err := s.GetByToken("deadbeef")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_Delete(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	op, _ := s.Register("Alice", RoleDirector)

	err := s.Delete(op.ID)
	require.NoError(t, err)

	_, err = s.Get(op.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_Delete_NotFound(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	err := s.Delete("nonexistent")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_List(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, _ = s.Register("Alice", RoleDirector)
	_, _ = s.Register("Bob", RoleAudio)
	_, _ = s.Register("Carol", RoleGraphics)

	require.Len(t, s.List(), 3)
}

func TestStore_Persistence(t *testing.T) {
	path := tempStorePath(t)

	// Create store and register operators.
	s1, _ := NewStore(path)
	_, _ = s1.Register("Alice", RoleDirector)
	_, _ = s1.Register("Bob", RoleAudio)

	// Create new store from same file — should reload operators.
	s2, err := NewStore(path)
	require.NoError(t, err)
	require.Len(t, s2.List(), 2)
}

func TestStore_Persistence_DeleteAndReload(t *testing.T) {
	path := tempStorePath(t)

	s1, _ := NewStore(path)
	op, _ := s1.Register("Alice", RoleDirector)
	_ = s1.Delete(op.ID)

	s2, _ := NewStore(path)
	require.Empty(t, s2.List())
}

func TestStore_FileCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "operators.json")

	s, err := NewStore(path)
	require.NoError(t, err)

	_, _ = s.Register("Alice", RoleDirector)

	// Verify the file was created.
	_, err = os.Stat(path)
	require.False(t, os.IsNotExist(err), "expected operators.json to be created")
}

func TestStore_GetByToken_AfterDelete(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))

	// Register three operators.
	alice, _ := s.Register("Alice", RoleDirector)
	bob, _ := s.Register("Bob", RoleAudio)
	carol, _ := s.Register("Carol", RoleGraphics)

	// Delete the middle operator (Bob) to shift indices.
	require.NoError(t, s.Delete(bob.ID))

	// Bob's token should no longer resolve.
	_, err := s.GetByToken(bob.Token)
	require.ErrorIs(t, err, ErrNotFound)

	// Alice and Carol should still resolve correctly.
	gotAlice, err := s.GetByToken(alice.Token)
	require.NoError(t, err)
	require.Equal(t, alice.ID, gotAlice.ID)

	gotCarol, err := s.GetByToken(carol.Token)
	require.NoError(t, err)
	require.Equal(t, carol.ID, gotCarol.ID)
}
