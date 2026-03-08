package output

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutputManager_AddDestination(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
		Name:    "YouTube",
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.Len(t, id, 8, "ID should be 8 hex chars")

	// Verify destination exists.
	status, err := mgr.GetDestination(id)
	require.NoError(t, err)
	require.Equal(t, id, status.ID)
	require.Equal(t, "srt-caller", status.Config.Type)
	require.Equal(t, "192.168.1.100", status.Config.Address)
	require.Equal(t, 9000, status.Config.Port)
	require.Equal(t, "YouTube", status.Config.Name)
	require.Equal(t, "stopped", status.State)
}

func TestOutputManager_AddDestination_InvalidType(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type: "rtmp",
		Port: 9000,
	}

	_, err := mgr.AddDestination(config)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported destination type")
}

func TestOutputManager_AddDestination_MissingPort(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
	}

	_, err := mgr.AddDestination(config)
	require.Error(t, err)
	require.Contains(t, err.Error(), "port is required")
}

func TestOutputManager_AddDestination_CallerMissingAddress(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type: "srt-caller",
		Port: 9000,
	}

	_, err := mgr.AddDestination(config)
	require.Error(t, err)
	require.Contains(t, err.Error(), "address is required")
}

func TestOutputManager_RemoveDestination(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)

	err = mgr.RemoveDestination(id)
	require.NoError(t, err)

	// Should no longer exist.
	_, err = mgr.GetDestination(id)
	require.ErrorIs(t, err, ErrDestinationNotFound)
}

func TestOutputManager_RemoveDestination_NotFound(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	err := mgr.RemoveDestination("nonexistent")
	require.ErrorIs(t, err, ErrDestinationNotFound)
}

func TestOutputManager_RemoveActiveDestination(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)

	// Start and then remove — should stop first.
	require.NoError(t, mgr.StartDestination(id))

	err = mgr.RemoveDestination(id)
	require.NoError(t, err)

	// Should no longer exist.
	_, err = mgr.GetDestination(id)
	require.ErrorIs(t, err, ErrDestinationNotFound)
}

func TestOutputManager_MultipleDestinations(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	id1, err := mgr.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
		Name:    "YouTube",
	})
	require.NoError(t, err)

	id2, err := mgr.AddDestination(DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.200",
		Port:    9001,
		Name:    "Twitch",
	})
	require.NoError(t, err)

	require.NotEqual(t, id1, id2, "IDs should be unique")

	dests := mgr.ListDestinations()
	require.Len(t, dests, 2)

	// Both should be accessible by ID.
	s1, err := mgr.GetDestination(id1)
	require.NoError(t, err)
	require.Equal(t, "YouTube", s1.Config.Name)

	s2, err := mgr.GetDestination(id2)
	require.NoError(t, err)
	require.Equal(t, "Twitch", s2.Config.Name)
}

func TestOutputManager_GetDestination_NotFound(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	_, err := mgr.GetDestination("does-not-exist")
	require.ErrorIs(t, err, ErrDestinationNotFound)
}

func TestOutputManager_StartDestination(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)

	err = mgr.StartDestination(id)
	require.NoError(t, err)

	// Verify the destination is active.
	status, err := mgr.GetDestination(id)
	require.NoError(t, err)
	require.NotEqual(t, "stopped", status.State)

	// Verify the muxer started.
	require.NotNil(t, mgr.viewer)
}

func TestOutputManager_StartDestination_NotFound(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	err := mgr.StartDestination("nonexistent")
	require.ErrorIs(t, err, ErrDestinationNotFound)
}

func TestOutputManager_StartDestination_AlreadyActive(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)

	require.NoError(t, mgr.StartDestination(id))
	err = mgr.StartDestination(id)
	require.ErrorIs(t, err, ErrDestinationActive)
}

func TestOutputManager_StopDestination(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)

	require.NoError(t, mgr.StartDestination(id))
	err = mgr.StopDestination(id)
	require.NoError(t, err)

	status, err := mgr.GetDestination(id)
	require.NoError(t, err)
	require.Equal(t, "stopped", status.State)
}

func TestOutputManager_StopDestination_NotFound(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	err := mgr.StopDestination("nonexistent")
	require.ErrorIs(t, err, ErrDestinationNotFound)
}

func TestOutputManager_StopDestination_NotActive(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)

	err = mgr.StopDestination(id)
	require.ErrorIs(t, err, ErrDestinationStopped)
}

func TestOutputManager_ListDestinations_Empty(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	dests := mgr.ListDestinations()
	require.Empty(t, dests)
}

func TestOutputManager_ListenerDestination(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type:     "srt-listener",
		Port:     9000,
		MaxConns: 4,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)

	status, err := mgr.GetDestination(id)
	require.NoError(t, err)
	require.Equal(t, "srt-listener", status.Config.Type)
	require.Equal(t, 4, status.Config.MaxConns)
}

func TestOutputManager_DestinationsInRebuild(t *testing.T) {
	// Verify that active destinations are included in the adapter fan-out.
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)

	require.NoError(t, mgr.StartDestination(id))

	// The adapters list should include the destination's async wrapper.
	adapterCount := len(*mgr.adapters.Load())
	require.Equal(t, 1, adapterCount)
}

func TestOutputManager_CloseWithDestinations(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)
	require.NoError(t, mgr.StartDestination(id))

	err = mgr.Close()
	require.NoError(t, err)
}

func TestOutputManager_StateCallbackOnDestination(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	callCount := 0
	mgr.OnStateChange(func() {
		callCount++
	})

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)
	require.Equal(t, 0, callCount, "add should not trigger state change")

	require.NoError(t, mgr.StartDestination(id))
	require.Greater(t, callCount, 0, "start should trigger state change")

	prevCount := callCount
	require.NoError(t, mgr.StopDestination(id))
	require.Greater(t, callCount, prevCount, "stop should trigger state change")
}

func TestOutputManager_RemoveDestination_FiresStateCallback(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	callCount := 0
	mgr.OnStateChange(func() {
		callCount++
	})

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)
	require.Equal(t, 0, callCount, "add should not trigger state change")

	err = mgr.RemoveDestination(id)
	require.NoError(t, err)
	require.Equal(t, 1, callCount, "remove should trigger state change")
}

func TestOutputManager_RemoveActiveDestination_FiresStateCallback(t *testing.T) {
	relay := newTestRelay()
	mgr := NewOutputManager(relay)
	defer func() { _ = mgr.Close() }()

	callCount := 0
	mgr.OnStateChange(func() {
		callCount++
	})

	config := DestinationConfig{
		Type:    "srt-caller",
		Address: "192.168.1.100",
		Port:    9000,
	}

	id, err := mgr.AddDestination(config)
	require.NoError(t, err)

	require.NoError(t, mgr.StartDestination(id))
	startCount := callCount
	require.Greater(t, startCount, 0, "start should trigger state change")

	err = mgr.RemoveDestination(id)
	require.NoError(t, err)
	require.Greater(t, callCount, startCount, "removing active destination should trigger state change")
}

func TestGenerateDestinationID(t *testing.T) {
	id1 := generateDestinationID()
	id2 := generateDestinationID()
	require.Len(t, id1, 8)
	require.Len(t, id2, 8)
	require.NotEqual(t, id1, id2, "IDs should be unique")
}
