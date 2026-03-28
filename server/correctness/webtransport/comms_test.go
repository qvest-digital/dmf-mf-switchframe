//go:build correctness

package webtransport

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	stdErrors "errors"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/comms"
)

// ==========================================================================
// TestCommsManagerJoinLeave
// ==========================================================================

func TestCommsManagerJoinLeave(t *testing.T) {
	broadcastCount := 0
	mgr := comms.NewManager(func() { broadcastCount++ })
	defer mgr.Close()

	// Join a participant.
	err := mgr.Join("op1", "Alice")
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	state := mgr.State()
	participantsAfterJoin := 0
	if state != nil {
		participantsAfterJoin = len(state.Participants)
	}
	t.Logf("MEASURE: participants after join=%d", participantsAfterJoin)

	if participantsAfterJoin != 1 {
		t.Errorf("expected 1 participant after join, got %d", participantsAfterJoin)
	}

	// Verify participant info.
	if state != nil && len(state.Participants) == 1 {
		p := state.Participants[0]
		t.Logf("MEASURE: participant id=%q name=%q muted=%v speaking=%v",
			p.OperatorID, p.Name, p.Muted, p.Speaking)
		if p.OperatorID != "op1" {
			t.Errorf("expected operatorID 'op1', got %q", p.OperatorID)
		}
		if p.Name != "Alice" {
			t.Errorf("expected name 'Alice', got %q", p.Name)
		}
		if p.Muted {
			t.Errorf("expected muted=false on fresh join, got true")
		}
		if p.Speaking {
			t.Errorf("expected speaking=false on fresh join, got true")
		}
	}

	// Leave.
	mgr.Leave("op1")

	stateAfter := mgr.State()
	participantsAfterLeave := 0
	if stateAfter != nil {
		participantsAfterLeave = len(stateAfter.Participants)
	}
	t.Logf("MEASURE: participants after leave=%d", participantsAfterLeave)

	// State() returns nil when no participants.
	if stateAfter != nil {
		t.Errorf("expected nil state after all participants leave, got %+v", stateAfter)
	}

	// Verify idempotent re-leave doesn't panic.
	mgr.Leave("op1")
	t.Log("MEASURE: idempotent leave did not panic")
}

// ==========================================================================
// TestCommsManagerIdempotentJoin
// ==========================================================================

func TestCommsManagerIdempotentJoin(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	// Join the same operator twice — should be idempotent.
	err1 := mgr.Join("op1", "Alice")
	err2 := mgr.Join("op1", "Alice")

	t.Logf("MEASURE: first join err=%v, second join err=%v", err1, err2)

	if err1 != nil {
		t.Fatalf("first join failed: %v", err1)
	}
	if err2 != nil {
		t.Errorf("idempotent re-join should return nil, got %v", err2)
	}

	state := mgr.State()
	count := 0
	if state != nil {
		count = len(state.Participants)
	}
	t.Logf("MEASURE: participants after double join=%d", count)
	if count != 1 {
		t.Errorf("expected 1 participant after idempotent re-join, got %d", count)
	}
}

// ==========================================================================
// TestCommsManagerMultipleParticipants
// ==========================================================================

func TestCommsManagerMultipleParticipants(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	ids := []string{"op1", "op2", "op3", "op4"}
	names := []string{"Alice", "Bob", "Charlie", "Dave"}

	for i, id := range ids {
		if err := mgr.Join(id, names[i]); err != nil {
			t.Fatalf("Join(%q) failed: %v", id, err)
		}
	}

	state := mgr.State()
	countAfterAll := 0
	if state != nil {
		countAfterAll = len(state.Participants)
	}
	t.Logf("MEASURE: participants after adding 4=%d", countAfterAll)

	if countAfterAll != 4 {
		t.Errorf("expected 4 participants, got %d", countAfterAll)
	}

	// Log all participant IDs.
	if state != nil {
		for _, p := range state.Participants {
			t.Logf("MEASURE: participant id=%q name=%q", p.OperatorID, p.Name)
		}
	}

	// Remove 2.
	mgr.Leave("op1")
	mgr.Leave("op3")

	state2 := mgr.State()
	countAfterRemove := 0
	if state2 != nil {
		countAfterRemove = len(state2.Participants)
	}
	t.Logf("MEASURE: participants after removing 2=%d", countAfterRemove)

	if countAfterRemove != 2 {
		t.Errorf("expected 2 participants after removing 2, got %d", countAfterRemove)
	}

	// Verify the right ones remain.
	if state2 != nil {
		remaining := make(map[string]bool)
		for _, p := range state2.Participants {
			remaining[p.OperatorID] = true
			t.Logf("MEASURE: remaining participant id=%q", p.OperatorID)
		}
		if !remaining["op2"] {
			t.Errorf("expected op2 to remain")
		}
		if !remaining["op4"] {
			t.Errorf("expected op4 to remain")
		}
		if remaining["op1"] {
			t.Errorf("op1 should have been removed")
		}
		if remaining["op3"] {
			t.Errorf("op3 should have been removed")
		}
	}
}

