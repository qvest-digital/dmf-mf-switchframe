package sidecar

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

func TestWriteReadMessage(t *testing.T) {
	var buf bytes.Buffer

	orig := &Message{
		Cmd:  "configure",
		Type: "status",
		Data: map[string]any{"key": "value"},
	}

	if err := WriteMessage(&buf, orig); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	got, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got.Cmd != orig.Cmd {
		t.Errorf("Cmd = %q, want %q", got.Cmd, orig.Cmd)
	}
	if got.Type != orig.Type {
		t.Errorf("Type = %q, want %q", got.Type, orig.Type)
	}
	// Data round-trips through JSON so it comes back as map[string]interface{}
	data, ok := got.Data.(map[string]any)
	if !ok {
		t.Fatalf("Data type = %T, want map[string]any", got.Data)
	}
	if data["key"] != "value" {
		t.Errorf("Data[key] = %v, want %q", data["key"], "value")
	}
}

func TestReadMessageMaxSize(t *testing.T) {
	// Craft a header claiming 1MB payload (exceeds 64KB limit).
	var buf bytes.Buffer
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 1<<20) // 1MB
	buf.Write(hdr[:])

	_, err := ReadMessage(&buf)
	if err == nil {
		t.Fatal("expected error for oversized message")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %q, want containing 'too large'", err)
	}
}

func TestReadMessageEOF(t *testing.T) {
	var buf bytes.Buffer // empty

	_, err := ReadMessage(&buf)
	if err != io.EOF {
		t.Errorf("err = %v, want io.EOF", err)
	}
}

func TestWriteMessageTooLarge(t *testing.T) {
	// Create a message with Data field exceeding 64KB when serialized.
	big := strings.Repeat("x", maxMessageSize+1)
	msg := &Message{Data: big}

	var buf bytes.Buffer
	err := WriteMessage(&buf, msg)
	if err == nil {
		t.Fatal("expected error for oversized message")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %q, want containing 'too large'", err)
	}
}

func TestMultipleMessages(t *testing.T) {
	var buf bytes.Buffer

	msgs := []*Message{
		{Cmd: "configure", Config: map[string]any{"lang": "en"}},
		{Type: "result", Data: "hello"},
		{Type: "error", Error: "something failed"},
	}

	for i, m := range msgs {
		if err := WriteMessage(&buf, m); err != nil {
			t.Fatalf("WriteMessage[%d]: %v", i, err)
		}
	}

	for i, orig := range msgs {
		got, err := ReadMessage(&buf)
		if err != nil {
			t.Fatalf("ReadMessage[%d]: %v", i, err)
		}
		if got.Cmd != orig.Cmd {
			t.Errorf("[%d] Cmd = %q, want %q", i, got.Cmd, orig.Cmd)
		}
		if got.Type != orig.Type {
			t.Errorf("[%d] Type = %q, want %q", i, got.Type, orig.Type)
		}
		if got.Error != orig.Error {
			t.Errorf("[%d] Error = %q, want %q", i, got.Error, orig.Error)
		}
	}
}
