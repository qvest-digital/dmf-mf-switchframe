// server/control/state_test.go
package control

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal"
)

func TestStatePublisherEncodesJSON(t *testing.T) {
	var published []byte
	pub := NewStatePublisher(func(data []byte) {
		published = data
	})

	state := internal.ControlRoomState{
		ProgramSource: "camera1",
		PreviewSource: "camera2",
		Seq:           1,
		Timestamp:     1709500000000,
		TallyState: map[string]internal.TallyStatus{
			"camera1": internal.TallyProgram,
			"camera2": internal.TallyPreview,
		},
		Sources: map[string]internal.SourceInfo{
			"camera1": {Key: "camera1", Status: internal.SourceHealthy},
		},
	}

	pub.Publish(state)

	if published == nil {
		t.Fatal("nothing published")
	}

	var decoded internal.ControlRoomState
	if err := json.Unmarshal(published, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ProgramSource != "camera1" {
		t.Errorf("ProgramSource = %q, want %q", decoded.ProgramSource, "camera1")
	}
	if decoded.Seq != 1 {
		t.Errorf("Seq = %d, want 1", decoded.Seq)
	}
}

func TestStatePublisherSequentialPublishes(t *testing.T) {
	var count int
	pub := NewStatePublisher(func(data []byte) {
		count++
	})
	for i := 0; i < 5; i++ {
		pub.Publish(internal.ControlRoomState{Seq: uint64(i)})
	}
	if count != 5 {
		t.Errorf("published %d times, want 5", count)
	}
}

func TestChannelPublisher(t *testing.T) {
	pub := NewChannelPublisher(2)

	// Publish a state
	state := internal.ControlRoomState{ProgramSource: "cam1", Seq: 1}
	pub.Publish(state)

	// Read from channel
	select {
	case data := <-pub.Ch():
		var got internal.ControlRoomState
		require.NoError(t, json.Unmarshal(data, &got))
		require.Equal(t, "cam1", got.ProgramSource)
	default:
		t.Fatal("expected data on channel")
	}

	// Test overflow: fill buffer, then publish one more
	pub.Publish(internal.ControlRoomState{Seq: 2})
	pub.Publish(internal.ControlRoomState{Seq: 3})
	pub.Publish(internal.ControlRoomState{Seq: 4}) // should drop seq 2

	data := <-pub.Ch()
	var got internal.ControlRoomState
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, uint64(3), got.Seq) // seq 2 was dropped
}

func TestChannelPublisherEmptyBuffer(t *testing.T) {
	pub := NewChannelPublisher(1)

	// Channel should be empty initially
	select {
	case <-pub.Ch():
		t.Fatal("expected empty channel")
	default:
		// expected
	}
}

func TestChannelPublisher_DroppedCounter(t *testing.T) {
	pub := NewChannelPublisher(2) // small buffer for testing

	// Publish 5 messages without reading — should drop 3
	for i := 0; i < 5; i++ {
		pub.Publish(internal.ControlRoomState{Seq: uint64(i)})
	}

	require.Equal(t, int64(3), pub.DroppedCount())
}

func TestChannelPublisherMultipleReads(t *testing.T) {
	pub := NewChannelPublisher(4)

	for i := uint64(1); i <= 4; i++ {
		pub.Publish(internal.ControlRoomState{Seq: i})
	}

	// Read all four in order
	for i := uint64(1); i <= 4; i++ {
		select {
		case data := <-pub.Ch():
			var got internal.ControlRoomState
			require.NoError(t, json.Unmarshal(data, &got))
			require.Equal(t, i, got.Seq)
		default:
			t.Fatalf("expected data for seq %d", i)
		}
	}
}