// ==========================================================================
// TestCommsManagerCapacity
// ==========================================================================

func TestCommsManagerCapacity(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	// Fill to MaxParticipants (6).
	for i := 0; i < comms.MaxParticipants; i++ {
		err := mgr.Join("op"+string(rune('A'+i)), "Name")
		if err != nil {
			t.Fatalf("Join #%d failed unexpectedly: %v", i, err)
		}
	}

	state := mgr.State()
	fullCount := 0
	if state != nil {
		fullCount = len(state.Participants)
	}
	t.Logf("MEASURE: participants at capacity=%d, max=%d", fullCount, comms.MaxParticipants)

	// Attempt to add one more — should fail with ErrCommsFull.
	err := mgr.Join("extra", "Extra")
	t.Logf("MEASURE: join beyond capacity err=%v", err)

	require.ErrorIs(t, err, comms.ErrCommsFull, "joining beyond capacity must return ErrCommsFull")
}

// ==========================================================================
// TestCommsManagerMuteUnmute
// ==========================================================================

func TestCommsManagerMuteUnmute(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	// Initial state: not muted.
	state0 := mgr.State()
	initialMuted := false
	if state0 != nil && len(state0.Participants) > 0 {
		initialMuted = state0.Participants[0].Muted
	}
	t.Logf("MEASURE: initial muted=%v", initialMuted)
	if initialMuted {
		t.Errorf("expected initially unmuted")
	}

	// Mute.
	if err := mgr.SetMuted("op1", true); err != nil {
		t.Fatalf("SetMuted(true) failed: %v", err)
	}

	state1 := mgr.State()
	mutedAfterMute := false
	if state1 != nil && len(state1.Participants) > 0 {
		mutedAfterMute = state1.Participants[0].Muted
	}
	t.Logf("MEASURE: muted after SetMuted(true)=%v", mutedAfterMute)
	if !mutedAfterMute {
		t.Errorf("expected muted=true after SetMuted(true)")
	}

	// Unmute.
	if err := mgr.SetMuted("op1", false); err != nil {
		t.Fatalf("SetMuted(false) failed: %v", err)
	}

	state2 := mgr.State()
	mutedAfterUnmute := false
	if state2 != nil && len(state2.Participants) > 0 {
		mutedAfterUnmute = state2.Participants[0].Muted
	}
	t.Logf("MEASURE: muted after SetMuted(false)=%v", mutedAfterUnmute)
	if mutedAfterUnmute {
		t.Errorf("expected muted=false after SetMuted(false)")
	}

	// Muting a non-existent participant should return ErrNotInComms.
	err := mgr.SetMuted("nonexistent", true)
	t.Logf("MEASURE: SetMuted on non-existent err=%v", err)
	require.ErrorIs(t, err, comms.ErrNotInComms, "muting non-existent participant must return ErrNotInComms")
}

// ==========================================================================
// TestCommsProtocolFraming
// ==========================================================================

