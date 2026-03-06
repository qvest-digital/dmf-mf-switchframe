package debug

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEventLog_AddAndSnapshot(t *testing.T) {
	t.Parallel()
	log := NewEventLog(3) // capacity 3

	log.Add("test_event", map[string]any{"key": "val1"})
	log.Add("test_event", map[string]any{"key": "val2"})

	events := log.Snapshot()
	require.Len(t, events, 2)
	require.Equal(t, "test_event", events[0].Type)
	require.Equal(t, "val1", events[0].Detail["key"])
	require.False(t, events[0].Timestamp.IsZero(), "expected non-zero timestamp")
}

func TestEventLog_Wraparound(t *testing.T) {
	t.Parallel()
	log := NewEventLog(3)

	log.Add("a", nil)
	log.Add("b", nil)
	log.Add("c", nil)
	log.Add("d", nil) // overwrites "a"

	events := log.Snapshot()
	require.Len(t, events, 3)
	// Should be in chronological order: b, c, d
	require.Equal(t, "b", events[0].Type)
	require.Equal(t, "d", events[2].Type)
}

func TestEventLog_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	log := NewEventLog(100)
	done := make(chan struct{})

	// Writer
	go func() {
		for i := 0; i < 1000; i++ {
			log.Add("concurrent", nil)
		}
		close(done)
	}()

	// Reader
	for i := 0; i < 100; i++ {
		_ = log.Snapshot()
	}
	<-done
}
