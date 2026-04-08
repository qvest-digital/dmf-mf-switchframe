package output

import (
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
