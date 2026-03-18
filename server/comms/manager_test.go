package comms

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerJoinLeave(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	m := NewManager(func() {})
	defer m.Close()

	// Join two participants.
	if err := m.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join op1: %v", err)
	}
	if err := m.Join("op2", "Bob"); err != nil {
		t.Fatalf("Join op2: %v", err)
	}

	// Verify state shows 2 participants.
	st := m.State()
	if st == nil {
		t.Fatal("State() should not be nil with participants")
	}
	if !st.Active {
		t.Error("State should be active")
	}
	if len(st.Participants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(st.Participants))
	}

	// Leave one.
	m.Leave("op1")
	st = m.State()
	if st == nil {
		t.Fatal("State() should not be nil with 1 participant")
	}
	if len(st.Participants) != 1 {
		t.Fatalf("expected 1 participant after leave, got %d", len(st.Participants))
	}
	if st.Participants[0].OperatorID != "op2" {
		t.Errorf("remaining participant should be op2, got %s", st.Participants[0].OperatorID)
	}

	// Leave last.
	m.Leave("op2")
	st = m.State()
	if st != nil {
		t.Error("State() should be nil when no participants")
	}
}

func TestManagerMaxParticipants(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	m := NewManager(func() {})
	defer m.Close()

	// Fill to capacity.
	for i := 0; i < MaxParticipants; i++ {
		id := string(rune('A' + i))
		if err := m.Join(id, "Name"+id); err != nil {
			t.Fatalf("Join %s: %v", id, err)
		}
	}

	// Next join should fail.
	err := m.Join("overflow", "Overflow")
	if err != ErrCommsFull {
		t.Fatalf("expected ErrCommsFull, got %v", err)
	}
}

func TestManagerDuplicateJoin(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	m := NewManager(func() {})
	defer m.Close()

	if err := m.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join: %v", err)
	}

	// Duplicate join should be idempotent.
	if err := m.Join("op1", "Alice Again"); err != nil {
		t.Fatalf("duplicate Join: %v", err)
	}

	st := m.State()
	if st == nil {
		t.Fatal("State() should not be nil")
	}
	if len(st.Participants) != 1 {
		t.Fatalf("expected 1 participant after duplicate join, got %d", len(st.Participants))
	}
}

func TestManagerSetMuted(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	m := NewManager(func() {})
	defer m.Close()

	if err := m.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join: %v", err)
	}

	if err := m.SetMuted("op1", true); err != nil {
		t.Fatalf("SetMuted: %v", err)
	}

	st := m.State()
	if st == nil {
		t.Fatal("State() should not be nil")
	}
	if !st.Participants[0].Muted {
		t.Error("participant should be muted")
	}

	// SetMuted on non-existent should return error.
	if err := m.SetMuted("ghost", true); err != ErrNotInComms {
		t.Fatalf("expected ErrNotInComms, got %v", err)
	}
}

func TestManagerBroadcastOnJoin(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	var called atomic.Int32
	m := NewManager(func() {
		called.Add(1)
	})
	defer m.Close()

	if err := m.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join: %v", err)
	}

	// Give a moment for the callback to fire (it's synchronous in Join, but be safe).
	time.Sleep(10 * time.Millisecond)

	if called.Load() < 1 {
		t.Error("onBroadcast should have been called on join")
	}
}

func TestManagerStateNilWhenEmpty(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	m := NewManager(func() {})
	defer m.Close()

	st := m.State()
	if st != nil {
		t.Error("State() should be nil when no participants")
	}
}
