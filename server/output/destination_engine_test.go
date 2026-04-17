package output

import (
	"errors"
	"strings"
	"testing"
)

func TestAddDestination_DefaultsEngine(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	m.SetEngineLabel("a")

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
	})
	if err != nil {
		t.Fatal(err)
	}

	status, err := m.GetDestination(id)
	if err != nil {
		t.Fatal(err)
	}
	if status.Config.Engine != "a" {
		t.Errorf("expected engine 'a', got %q", status.Config.Engine)
	}
}

func TestAddDestination_ExplicitEnginePreserved(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	m.SetEngineLabel("a")

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
		Engine:  "b",
	})
	if err != nil {
		t.Fatal(err)
	}

	status, err := m.GetDestination(id)
	if err != nil {
		t.Fatal(err)
	}
	if status.Config.Engine != "b" {
		t.Errorf("expected engine 'b', got %q", status.Config.Engine)
	}
}

func TestReassignDestinationEngine_Success(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
		Engine:  "a",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := m.ReassignDestinationEngine(id, "b"); err != nil {
		t.Fatal(err)
	}

	status, err := m.GetDestination(id)
	if err != nil {
		t.Fatal(err)
	}
	if status.Config.Engine != "b" {
		t.Errorf("expected engine 'b', got %q", status.Config.Engine)
	}
}

func TestReassignDestinationEngine_ClearEngine(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
		Engine:  "a",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := m.ReassignDestinationEngine(id, ""); err != nil {
		t.Fatal(err)
	}

	status, err := m.GetDestination(id)
	if err != nil {
		t.Fatal(err)
	}
	if status.Config.Engine != "" {
		t.Errorf("expected empty engine, got %q", status.Config.Engine)
	}
}

func TestReassignDestinationEngine_NotFound(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	err := m.ReassignDestinationEngine("nonexistent", "a")
	if err != ErrDestinationNotFound {
		t.Errorf("expected ErrDestinationNotFound, got %v", err)
	}
}

func TestReassignDestinationEngine_InvalidEngine(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = m.ReassignDestinationEngine(id, "c")
	if err == nil {
		t.Error("expected error for invalid engine, got nil")
	}
}

func TestReassignDestinationEngine_TriggersStateCallback(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	called := false
	m.OnStateChange(func() { called = true })

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
		Engine:  "a",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := m.ReassignDestinationEngine(id, "b"); err != nil {
		t.Fatal(err)
	}

	if !called {
		t.Error("expected onState callback to be triggered")
	}
}

func TestStartDestination_RejectsWrongEngine(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	m.SetEngineLabel("a")

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
		Engine:  "b",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = m.StartDestination(id)
	if err == nil {
		t.Fatal("expected error starting destination for engine 'b' on engine 'a', got nil")
	}
	if !errors.Is(err, ErrDestinationWrongEngine) {
		t.Errorf("expected ErrDestinationWrongEngine, got: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, id) {
		t.Errorf("error should include destination ID %q; got: %v", id, err)
	}
	if !strings.Contains(msg, `"b"`) {
		t.Errorf("error should reference configured engine \"b\"; got: %v", err)
	}
	if !strings.Contains(msg, `"a"`) {
		t.Errorf("error should reference local engine \"a\"; got: %v", err)
	}

	status, err := m.GetDestination(id)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "stopped" {
		t.Errorf("expected state 'stopped' after rejected start, got %q", status.State)
	}
}

func TestStartDestination_AcceptsMatchingEngine(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	m.SetEngineLabel("a")

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
		Engine:  "a",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := m.StartDestination(id); err != nil {
		t.Fatalf("expected StartDestination to succeed when engines match, got: %v", err)
	}
}

func TestStartDestination_AcceptsEmptyEngine(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	m.SetEngineLabel("a")

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
		Engine:  "a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ReassignDestinationEngine(id, ""); err != nil {
		t.Fatal(err)
	}

	if err := m.StartDestination(id); err != nil {
		t.Fatalf("expected StartDestination to succeed for unscoped destination, got: %v", err)
	}
}

func TestStartDestination_AcceptsWildcardManager(t *testing.T) {
	// Single-engine manager (no engine label) must accept a tagged
	// destination — e.g. a demotion from multi-region where config still
	// carries engine tags. Otherwise the manager can't start any destination
	// inherited from the old multi-engine config.
	m := NewManager()
	defer func() { _ = m.Close() }()

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
		Engine:  "a",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := m.StartDestination(id); err != nil {
		t.Fatalf("expected StartDestination to succeed on unlabeled manager, got: %v", err)
	}
}

func TestStopDestination_RejectsWrongEngineWhenInactive(t *testing.T) {
	// If an operator mis-routes a stop call to the wrong engine, the old
	// behavior returned ErrDestinationStopped (misleading — the destination
	// is active elsewhere). The guard surfaces the routing error instead.
	m := NewManager()
	defer func() { _ = m.Close() }()

	m.SetEngineLabel("a")

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
		Engine:  "b",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = m.StopDestination(id)
	if err == nil {
		t.Fatal("expected error stopping destination for engine 'b' on engine 'a', got nil")
	}
	if !errors.Is(err, ErrDestinationWrongEngine) {
		t.Errorf("expected ErrDestinationWrongEngine, got: %v", err)
	}
	if errors.Is(err, ErrDestinationStopped) {
		t.Errorf("wrong-engine stop must not masquerade as ErrDestinationStopped; got: %v", err)
	}
}

func TestStopDestination_AllowsStopOfActiveMismatchForRecovery(t *testing.T) {
	// Recovery case: a destination got activated on the wrong engine (legacy
	// state from before the Start guard landed). We must still be able to
	// stop it here to clean up. The guard only fires on the inactive path.
	m := NewManager()
	defer func() { _ = m.Close() }()

	m.SetEngineLabel("a")

	id, err := m.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "10.0.0.1",
		Port:    5000,
		Engine:  "a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartDestination(id); err != nil {
		t.Fatal(err)
	}
	// After it's active, simulate a reassignment to the other engine —
	// the destination is now active here but labeled for the other engine.
	if err := m.ReassignDestinationEngine(id, "b"); err != nil {
		t.Fatal(err)
	}

	if err := m.StopDestination(id); err != nil {
		t.Fatalf("expected StopDestination to proceed for active mismatched destination (recovery), got: %v", err)
	}
}