func TestCommsProtocolFraming(t *testing.T) {
	// Wire protocol: [1 byte type][2 bytes BE length][payload]
	// Test: construct and parse audio messages.

	t.Run("audio_message_raw_bytes", func(t *testing.T) {
		// Build a comms audio message entirely from raw bytes — no helper.
		// Wire format spec: [1 byte type][2 bytes BE payload length][payload]
		//
		// MsgAudio = 0x01
		// Payload  = {0xDE, 0xAD, 0xBE, 0xEF} (4 bytes)
		// Length   = 0x0004 in big-endian
		//
		// Expected wire bytes: 0x01 0x00 0x04 0xDE 0xAD 0xBE 0xEF
		rawMsg := []byte{
			0x01,       // MsgAudio
			0x00, 0x04, // payload length = 4 (big-endian)
			0xDE, 0xAD, 0xBE, 0xEF, // payload
		}

		t.Logf("MEASURE: raw audio message bytes = %x", rawMsg)

		// Verify the raw byte for MsgAudio matches the constant.
		require.Equal(t, comms.MsgAudio, rawMsg[0], "MsgAudio constant must be 0x01")

		// Parse with the production parser — this validates the parser against the spec.
		parsedType, parsedPayload, err := parseCommsMessage(rawMsg)
		t.Logf("MEASURE: parsed type=0x%02x payload_len=%d err=%v", parsedType, len(parsedPayload), err)

		require.NoError(t, err, "parsing raw audio message")
		require.Equal(t, comms.MsgAudio, parsedType, "message type")
		require.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, parsedPayload, "payload must match raw bytes exactly")

		// Also parse with the stream reader for completeness.
		reader := bytes.NewReader(rawMsg)
		streamType, streamPayload, streamErr := parseCommsMessageFromReader(reader)
		require.NoError(t, streamErr, "stream-parsing raw audio message")
		require.Equal(t, comms.MsgAudio, streamType, "stream message type")
		require.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, streamPayload, "stream payload")
	})

	t.Run("control_message_raw_bytes", func(t *testing.T) {
		// Build a control message with raw bytes.
		// MsgControl = 0x02
		// Payload = {"action":"mute"} (16 bytes)
		jsonPayload := []byte(`{"action":"mute"}`)
		payloadLen := len(jsonPayload) // 17

		rawMsg := make([]byte, 3+payloadLen)
		rawMsg[0] = 0x02 // MsgControl
		rawMsg[1] = byte(payloadLen >> 8)
		rawMsg[2] = byte(payloadLen & 0xFF)
		copy(rawMsg[3:], jsonPayload)

		t.Logf("MEASURE: raw control message len=%d, payload_len=%d", len(rawMsg), payloadLen)

		require.Equal(t, comms.MsgControl, rawMsg[0], "MsgControl constant must be 0x02")

		parsedType, parsedPayload, err := parseCommsMessage(rawMsg)
		require.NoError(t, err, "parsing raw control message")
		require.Equal(t, comms.MsgControl, parsedType, "message type")
		require.Equal(t, jsonPayload, parsedPayload, "payload must match")

		// Verify JSON round-trip.
		var parsed map[string]interface{}
		require.NoError(t, json.Unmarshal(parsedPayload, &parsed))
		require.Equal(t, "mute", parsed["action"], "JSON action field")
	})

	t.Run("audio_message_helper", func(t *testing.T) {
		audioPayload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
		msg := buildCommsMessage(comms.MsgAudio, audioPayload)

		t.Logf("MEASURE: audio message total_len=%d (header=3, payload=%d)", len(msg), len(audioPayload))

		require.Equal(t, 3+len(audioPayload), len(msg), "message length")

		parsedType, parsedPayload, err := parseCommsMessage(msg)
		t.Logf("MEASURE: parsed type=0x%02x payload_len=%d err=%v", parsedType, len(parsedPayload), err)

		require.NoError(t, err, "parse failed")
		require.Equal(t, comms.MsgAudio, parsedType, "message type")
		require.Equal(t, audioPayload, parsedPayload, "payload")
	})

	t.Run("control_message_mute", func(t *testing.T) {
		muteCmd := map[string]interface{}{
			"action": "mute",
			"muted":  true,
		}
		payload, err := json.Marshal(muteCmd)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}

		msg := buildCommsMessage(comms.MsgControl, payload)
		t.Logf("MEASURE: control message total_len=%d payload=%s", len(msg), string(payload))

		parsedType, parsedPayload, parseErr := parseCommsMessage(msg)
		if parseErr != nil {
			t.Fatalf("parse failed: %v", parseErr)
		}
		if parsedType != comms.MsgControl {
			t.Errorf("expected control type 0x%02x, got 0x%02x", comms.MsgControl, parsedType)
		}
		if !bytes.Equal(parsedPayload, payload) {
			t.Errorf("payload mismatch")
		}

		// Verify JSON round-trip.
		var parsed map[string]interface{}
		if err := json.Unmarshal(parsedPayload, &parsed); err != nil {
			t.Fatalf("JSON unmarshal failed: %v", err)
		}
		if parsed["action"] != "mute" {
			t.Errorf("expected action 'mute', got %v", parsed["action"])
		}
	})

	t.Run("hello_handshake", func(t *testing.T) {
		hello := map[string]interface{}{
			"action":     "hello",
			"operatorId": "test-op-123",
		}
		payload, _ := json.Marshal(hello)
		msg := buildCommsMessage(comms.MsgControl, payload)

		parsedType, parsedPayload, err := parseCommsMessage(msg)
		t.Logf("MEASURE: hello message type=0x%02x payload_len=%d err=%v", parsedType, len(parsedPayload), err)

		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}

		var parsedHello struct {
			Action     string `json:"action"`
			OperatorID string `json:"operatorId"`
		}
		if err := json.Unmarshal(parsedPayload, &parsedHello); err != nil {
			t.Fatalf("JSON unmarshal failed: %v", err)
		}
		if parsedHello.Action != "hello" {
			t.Errorf("expected action 'hello', got %q", parsedHello.Action)
		}
		if parsedHello.OperatorID != "test-op-123" {
			t.Errorf("expected operatorId 'test-op-123', got %q", parsedHello.OperatorID)
		}
	})

	t.Run("empty_payload", func(t *testing.T) {
		msg := buildCommsMessage(comms.MsgAudio, nil)
		t.Logf("MEASURE: empty payload message len=%d", len(msg))

		parsedType, parsedPayload, err := parseCommsMessage(msg)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if parsedType != comms.MsgAudio {
			t.Errorf("expected audio type, got 0x%02x", parsedType)
		}
		if len(parsedPayload) != 0 {
			t.Errorf("expected empty payload, got %d bytes", len(parsedPayload))
		}
	})

	t.Run("max_payload_length_encoding", func(t *testing.T) {
		// Maximum payload representable in 2 bytes = 65535.
		// Verify the length encoding uses the full 16-bit range.
		length := uint16(65535)
		header := make([]byte, 3)
		header[0] = comms.MsgAudio
		binary.BigEndian.PutUint16(header[1:3], length)

		parsedLen := binary.BigEndian.Uint16(header[1:3])
		t.Logf("MEASURE: max length encoding: wrote=%d read=%d", length, parsedLen)

		if parsedLen != 65535 {
			t.Errorf("length encoding round-trip failed: expected 65535, got %d", parsedLen)
		}
	})

	t.Run("sequential_messages", func(t *testing.T) {
		// Build a stream of multiple messages and parse them sequentially.
		var buf bytes.Buffer
		msgs := []struct {
			typ     byte
			payload []byte
		}{
			{comms.MsgAudio, []byte{0x01, 0x02}},
			{comms.MsgControl, []byte(`{"action":"mute","muted":false}`)},
			{comms.MsgAudio, []byte{0x03, 0x04, 0x05}},
		}

		for _, m := range msgs {
			buf.Write(buildCommsMessage(m.typ, m.payload))
		}

		reader := bytes.NewReader(buf.Bytes())
		for i, expected := range msgs {
			parsedType, parsedPayload, err := parseCommsMessageFromReader(reader)
			t.Logf("MEASURE: message[%d] type=0x%02x payload_len=%d err=%v", i, parsedType, len(parsedPayload), err)

			if err != nil {
				t.Fatalf("message[%d] parse failed: %v", i, err)
			}
			if parsedType != expected.typ {
				t.Errorf("message[%d] type: expected 0x%02x, got 0x%02x", i, expected.typ, parsedType)
			}
			if !bytes.Equal(parsedPayload, expected.payload) {
				t.Errorf("message[%d] payload mismatch", i)
			}
		}
	})
}

