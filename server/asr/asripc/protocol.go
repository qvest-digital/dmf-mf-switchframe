// Package asripc defines the length-prefixed JSON wire protocol used for IPC
// between the switchframe server and the ASR sidecar process over a Unix socket.
//
// Wire format: [4 bytes big-endian payload length][JSON payload]
package asripc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// MaxMessageSize is the maximum allowed JSON payload size (64KB).
const MaxMessageSize = 65536

// Message is the envelope for all IPC communication.
// Control messages (switchframe -> sidecar) use Cmd.
// Transcript/status messages (sidecar -> switchframe) use Type.
type Message struct {
	// Direction: sidecar -> switchframe
	Type      string `json:"type,omitempty"`       // "transcript", "status", "error"
	Words     []Word `json:"words,omitempty"`       // for type=transcript
	IsFinal   bool   `json:"is_final,omitempty"`    // for type=transcript
	State     string `json:"state,omitempty"`       // for type=status: "ready", "inference"
	Model     string `json:"model,omitempty"`       // for type=status
	LatencyMs int    `json:"latency_ms,omitempty"`  // for type=status
	Error     string `json:"error,omitempty"`        // for type=error

	// Direction: switchframe -> sidecar
	Cmd    string          `json:"cmd,omitempty"`    // "start", "stop", "reset", "set_config", "shutdown"
	Config json.RawMessage `json:"config,omitempty"` // for cmd=set_config
}

// Word mirrors asr.TranscriptWord for IPC serialization.
type Word struct {
	Text       string  `json:"text"`
	Confidence float32 `json:"confidence"`
	GapBefore  int     `json:"gap_before"`
}

// WriteMessage serializes a Message as [4-byte big-endian length][JSON].
func WriteMessage(w io.Writer, msg *Message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("asripc: marshal message: %w", err)
	}

	// Write 4-byte big-endian length prefix.
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("asripc: write length: %w", err)
	}

	// Write JSON payload.
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("asripc: write payload: %w", err)
	}

	return nil
}

// ReadMessage reads a length-prefixed JSON message from r.
// Returns io.EOF if the reader is closed (not wrapped).
// Returns a descriptive error if the payload exceeds MaxMessageSize.
func ReadMessage(r io.Reader) (*Message, error) {
	// Read 4-byte length header.
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("asripc: read length: %w", err)
	}

	length := binary.BigEndian.Uint32(hdr[:])

	// Validate message size.
	if length > MaxMessageSize {
		return nil, fmt.Errorf("asripc: message size %d exceeds maximum %d bytes", length, MaxMessageSize)
	}

	// Read JSON payload.
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("asripc: read payload: %w", err)
	}

	var msg Message
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil, fmt.Errorf("asripc: unmarshal message: %w", err)
	}

	return &msg, nil
}
