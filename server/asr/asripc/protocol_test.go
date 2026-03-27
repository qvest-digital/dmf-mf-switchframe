package asripc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"testing"
)

func TestRoundtripTranscript(t *testing.T) {
	var buf bytes.Buffer

	msg := &Message{
		Type:    "transcript",
		IsFinal: true,
		Words: []Word{
			{Text: "hello", Confidence: 0.95, GapBefore: 0},
			{Text: "world", Confidence: 0.88, GapBefore: 120},
		},
	}

	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	got, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got.Type != "transcript" {
		t.Errorf("Type = %q, want %q", got.Type, "transcript")
	}
	if !got.IsFinal {
		t.Error("IsFinal = false, want true")
	}
	if len(got.Words) != 2 {
		t.Fatalf("len(Words) = %d, want 2", len(got.Words))
	}
	if got.Words[0].Text != "hello" {
		t.Errorf("Words[0].Text = %q, want %q", got.Words[0].Text, "hello")
	}
	if got.Words[0].Confidence != 0.95 {
		t.Errorf("Words[0].Confidence = %v, want 0.95", got.Words[0].Confidence)
	}
	if got.Words[1].Text != "world" {
		t.Errorf("Words[1].Text = %q, want %q", got.Words[1].Text, "world")
	}
	if got.Words[1].GapBefore != 120 {
		t.Errorf("Words[1].GapBefore = %d, want 120", got.Words[1].GapBefore)
	}
}

func TestRoundtripControl(t *testing.T) {
	cmds := []struct {
		name   string
		msg    *Message
		verify func(t *testing.T, got *Message)
	}{
		{
			name: "start",
			msg:  &Message{Cmd: "start"},
			verify: func(t *testing.T, got *Message) {
				if got.Cmd != "start" {
					t.Errorf("Cmd = %q, want %q", got.Cmd, "start")
				}
			},
		},
		{
			name: "stop",
			msg:  &Message{Cmd: "stop"},
			verify: func(t *testing.T, got *Message) {
				if got.Cmd != "stop" {
					t.Errorf("Cmd = %q, want %q", got.Cmd, "stop")
				}
			},
		},
		{
			name: "reset",
			msg:  &Message{Cmd: "reset"},
			verify: func(t *testing.T, got *Message) {
				if got.Cmd != "reset" {
					t.Errorf("Cmd = %q, want %q", got.Cmd, "reset")
				}
			},
		},
		{
			name: "shutdown",
			msg:  &Message{Cmd: "shutdown"},
			verify: func(t *testing.T, got *Message) {
				if got.Cmd != "shutdown" {
					t.Errorf("Cmd = %q, want %q", got.Cmd, "shutdown")
				}
			},
		},
		{
			name: "set_config",
			msg: &Message{
				Cmd:    "set_config",
				Config: json.RawMessage(`{"language":"en","model":"large-v3"}`),
			},
			verify: func(t *testing.T, got *Message) {
				if got.Cmd != "set_config" {
					t.Errorf("Cmd = %q, want %q", got.Cmd, "set_config")
				}
				if got.Config == nil {
					t.Fatal("Config is nil")
				}
				var cfg map[string]string
				if err := json.Unmarshal(got.Config, &cfg); err != nil {
					t.Fatalf("unmarshal config: %v", err)
				}
				if cfg["language"] != "en" {
					t.Errorf("config.language = %q, want %q", cfg["language"], "en")
				}
				if cfg["model"] != "large-v3" {
					t.Errorf("config.model = %q, want %q", cfg["model"], "large-v3")
				}
			},
		},
	}

	for _, tc := range cmds {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteMessage(&buf, tc.msg); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}
			got, err := ReadMessage(&buf)
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}
			tc.verify(t, got)
		})
	}
}

func TestRoundtripStatus(t *testing.T) {
	var buf bytes.Buffer

	msg := &Message{
		Type:      "status",
		State:     "ready",
		Model:     "large-v3",
		LatencyMs: 42,
	}

	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	got, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got.Type != "status" {
		t.Errorf("Type = %q, want %q", got.Type, "status")
	}
	if got.State != "ready" {
		t.Errorf("State = %q, want %q", got.State, "ready")
	}
	if got.Model != "large-v3" {
		t.Errorf("Model = %q, want %q", got.Model, "large-v3")
	}
	if got.LatencyMs != 42 {
		t.Errorf("LatencyMs = %d, want 42", got.LatencyMs)
	}
}

func TestRoundtripError(t *testing.T) {
	var buf bytes.Buffer

	msg := &Message{
		Type:  "error",
		Error: "model failed to load: out of GPU memory",
	}

	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	got, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got.Type != "error" {
		t.Errorf("Type = %q, want %q", got.Type, "error")
	}
	if got.Error != "model failed to load: out of GPU memory" {
		t.Errorf("Error = %q, want %q", got.Error, "model failed to load: out of GPU memory")
	}
}

func TestMaxMessageSize(t *testing.T) {
	// Craft a payload that exceeds MaxMessageSize.
	oversized := make([]byte, MaxMessageSize+1)
	for i := range oversized {
		oversized[i] = 'x'
	}

	var buf bytes.Buffer
	// Write the length prefix manually.
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(oversized))); err != nil {
		t.Fatalf("write length: %v", err)
	}
	buf.Write(oversized)

	_, err := ReadMessage(&buf)
	if err == nil {
		t.Fatal("expected error for oversized message, got nil")
	}
	// Should not be io.EOF — it should be a descriptive error.
	if err == io.EOF {
		t.Fatal("expected descriptive error, got io.EOF")
	}
}

func TestMultipleMessages(t *testing.T) {
	var buf bytes.Buffer

	msgs := []*Message{
		{Cmd: "start"},
		{
			Type:    "transcript",
			IsFinal: false,
			Words:   []Word{{Text: "testing", Confidence: 0.9, GapBefore: 0}},
		},
		{
			Type:  "status",
			State: "inference",
			Model: "tiny",
		},
	}

	for i, msg := range msgs {
		if err := WriteMessage(&buf, msg); err != nil {
			t.Fatalf("WriteMessage[%d]: %v", i, err)
		}
	}

	for i, want := range msgs {
		got, err := ReadMessage(&buf)
		if err != nil {
			t.Fatalf("ReadMessage[%d]: %v", i, err)
		}

		// Compare by re-serializing to JSON.
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(got)
		if string(wantJSON) != string(gotJSON) {
			t.Errorf("message[%d]:\n  got:  %s\n  want: %s", i, gotJSON, wantJSON)
		}
	}

	// After reading all messages, should get EOF.
	_, err := ReadMessage(&buf)
	if err != io.EOF {
		t.Errorf("expected io.EOF after all messages, got %v", err)
	}
}

func TestReadMessageEOF(t *testing.T) {
	// Empty reader should return io.EOF directly.
	var buf bytes.Buffer
	_, err := ReadMessage(&buf)
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestReadMessagePartialHeader(t *testing.T) {
	// Only 2 bytes of the 4-byte header — should return an error.
	var buf bytes.Buffer
	buf.Write([]byte{0x00, 0x01})
	_, err := ReadMessage(&buf)
	if err == nil {
		t.Fatal("expected error for partial header, got nil")
	}
}
