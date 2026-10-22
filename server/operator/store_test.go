package operator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func tempStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "operators.json")
}

func TestNewStore_EmptyFile(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)
	require.Empty(t, s.List())
}

func TestStore_Register(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))
	_, err := s.Register("", RoleDirector)
	require.ErrorIs(t, err, ErrEmptyName)
}

func TestStore_Register_InvalidRole(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))
	_, err := s.Register("Alice", Role("admin"))
	require.ErrorIs(t, err, ErrInvalidRole)
}

func TestStore_Register_DuplicateName(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))
	_, _ = s.Register("Alice", RoleDirector)
	_, err := s.Register("Alice", RoleAudio)
	require.ErrorIs(t, err, ErrDuplicateName)
}

func TestStore_Get(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))
	op, _ := s.Register("Alice", RoleDirector)

	got, err := s.Get(op.ID)
	require.NoError(t, err)
	require.Equal(t, "Alice", got.Name)
}

func TestStore_Get_NotFound(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))
	_, err := s.Get("nonexistent")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_GetByToken(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))
	op, _ := s.Register("Alice", RoleDirector)

	got, err := s.GetByToken(op.Token)
	require.NoError(t, err)
	require.Equal(t, op.ID, got.ID)
}

func TestStore_GetByToken_NotFound(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))
	_, err := s.GetByToken("deadbeef")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_Delete(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))
	op, _ := s.Register("Alice", RoleDirector)

	err := s.Delete(op.ID)
	require.NoError(t, err)

	_, err = s.Get(op.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_Delete_NotFound(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))
	err := s.Delete("nonexistent")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_List(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))
	_, _ = s.Register("Alice", RoleDirector)
	_, _ = s.Register("Bob", RoleAudio)
	_, _ = s.Register("Carol", RoleGraphics)

	require.Len(t, s.List(), 3)
}

func TestStore_Persistence(t *testing.T) {
	t.Parallel()
	path := tempStorePath(t)

	// Create store and register operators.
	s1, _ := NewStore(path)
	_, _ = s1.Register("Alice", RoleDirector)
	_, _ = s1.Register("Bob", RoleAudio)

	// Create new store from same file -- should reload operators.
	s2, err := NewStore(path)
	require.NoError(t, err)
	require.Len(t, s2.List(), 2)
}

func TestStore_Persistence_DeleteAndReload(t *testing.T) {
	t.Parallel()
	path := tempStorePath(t)

	s1, _ := NewStore(path)
	op, _ := s1.Register("Alice", RoleDirector)
	_ = s1.Delete(op.ID)

	s2, _ := NewStore(path)
	require.Empty(t, s2.List())
}

func TestStore_FileCreatesDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "operators.json")

	s, err := NewStore(path)
	require.NoError(t, err)

	_, _ = s.Register("Alice", RoleDirector)

	// Verify the file was created.
	_, err = os.Stat(path)
	require.False(t, os.IsNotExist(err), "expected operators.json to be created")
}

func TestStore_ListNeverObservesTransientState(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))

	// Pre-populate with one operator so the store is non-empty.
	seed, err := s.Register("Seed", RoleDirector)
	require.NoError(t, err)

	// Hammer Register and List concurrently. List() returns a consistent
	// snapshot — it never observes an in-flight mutation.
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			name := fmt.Sprintf("Op-%d", n)
			_, _ = s.Register(name, RoleViewer)
		}(i)
	}

	// Continuously list while registrations are in flight.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-done:
				return
			default:
			}
			list := s.List()
			// The seed operator must always be visible.
			found := false
			for _, op := range list {
				if op.ID == seed.ID {
					found = true
					break
				}
			}
			if !found && len(list) > 0 {
				t.Errorf("List() returned %d operators but seed operator is missing", len(list))
				return
			}
		}
	}()

	wg.Wait()
	done <- struct{}{}

	// After all goroutines finish, we should have seed + all concurrent operators.
	finalList := s.List()
	require.Equal(t, goroutines+1, len(finalList))
}