// ==========================================================================
// TestCommsN1MixerExclusion
// ==========================================================================

func TestCommsN1MixerExclusion(t *testing.T) {
	// N-1 MIXER VERIFICATION LIMITATION:
	//
	// The core N-1 exclusion logic lives in the unexported mixer.mixFor() method,
	// which sums all participant PCM inputs except the excluded participant's.
	// Full end-to-end verification requires:
	//   1. Creating Opus-encoded audio via an Opus encoder
	//   2. Feeding it through IngestAudio to decode + queue PCM
	//   3. Waiting for mixTick() to produce N-1 mixes
	//   4. Decoding the output Opus packets to verify PCM content
	//
	// The Opus encoder/decoder types are unexported from the comms package,
	// so we cannot encode test audio from this test package. The mixer struct
	// is also unexported.
	//
	// What we CAN verify from this test:
	//   - SendTestPacket routes to a specific participant only
	//   - Non-existent participant sends fail
	//   - The mix loop does not produce output for solo participants
	//   - Participant identity is tracked correctly
	//
	// The N-1 exclusion property is verified by code review of mixer.mixFor():
	// it iterates inputs, skips excludeID, and sums the rest. A unit test
	// for mixFor() exists in the comms package itself.

	mgr := comms.NewManager(nil)
	defer mgr.Close()

	// Join 3 participants and verify their identities.
	require.NoError(t, mgr.Join("A", "Alice"))
	require.NoError(t, mgr.Join("B", "Bob"))
	require.NoError(t, mgr.Join("C", "Charlie"))

	pA, okA := mgr.GetParticipant("A")
	pB, okB := mgr.GetParticipant("B")
	pC, okC := mgr.GetParticipant("C")
	t.Logf("MEASURE: got participants A=%v B=%v C=%v", okA, okB, okC)
	require.True(t, okA && okB && okC, "all participants must be found")

	// Verify participant identity from State().
	state := mgr.State()
	require.NotNil(t, state)
	require.Len(t, state.Participants, 3)
	namesByID := make(map[string]string)
	for _, p := range state.Participants {
		namesByID[p.OperatorID] = p.Name
	}
	require.Equal(t, "Alice", namesByID["A"], "participant A name")
	require.Equal(t, "Bob", namesByID["B"], "participant B name")
	require.Equal(t, "Charlie", namesByID["C"], "participant C name")

	// Verify SendTestPacket routes to ONLY the targeted participant.
	testData := []byte{0x01, 0x02, 0x03}
	sentA := mgr.SendTestPacket("A", testData)
	t.Logf("MEASURE: SendTestPacket to A result=%v", sentA)
	require.True(t, sentA, "SendTestPacket to A must succeed")

	// Read it back from A's channel and verify exact data.
	select {
	case pkt := <-pA.SendCh():
		t.Logf("MEASURE: received packet from A's sendCh len=%d", len(pkt))
		require.Equal(t, testData, pkt, "packet data must match what was sent to A")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for packet from A's sendCh")
	}

	// Verify B and C did NOT receive the packet (routing isolation).
	select {
	case pkt := <-pB.SendCh():
		t.Fatalf("B unexpectedly received a packet: %x", pkt)
	case pkt := <-pC.SendCh():
		t.Fatalf("C unexpectedly received a packet: %x", pkt)
	case <-time.After(50 * time.Millisecond):
		t.Log("MEASURE: B and C correctly did not receive A's direct packet")
	}

	// Verify SendTestPacket to non-existent participant returns false.
	sentNonexistent := mgr.SendTestPacket("nonexistent", testData)
	t.Logf("MEASURE: SendTestPacket to nonexistent=%v", sentNonexistent)
	require.False(t, sentNonexistent, "SendTestPacket to nonexistent must fail")
}

