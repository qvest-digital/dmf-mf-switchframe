package sidecar

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const maxMessageSize = 1 << 16 // 64KB

// WriteMessage writes a length-prefixed JSON message.
func WriteMessage(w io.Writer, msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if len(data) > maxMessageSize {
		return fmt.Errorf("message too large: %d > %d", len(data), maxMessageSize)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// ReadMessage reads a length-prefixed JSON message.
func ReadMessage(r io.Reader) (*Message, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(hdr[:])
	if size > maxMessageSize {
		return nil, fmt.Errorf("message too large: %d > %d", size, maxMessageSize)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &msg, nil
}