func TestStore_Register_RollbackOnSaveFailure(t *testing.T) {
	t.Parallel()
	path := tempStorePath(t)
	s, err := NewStore(path)
	require.NoError(t, err)

	// Register one operator successfully first.
	alice, err := s.Register("Alice", RoleDirector)
	require.NoError(t, err)

	// Make the directory read-only so save() will fail on CreateTemp.
	dir := filepath.Dir(path)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	// Remove the existing file so the next save cannot even use it.
	_ = os.Remove(path)

	_, err = s.Register("Bob", RoleAudio)
	require.Error(t, err, "expected save failure")

	// In-memory state should be rolled back: only Alice should remain.
	require.Len(t, s.List(), 1)
	require.Equal(t, "Alice", s.List()[0].Name)

	// Token index should still work for Alice.
	got, err := s.GetByToken(alice.Token)
	require.NoError(t, err)
	require.Equal(t, alice.ID, got.ID)

	// Bob's registration should have been fully undone —
	// a subsequent Register("Bob") should succeed after restoring permissions.
	require.NoError(t, os.Chmod(dir, 0o755))
	bob, err := s.Register("Bob", RoleAudio)
	require.NoError(t, err)
	require.Equal(t, "Bob", bob.Name)
	require.Len(t, s.List(), 2)
}

func TestStore_Delete_RollbackOnSaveFailure(t *testing.T) {
	t.Parallel()
	path := tempStorePath(t)
	s, err := NewStore(path)
	require.NoError(t, err)

	// Register three operators.
	alice, err := s.Register("Alice", RoleDirector)
	require.NoError(t, err)
	bob, err := s.Register("Bob", RoleAudio)
	require.NoError(t, err)
	carol, err := s.Register("Carol", RoleGraphics)
	require.NoError(t, err)

	// Make the directory read-only so save() will fail.
	dir := filepath.Dir(path)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_ = os.Remove(path)

	// Try to delete Bob — should fail.
	err = s.Delete(bob.ID)
	require.Error(t, err, "expected save failure")

	// In-memory state should be rolled back: all three operators remain.
	require.Len(t, s.List(), 3)

	// Token lookups should all still work.
	gotAlice, err := s.GetByToken(alice.Token)
	require.NoError(t, err)
	require.Equal(t, alice.ID, gotAlice.ID)

	gotBob, err := s.GetByToken(bob.Token)
	require.NoError(t, err)
	require.Equal(t, bob.ID, gotBob.ID)

	gotCarol, err := s.GetByToken(carol.Token)
	require.NoError(t, err)
	require.Equal(t, carol.ID, gotCarol.ID)

	// Order should be preserved: Alice, Bob, Carol.
	list := s.List()
	require.Equal(t, "Alice", list[0].Name)
	require.Equal(t, "Bob", list[1].Name)
	require.Equal(t, "Carol", list[2].Name)
}

func TestStore_GetByToken_AfterDelete(t *testing.T) {
	t.Parallel()
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

// --- Client-generated ID tests ---

func TestStore_Register_WithClientID(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))

	clientID := "abcdef1234567890abcdef1234567890" // 32 chars
	op, err := s.Register("Alice", RoleDirector, clientID)
	require.NoError(t, err)
	require.Equal(t, clientID, op.ID)
	require.Equal(t, "Alice", op.Name)
	require.Equal(t, RoleDirector, op.Role)
	require.NotEmpty(t, op.Token)
}

func TestStore_Register_WithoutClientID_GeneratesID(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))

	// No client ID — should generate a random one (backward compat).
	op, err := s.Register("Alice", RoleDirector)
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)
	require.Len(t, op.ID, 32) // generateID returns 16 bytes = 32 hex chars
}

func TestStore_Register_EmptyClientID_GeneratesID(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))

	// Explicit empty string — should generate a random one.
	op, err := s.Register("Alice", RoleDirector, "")
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)
	require.Len(t, op.ID, 32)
}

func TestStore_Register_ClientID_TooShort(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))

	_, err := s.Register("Alice", RoleDirector, "short")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid client ID length")
}

func TestStore_Register_ClientID_InvalidChars(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))

	cases := []struct {
		name string
		id   string
	}{
		{"spaces", "abcdefgh 1234567"},
		{"underscores", "abcdefgh_1234567"},
		{"dots", "abcdefgh.1234567"},
		{"slashes", "abcdefgh/1234567"},
		{"special", "abcdefgh!@#$%^&*"},
		{"unicode", "abcdefgh\u00e9\u00e9\u00e9\u00e9\u00e9\u00e9\u00e9\u00e9"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Register("Alice-"+tc.name, RoleDirector, tc.id)
			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid client ID")
		})
	}
}