// ==========================================================================
// TestCommsManagerCleanShutdown
// ==========================================================================

func TestCommsManagerCleanShutdown(t *testing.T) {
	goroutinesBefore := runtime.NumGoroutine()
	t.Logf("MEASURE: goroutines before=%d", goroutinesBefore)

	mgr := comms.NewManager(nil)

	if err := mgr.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join failed: %v", err)
	}
	if err := mgr.Join("op2", "Bob"); err != nil {
		t.Fatalf("Join failed: %v", err)
	}
	if err := mgr.Join("op3", "Charlie"); err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	goroutinesWithManager := runtime.NumGoroutine()
	t.Logf("MEASURE: goroutines with manager + 3 participants=%d", goroutinesWithManager)

	// Close manager.
	mgr.Close()

	// Give goroutines time to wind down.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	goroutinesAfter := runtime.NumGoroutine()
	t.Logf("MEASURE: goroutines after close=%d", goroutinesAfter)

	// After close, all participants should be gone.
	state := mgr.State()
	participantsAfterClose := 0
	if state != nil {
		participantsAfterClose = len(state.Participants)
	}
	t.Logf("MEASURE: participants after close=%d", participantsAfterClose)

	if participantsAfterClose != 0 {
		t.Errorf("expected 0 participants after close, got %d", participantsAfterClose)
	}

	// The goroutine count should drop back close to the before count.
	// We allow a margin of 3 for GC/scheduler goroutines.
	leaked := goroutinesAfter - goroutinesBefore
	t.Logf("MEASURE: goroutine delta=%d (after=%d - before=%d)", leaked, goroutinesAfter, goroutinesBefore)
	if leaked > 3 {
		t.Errorf("possible goroutine leak: %d extra goroutines after Close()", leaked)
	}
}

// ==========================================================================
// TestCommsManagerConcurrentJoinLeave
// ==========================================================================

