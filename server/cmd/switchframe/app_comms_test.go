package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/comms"
)

func TestHandleCommsStream_NilManager(t *testing.T) {
	app := &App{}
	// Should return immediately without panic.
	app.handleCommsStream("op1", &bytes.Buffer{}, &bytes.Buffer{})
}

func TestHandleCommsStream_UnknownParticipant(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	app := &App{commsMgr: mgr}
	// No one has joined, so GetParticipant returns false and we return early.
	app.handleCommsStream("op1", &bytes.Buffer{}, &bytes.Buffer{})
}

func TestHandleCommsStream_AudioIngest(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	// Join — if opus is unavailable, skip.
	if err := mgr.Join("op1", "Alice"); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}

	// Build a wire-protocol audio message.
	payload := make([]byte, 10) // short dummy opus data
	msg := makeWireMsg(commsMsgAudio, payload)

	r := bytes.NewReader(msg)
	w := &bytes.Buffer{}

	handleCommsStreamForTest(mgr, "op1", r, w)

	// The handler should have read the message and exited on EOF.
	// IngestAudio will have been called — we can't easily observe the result
	// without a real opus frame, but the handler should not panic.
}

func TestHandleCommsStream_ControlMute(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}

	// Build a mute control message.
	cmd := commsMuteCmd{Action: "mute", Muted: true}
	payload, _ := json.Marshal(cmd)
	msg := makeWireMsg(commsMsgControl, payload)

	r := bytes.NewReader(msg)
	w := &bytes.Buffer{}

	handleCommsStreamForTest(mgr, "op1", r, w)

	// Verify the participant is now muted.
	state := mgr.State()
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	for _, p := range state.Participants {
		if p.OperatorID == "op1" {
			if !p.Muted {
				t.Error("expected participant to be muted after control command")
			}
			return
		}
	}
	t.Error("participant op1 not found in state")
}

func TestHandleCommsStream_WriteLoop(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}

	// Use a pipe so the write goroutine can write and we can read.
	pr, pw := io.Pipe()

	// Use a reader that blocks until we close it, simulating a live stream.
	readReady := make(chan struct{})
	blockingReader := &blockingReadCloser{done: readReady}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handleCommsStreamForTest(mgr, "op1", blockingReader, pw)
		pw.Close()
	}()

	// Push a packet into the participant's send channel via the test helper.
	testPacket := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if !mgr.SendTestPacket("op1", testPacket) {
		t.Fatal("could not send test packet to participant channel")
	}

	// Read the wire-protocol message from the pipe.
	header := make([]byte, 3)
	if _, err := io.ReadFull(pr, header); err != nil {
		t.Fatalf("read header: %v", err)
	}

	if header[0] != commsMsgAudio {
		t.Errorf("expected message type 0x%02x, got 0x%02x", commsMsgAudio, header[0])
	}

	payloadLen := binary.BigEndian.Uint16(header[1:3])
	if int(payloadLen) != len(testPacket) {
		t.Fatalf("payload length = %d, want %d", payloadLen, len(testPacket))
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(pr, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}

	if !bytes.Equal(payload, testPacket) {
		t.Errorf("payload = %x, want %x", payload, testPacket)
	}

	// Close the blocking reader to let handleCommsStream exit.
	close(readReady)
	wg.Wait()
}

func TestHandleCommsStream_UnknownMsgType(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}

	// Build a message with an unknown type.
	msg := makeWireMsg(0xFF, []byte("hello"))

	r := bytes.NewReader(msg)
	w := &bytes.Buffer{}

	// Should not panic, just log and continue (then hit EOF).
	handleCommsStreamForTest(mgr, "op1", r, w)
}

func TestHandleCommsStream_MultipleMessages(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}

	// Build multiple messages in sequence.
	var buf bytes.Buffer

	// First: audio message.
	buf.Write(makeWireMsg(commsMsgAudio, []byte{0x01, 0x02}))

	// Second: control mute command.
	cmd := commsMuteCmd{Action: "mute", Muted: true}
	payload, _ := json.Marshal(cmd)
	buf.Write(makeWireMsg(commsMsgControl, payload))

	// Third: unknown type (should be skipped).
	buf.Write(makeWireMsg(0x99, []byte("ignored")))

	// Fourth: unmute command.
	cmd2 := commsMuteCmd{Action: "mute", Muted: false}
	payload2, _ := json.Marshal(cmd2)
	buf.Write(makeWireMsg(commsMsgControl, payload2))

	w := &bytes.Buffer{}
	handleCommsStreamForTest(mgr, "op1", &buf, w)

	// After processing all messages, participant should be unmuted.
	state := mgr.State()
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	for _, p := range state.Participants {
		if p.OperatorID == "op1" {
			if p.Muted {
				t.Error("expected participant to be unmuted after final control command")
			}
			return
		}
	}
	t.Error("participant op1 not found in state")
}

func TestHandleCommsStream_EmptyPayload(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}

	// Zero-length payload audio message.
	msg := makeWireMsg(commsMsgAudio, []byte{})

	r := bytes.NewReader(msg)
	w := &bytes.Buffer{}

	// Should not panic.
	handleCommsStreamForTest(mgr, "op1", r, w)
}

func TestHandleCommsStream_TruncatedHeader(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}

	// Only 2 bytes — incomplete header.
	r := bytes.NewReader([]byte{0x01, 0x00})
	w := &bytes.Buffer{}

	// Should exit cleanly.
	handleCommsStreamForTest(mgr, "op1", r, w)
}

func TestHandleCommsStream_TruncatedPayload(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}

	// Header says 10 bytes payload, but only 3 bytes follow.
	var buf bytes.Buffer
	buf.WriteByte(commsMsgAudio)
	buf.WriteByte(0x00)
	buf.WriteByte(0x0A) // 10 bytes
	buf.Write([]byte{0x01, 0x02, 0x03})

	w := &bytes.Buffer{}

	// Should exit cleanly on short read.
	handleCommsStreamForTest(mgr, "op1", &buf, w)
}

func TestHandleCommsStream_InvalidControlJSON(t *testing.T) {
	mgr := comms.NewManager(nil)
	defer mgr.Close()

	if err := mgr.Join("op1", "Alice"); err != nil {
		if errors.Is(err, comms.ErrOpusUnavailable) {
			t.Skip("opus codec not available")
		}
		t.Fatalf("Join: %v", err)
	}

	// Invalid JSON as control payload — should be skipped, not crash.
	msg := makeWireMsg(commsMsgControl, []byte("not json!!!"))

	r := bytes.NewReader(msg)
	w := &bytes.Buffer{}

	handleCommsStreamForTest(mgr, "op1", r, w)
}

// makeWireMsg builds a wire-protocol message: [type][len_hi][len_lo][payload].
func makeWireMsg(msgType byte, payload []byte) []byte {
	msg := make([]byte, 3+len(payload))
	msg[0] = msgType
	binary.BigEndian.PutUint16(msg[1:3], uint16(len(payload)))
	copy(msg[3:], payload)
	return msg
}

// blockingReadCloser blocks on Read until the done channel is closed,
// then returns io.EOF. Used to simulate a live connection that stays open.
type blockingReadCloser struct {
	done <-chan struct{}
	once sync.Once
}

func (b *blockingReadCloser) Read(p []byte) (int, error) {
	select {
	case <-b.done:
		return 0, io.EOF
	case <-time.After(5 * time.Second):
		return 0, io.EOF
	}
}
