package output

import (
	"errors"
	"testing"
)

func TestEngineLabel_SetAndGet(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	if got := m.EngineLabel(); got != "" {
		t.Errorf("EngineLabel() on fresh manager = %q, want \"\"", got)
	}
	m.SetEngineLabel("a")
	if got := m.EngineLabel(); got != "a" {
		t.Errorf("EngineLabel() after SetEngineLabel(a) = %q, want \"a\"", got)
	}
	m.SetEngineLabel("")
	if got := m.EngineLabel(); got != "" {
		t.Errorf("EngineLabel() after SetEngineLabel(\"\") = %q, want \"\"", got)
	}
}

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

func TestStartDestination_StandbyOnOtherEngine(t *testing.T) {
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

	if err := m.StartDestination(id); err != nil {
		t.Fatalf("start on the engine that does not send: %v", err)
	}
	status, err := m.GetDestination(id)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "standby" || !status.Armed {
		t.Errorf("expected an armed standby, got state %q armed %v", status.State, status.Armed)
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

func TestStopDestination_DisarmsStandby(t *testing.T) {
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
	if err := m.StartDestination(id); err != nil {
		t.Fatal(err)
	}

	if err := m.StopDestination(id); err != nil {
		t.Fatalf("stop of an armed standby: %v", err)
	}
	if err := m.StopDestination(id); !errors.Is(err, ErrDestinationStopped) {
		t.Errorf("second stop: expected ErrDestinationStopped, got %v", err)
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