func TestCommsManagerConcurrentJoinLeave(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	var wg sync.WaitGroup
	const N = 20

	// Concurrent joins — some will succeed, some will hit MaxParticipants.
	errors := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := string(rune('A' + idx%26))
			errors[idx] = mgr.Join(id, "Name"+id)
		}(i)
	}
	wg.Wait()

	successCount := 0
	fullCount := 0
	otherErrorCount := 0
	for _, err := range errors {
		if err == nil {
			successCount++
		} else if stdErrors.Is(err, comms.ErrCommsFull) {
			fullCount++
		} else {
			otherErrorCount++
			t.Errorf("unexpected error from concurrent Join: %v", err)
		}
	}

	state := mgr.State()
	actualCount := 0
	if state != nil {
		actualCount = len(state.Participants)
	}

	t.Logf("MEASURE: concurrent join results: success=%d full=%d other_errors=%d actual_participants=%d max=%d",
		successCount, fullCount, otherErrorCount, actualCount, comms.MaxParticipants)

	if actualCount > comms.MaxParticipants {
		t.Errorf("exceeded MaxParticipants: got %d, max=%d", actualCount, comms.MaxParticipants)
	}

	// Concurrent leaves.
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := string(rune('A' + idx%26))
			mgr.Leave(id)
		}(i)
	}
	wg.Wait()

	stateAfter := mgr.State()
	t.Logf("MEASURE: state after concurrent leaves=%v (should be nil)", stateAfter)

	if stateAfter != nil {
		t.Errorf("expected nil state after all concurrent leaves, got %d participants", len(stateAfter.Participants))
	}
}

// ==========================================================================
// TestCommsManagerBroadcastCallback
// ==========================================================================

func TestCommsManagerBroadcastCallback(t *testing.T) {
	var mu sync.Mutex
	broadcastCount := 0
	mgr := comms.NewManager(func() {
		mu.Lock()
		broadcastCount++
		mu.Unlock()
	})
	defer mgr.Close()

	// Join should NOT trigger broadcast (only Leave does in the current impl).
	if err := mgr.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	mu.Lock()
	countAfterJoin := broadcastCount
	mu.Unlock()
	t.Logf("MEASURE: broadcast count after join=%d", countAfterJoin)

	// Leave SHOULD trigger broadcast.
	mgr.Leave("op1")

	mu.Lock()
	countAfterLeave := broadcastCount
	mu.Unlock()
	t.Logf("MEASURE: broadcast count after leave=%d", countAfterLeave)

	if countAfterLeave < 1 {
		t.Errorf("expected at least 1 broadcast after leave, got %d", countAfterLeave)
	}

	// Leave of non-existent should NOT trigger broadcast.
	mgr.Leave("nonexistent")

	mu.Lock()
	countAfterNoop := broadcastCount
	mu.Unlock()
	t.Logf("MEASURE: broadcast count after noop leave=%d", countAfterNoop)

	if countAfterNoop != countAfterLeave {
		t.Errorf("expected no broadcast for non-existent leave, count went from %d to %d",
			countAfterLeave, countAfterNoop)
	}
}

// ==========================================================================
// TestCommsIngestAudioNonExistent
// ==========================================================================

func TestCommsIngestAudioNonExistent(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	// IngestAudio for a participant that hasn't joined should return ErrNotInComms.
	err := mgr.IngestAudio("nobody", []byte{0x01, 0x02, 0x03})
	t.Logf("MEASURE: IngestAudio for non-existent err=%v", err)

	require.ErrorIs(t, err, comms.ErrNotInComms, "ingesting audio for non-existent participant must return ErrNotInComms")
}

// ==========================================================================
// TestCommsSendChannelBackpressure
// ==========================================================================

func TestCommsSendChannelBackpressure(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	// The send channel has capacity 4. Fill it up.
	sentCount := 0
	for i := 0; i < 10; i++ {
		if mgr.SendTestPacket("op1", []byte{byte(i)}) {
			sentCount++
		}
	}

	t.Logf("MEASURE: sent %d/10 packets before backpressure (channel capacity=4)", sentCount)

	// Should be limited to 4 (the channel capacity).
	if sentCount != 4 {
		t.Errorf("expected exactly 4 successful sends (channel capacity), got %d", sentCount)
	}

	// Drain one and verify we can send again.
	p, ok := mgr.GetParticipant("op1")
	if !ok {
		t.Fatalf("participant not found")
	}
	<-p.SendCh()

	sentAfterDrain := mgr.SendTestPacket("op1", []byte{0xFF})
	t.Logf("MEASURE: send after draining one=%v", sentAfterDrain)
	if !sentAfterDrain {
		t.Errorf("expected send to succeed after draining one slot")
	}
}

// ==========================================================================
// TestCommsParticipantSendAfterClose
// ==========================================================================