func TestStore_Register_ClientID_ValidChars(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))

	// UUID format (lowercase hex + hyphens) — what crypto.randomUUID() produces.
	uuid := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	op, err := s.Register("UUID-User", RoleDirector, uuid)
	require.NoError(t, err)
	require.Equal(t, uuid, op.ID)

	// All uppercase alphanumeric.
	upper := "ABCDEFGH-12345678"
	op2, err := s.Register("Upper-User", RoleAudio, upper)
	require.NoError(t, err)
	require.Equal(t, upper, op2.ID)
}

func TestStore_Register_ClientID_TooLong(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))

	longID := string(make([]byte, 65))
	for i := range longID {
		longID = longID[:i] + "a" + longID[i+1:]
	}
	_, err := s.Register("Alice", RoleDirector, longID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid client ID length")
}

func TestStore_Register_DuplicateClientID_ReturnsExisting(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))

	clientID := "abcdef1234567890abcdef1234567890"
	op1, err := s.Register("Alice", RoleDirector, clientID)
	require.NoError(t, err)

	// Second registration with the same client ID — should return the existing
	// operator (idempotent, for dual-engine fan-out).
	op2, err := s.Register("Alice", RoleDirector, clientID)
	require.NoError(t, err)
	require.Equal(t, op1.ID, op2.ID)
	require.Equal(t, op1.Token, op2.Token)
	require.Equal(t, op1.Name, op2.Name)

	// Only one operator should exist in the store.
	require.Len(t, s.List(), 1)
}

func TestStore_Register_DuplicateClientID_DifferentName(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))

	clientID := "abcdef1234567890abcdef1234567890"
	_, err := s.Register("Alice", RoleDirector, clientID)
	require.NoError(t, err)

	// Same ID, different name/role — must fail with a consistency error.
	_, err = s.Register("Bob", RoleAudio, clientID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "client ID already registered")

	// Same ID, same name but different role — must also fail.
	_, err = s.Register("Alice", RoleAudio, clientID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "client ID already registered")

	// Only the original operator should exist.
	require.Len(t, s.List(), 1)
}

func TestStore_Register_ClientID_GetByID(t *testing.T) {
	t.Parallel()
	s, _ := NewStore(tempStorePath(t))

	clientID := "abcdef1234567890abcdef1234567890"
	op, err := s.Register("Alice", RoleDirector, clientID)
	require.NoError(t, err)

	got, err := s.Get(clientID)
	require.NoError(t, err)
	require.Equal(t, op.Token, got.Token)
}

// Both engines of a dual-engine pair receive the same registration. With a
// client-provided token they must accept the same bearer token; with tokens
// minted per engine, the browser holds one and the other engine rejects it.
func TestRegisterWithToken_BothEnginesAcceptTheClientToken(t *testing.T) {
	a, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("ab", 32)
	for _, s := range []*Store{a, b} {
		op, err := s.RegisterWithToken("Alice", RoleDirector, "client-id-0001", token)
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if op.Token != token {
			t.Fatalf("token = %q, want the client token", op.Token)
		}
	}
	for name, s := range map[string]*Store{"A": a, "B": b} {
		if _, err := s.GetByToken(token); err != nil {
			t.Fatalf("engine %s does not accept the token: %v", name, err)
		}
	}
}

func TestRegisterWithToken_RejectsMalformedOrTakenTokens(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{"short", strings.Repeat("AB", 32), strings.Repeat("zz", 32)} {
		if _, err := s.RegisterWithToken("Bob", RoleAudio, "", tok); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("token %q: err = %v, want ErrInvalidToken", tok, err)
		}
	}
	tok := strings.Repeat("cd", 32)
	if _, err := s.RegisterWithToken("Carol", RoleAudio, "", tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterWithToken("Dave", RoleAudio, "", tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("reused token: err = %v, want ErrInvalidToken", err)
	}
}

func TestRegisterWithToken_ReRegistrationNeedsTheSameToken(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.Repeat("ef", 32)
	if _, err := s.RegisterWithToken("Erin", RoleViewer, "client-id-0002", tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterWithToken("Erin", RoleViewer, "client-id-0002", tok); err != nil {
		t.Fatalf("idempotent re-registration: %v", err)
	}
	if _, err := s.RegisterWithToken("Erin", RoleViewer, "client-id-0002", strings.Repeat("01", 32)); err == nil {
		t.Fatal("re-registration with another token succeeded")
	}
}