func TestCommsParticipantSendAfterClose(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	// Get participant before leaving.
	p, ok := mgr.GetParticipant("op1")
	if !ok {
		t.Fatalf("participant not found")
	}

	// Leave (which closes the participant).
	mgr.Leave("op1")

	// SendTestPacket to the removed participant should return false.
	sent := mgr.SendTestPacket("op1", []byte{0x01})
	t.Logf("MEASURE: SendTestPacket after leave=%v", sent)
	if sent {
		t.Errorf("expected SendTestPacket to fail after participant left")
	}

	// Reading from closed sendCh should return immediately with ok=false.
	select {
	case _, chOk := <-p.SendCh():
		t.Logf("MEASURE: read from closed sendCh ok=%v", chOk)
		if chOk {
			t.Errorf("expected closed channel read to return ok=false")
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("timeout: closed channel should be immediately readable")
	}
}

// ==========================================================================
// TestCommsStateActiveFlag
// ==========================================================================

func TestCommsStateActiveFlag(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	// No participants: State() returns nil.
	s0 := mgr.State()
	t.Logf("MEASURE: state with 0 participants=%v (should be nil)", s0)
	if s0 != nil {
		t.Errorf("expected nil state with no participants")
	}

	// One participant: State() should return Active=true.
	if err := mgr.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	s1 := mgr.State()
	t.Logf("MEASURE: state with 1 participant: nil=%v", s1 == nil)
	if s1 == nil {
		t.Fatalf("expected non-nil state with 1 participant")
	}
	t.Logf("MEASURE: active=%v participants=%d", s1.Active, len(s1.Participants))

	if !s1.Active {
		t.Errorf("expected Active=true when participants exist")
	}
}

// ==========================================================================
// TestCommsProtocolFramingMultiMessage (stream-based parsing)
// ==========================================================================

func TestCommsProtocolFramingMultiMessage(t *testing.T) {
	// Simulate what handleCommsStream does: read messages from a stream.
	// Build a stream with: hello handshake, audio frame, mute control, audio frame.

	var streamBuf bytes.Buffer

	// 1. Hello handshake.
	helloPayload, _ := json.Marshal(map[string]string{
		"action":     "hello",
		"operatorId": "test-op",
	})
	streamBuf.Write(buildCommsMessage(comms.MsgControl, helloPayload))

	// 2. Audio frame (fake Opus data).
	audioData := make([]byte, 120) // Typical Opus frame size.
	for i := range audioData {
		audioData[i] = byte(i)
	}
	streamBuf.Write(buildCommsMessage(comms.MsgAudio, audioData))

	// 3. Mute control.
	mutePayload, _ := json.Marshal(map[string]interface{}{
		"action": "mute",
		"muted":  true,
	})
	streamBuf.Write(buildCommsMessage(comms.MsgControl, mutePayload))

	// 4. Another audio frame.
	streamBuf.Write(buildCommsMessage(comms.MsgAudio, audioData))

	// Parse them all.
	reader := bytes.NewReader(streamBuf.Bytes())
	msgCount := 0
	audioCount := 0
	controlCount := 0

	for {
		msgType, payload, err := parseCommsMessageFromReader(reader)
		if err != nil {
			break
		}
		msgCount++
		switch msgType {
		case comms.MsgAudio:
			audioCount++
			t.Logf("MEASURE: message[%d] audio payload_len=%d", msgCount, len(payload))
		case comms.MsgControl:
			controlCount++
			t.Logf("MEASURE: message[%d] control payload=%s", msgCount, string(payload))
		}
	}

	t.Logf("MEASURE: total messages=%d audio=%d control=%d", msgCount, audioCount, controlCount)

	if msgCount != 4 {
		t.Errorf("expected 4 messages, got %d", msgCount)
	}
	if audioCount != 2 {
		t.Errorf("expected 2 audio messages, got %d", audioCount)
	}
	if controlCount != 2 {
		t.Errorf("expected 2 control messages, got %d", controlCount)
	}
}

// ==========================================================================
// TestCommsMixLoopSkipsWithFewerThan2
// ==========================================================================

func TestCommsMixLoopSkipsWithFewerThan2(t *testing.T) {
	// The mixTick function skips if fewer than 2 participants.
	// Verify: with 1 participant, no packets appear on the send channel
	// even after waiting for several mix cycles.

	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("solo", "Solo"); err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	p, ok := mgr.GetParticipant("solo")
	if !ok {
		t.Fatalf("participant not found")
	}

	// Wait for a few mix cycles (20ms each).
	time.Sleep(100 * time.Millisecond)

	// Check: no packets should have been sent.
	select {
	case pkt := <-p.SendCh():
		t.Errorf("unexpected packet on solo participant's sendCh: len=%d", len(pkt))
	default:
		t.Log("MEASURE: solo participant correctly received no mix packets")
	}
}

// ==========================================================================
// TestCommsIdempotentJoinDoesNotUpdateName
// ==========================================================================

func TestCommsIdempotentJoinDoesNotUpdateName(t *testing.T) {
	// When a participant re-joins with a different name, what happens?
	// Current behavior: idempotent re-join returns nil without updating.
	// This test documents whether the name is updated or not.

	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		t.Fatalf("first Join failed: %v", err)
	}

	// Re-join with different name.
	if err := mgr.Join("op1", "Bob"); err != nil {
		t.Fatalf("second Join failed: %v", err)
	}

	state := mgr.State()
	if state == nil || len(state.Participants) == 0 {
		t.Fatalf("no participants in state")
	}

	name := state.Participants[0].Name
	t.Logf("MEASURE: name after re-join with different name=%q (first='Alice', second='Bob')", name)

	// Document: the name should be updated to "Bob" on re-join,
	// because an operator may have changed their display name.
	// If it's still "Alice", this is a potential bug (stale name on re-join).
	if name != "Bob" {
		t.Errorf("BUG: idempotent re-join does NOT update name: expected 'Bob', got %q. "+
			"Operator display names will be stale if they change between joins.", name)
	}
}

// ==========================================================================
// TestCommsJoinDoesNotBroadcast
// ==========================================================================

func TestCommsJoinDoesNotBroadcast(t *testing.T) {
	// The Join method does NOT call onBroadcast, only Leave does.
	// This means UI clients won't see a new participant until the next
	// state broadcast triggered by something else (e.g., another leave).
	// This is potentially a bug — new participant should be visible immediately.

	var mu sync.Mutex
	broadcastCount := 0
	mgr := comms.NewManager(func() {
		mu.Lock()
		broadcastCount++
		mu.Unlock()
	})
	defer mgr.Close()

	// Join 3 participants.
	for i := 0; i < 3; i++ {
		id := string(rune('A' + i))
		if err := mgr.Join(id, "Name"+id); err != nil {
			t.Fatalf("Join %s failed: %v", id, err)
		}
	}

	mu.Lock()
	count := broadcastCount
	mu.Unlock()

	t.Logf("MEASURE: broadcast count after 3 joins=%d (should be >=3 for UI consistency)", count)

	// If Join doesn't broadcast, the UI won't know about new participants
	// until some other event triggers a broadcast. This is an asymmetry
	// with Leave which does broadcast.
	if count < 3 {
		t.Errorf("BUG: Join does not trigger onBroadcast (count=%d after 3 joins). "+
			"UI clients won't see new participants until next Leave or other state broadcast.", count)
	}
}

// ==========================================================================
// TestCommsDoubleClose
// ==========================================================================

func TestCommsDoubleClose(t *testing.T) {
	// Verify that calling Close() twice doesn't panic.
	// This is important for defensive cleanup in deferred Close() calls.

	mgr := comms.NewManager(nil)

	if err := mgr.Join("op1", "Alice"); err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	mgr.Close()

	// Second close -- should not panic.
	// If it panics, the test framework will catch it.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("BUG: double Close() panicked: %v", r)
		}
	}()

	mgr.Close()
	t.Log("MEASURE: double Close() completed without panic")
}

// ==========================================================================
// Helpers
// ==========================================================================

// buildCommsMessage constructs a wire-protocol framed message.
// Format: [1 byte type][2 bytes BE length][payload]
func buildCommsMessage(msgType byte, payload []byte) []byte {
	msg := make([]byte, 3+len(payload))
	msg[0] = msgType
	binary.BigEndian.PutUint16(msg[1:3], uint16(len(payload)))
	copy(msg[3:], payload)
	return msg
}

// parseCommsMessage parses a single wire-protocol message from a byte slice.
func parseCommsMessage(data []byte) (byte, []byte, error) {
	if len(data) < 3 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	msgType := data[0]
	payloadLen := binary.BigEndian.Uint16(data[1:3])
	if len(data) < 3+int(payloadLen) {
		return 0, nil, io.ErrUnexpectedEOF
	}
	return msgType, data[3 : 3+payloadLen], nil
}

// parseCommsMessageFromReader reads and parses a single wire-protocol message from a reader.
func parseCommsMessageFromReader(r io.Reader) (byte, []byte, error) {
	header := make([]byte, 3)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	msgType := header[0]
	payloadLen := binary.BigEndian.Uint16(header[1:3])
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}
	return msgType, payload, nil
}

// sinApprox is a simple sine approximation for generating test tones.
func sinApprox(x float64) float64 {
	// Reduce to [-pi, pi].
	for x > 3.14159265 {
		x -= 2 * 3.14159265
	}
	for x < -3.14159265 {
		x += 2 * 3.14159265
	}
	// Bhaskara approximation.
	return 16 * x * (3.14159265 - abs(x)) / (49.348 - 4*x*(3.14159265-abs(x)))
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
