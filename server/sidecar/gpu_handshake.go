package sidecar

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// GPUPoolHandshake contains all the information a worker needs to map
// a shared VRAM pool: IPC handles for frame and result memory, IPC event
// handles for GPU stream sync, and the pool configuration.
//
// Sent from host to worker over the control socket immediately after spawn.
// The worker uses these to call OpenCUDAGPUPoolWorker (or Metal equivalent).
type GPUPoolHandshake struct {
	Config        GPUPoolConfig        `json:"config"`
	ShmName       string               `json:"shm_name"`
	FrameHandles  []CUDAIPCHandle      `json:"frame_handles"`
	ResultHandles []CUDAIPCHandle      `json:"result_handles,omitempty"`
	ReadyEvents   []CUDAIPCEventHandle `json:"ready_events"`
	DoneEvents    []CUDAIPCEventHandle `json:"done_events"`
}

// gpuPoolHandshakeWire is the JSON-friendly wire format. Handles are
// base64-encoded since they're opaque 64-byte blobs.
type gpuPoolHandshakeWire struct {
	Config        GPUPoolConfig `json:"config"`
	ShmName       string        `json:"shm_name"`
	FrameHandles  []string      `json:"frame_handles"`
	ResultHandles []string      `json:"result_handles,omitempty"`
	ReadyEvents   []string      `json:"ready_events"`
	DoneEvents    []string      `json:"done_events"`
}

// MarshalJSON encodes handles as base64 strings.
func (h GPUPoolHandshake) MarshalJSON() ([]byte, error) {
	w := gpuPoolHandshakeWire{
		Config:  h.Config,
		ShmName: h.ShmName,
	}
	w.FrameHandles = encodeHandles(h.FrameHandles)
	w.ResultHandles = encodeHandles(h.ResultHandles)
	w.ReadyEvents = encodeEventHandles(h.ReadyEvents)
	w.DoneEvents = encodeEventHandles(h.DoneEvents)
	return json.Marshal(w)
}

// UnmarshalJSON decodes base64-encoded handles.
func (h *GPUPoolHandshake) UnmarshalJSON(data []byte) error {
	var w gpuPoolHandshakeWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	h.Config = w.Config
	h.ShmName = w.ShmName

	var err error
	if h.FrameHandles, err = decodeHandles(w.FrameHandles); err != nil {
		return fmt.Errorf("frame_handles: %w", err)
	}
	if h.ResultHandles, err = decodeHandles(w.ResultHandles); err != nil {
		return fmt.Errorf("result_handles: %w", err)
	}
	if h.ReadyEvents, err = decodeEventHandles(w.ReadyEvents); err != nil {
		return fmt.Errorf("ready_events: %w", err)
	}
	if h.DoneEvents, err = decodeEventHandles(w.DoneEvents); err != nil {
		return fmt.Errorf("done_events: %w", err)
	}
	return nil
}

// WriteGPUPoolHandshake sends the handshake to the worker over the control socket.
// Uses the standard length-prefixed protocol with cmd="gpu_pool_init".
func WriteGPUPoolHandshake(w io.Writer, hs *GPUPoolHandshake) error {
	return WriteMessage(w, &Message{
		Cmd:  "gpu_pool_init",
		Data: hs,
	})
}

// ReadGPUPoolHandshake reads and decodes a gpu_pool_init message from the host.
// Returns an error if the message is not a gpu_pool_init command.
func ReadGPUPoolHandshake(r io.Reader) (*GPUPoolHandshake, error) {
	msg, err := ReadMessage(r)
	if err != nil {
		return nil, err
	}
	if msg.Cmd != "gpu_pool_init" {
		return nil, fmt.Errorf("expected gpu_pool_init, got cmd=%q", msg.Cmd)
	}

	// msg.Data is a map[string]any from JSON unmarshal. Re-marshal and
	// unmarshal into GPUPoolHandshake to decode the base64 handles.
	raw, err := json.Marshal(msg.Data)
	if err != nil {
		return nil, fmt.Errorf("re-marshal handshake data: %w", err)
	}

	var hs GPUPoolHandshake
	if err := json.Unmarshal(raw, &hs); err != nil {
		return nil, fmt.Errorf("unmarshal handshake: %w", err)
	}
	return &hs, nil
}

func encodeHandles(handles []CUDAIPCHandle) []string {
	out := make([]string, len(handles))
	for i, h := range handles {
		out[i] = base64.StdEncoding.EncodeToString(h[:])
	}
	return out
}

func decodeHandles(encoded []string) ([]CUDAIPCHandle, error) {
	out := make([]CUDAIPCHandle, len(encoded))
	for i, s := range encoded {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("[%d]: %w", i, err)
		}
		if len(b) != 64 {
			return nil, fmt.Errorf("[%d]: decoded %d bytes, want 64", i, len(b))
		}
		copy(out[i][:], b)
	}
	return out, nil
}

func encodeEventHandles(handles []CUDAIPCEventHandle) []string {
	out := make([]string, len(handles))
	for i, h := range handles {
		out[i] = base64.StdEncoding.EncodeToString(h[:])
	}
	return out
}

func decodeEventHandles(encoded []string) ([]CUDAIPCEventHandle, error) {
	out := make([]CUDAIPCEventHandle, len(encoded))
	for i, s := range encoded {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("[%d]: %w", i, err)
		}
		if len(b) != 64 {
			return nil, fmt.Errorf("[%d]: decoded %d bytes, want 64", i, len(b))
		}
		copy(out[i][:], b)
	}
	return out, nil
}

// MaskHandleMessage is sent from the sidecar to the host when a per-source
// mask buffer is allocated. The host imports the IPC handle to get a device
// pointer to the same physical VRAM the sidecar writes masks to. Zero-copy.
type MaskHandleMessage struct {
	SourceID string        `json:"source_id"`
	Handle   CUDAIPCHandle `json:"handle"`
	Width    int           `json:"width"`
	Height   int           `json:"height"`
}

// maskHandleWire is the JSON-friendly wire format with base64 handle.
type maskHandleWire struct {
	SourceID string `json:"source_id"`
	Handle   string `json:"handle"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// MarshalJSON encodes the handle as base64.
func (m MaskHandleMessage) MarshalJSON() ([]byte, error) {
	return json.Marshal(maskHandleWire{
		SourceID: m.SourceID,
		Handle:   base64.StdEncoding.EncodeToString(m.Handle[:]),
		Width:    m.Width,
		Height:   m.Height,
	})
}

// UnmarshalJSON decodes the base64 handle.
func (m *MaskHandleMessage) UnmarshalJSON(data []byte) error {
	var w maskHandleWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	m.SourceID = w.SourceID
	m.Width = w.Width
	m.Height = w.Height
	b, err := base64.StdEncoding.DecodeString(w.Handle)
	if err != nil {
		return fmt.Errorf("handle: %w", err)
	}
	if len(b) != 64 {
		return fmt.Errorf("handle: decoded %d bytes, want 64", len(b))
	}
	copy(m.Handle[:], b)
	return nil
}

// ParseMaskHandleMessage extracts a MaskHandleMessage from a control message
// with type="mask_ready". Returns nil if the message is not a mask handle.
func ParseMaskHandleMessage(msg *Message) (*MaskHandleMessage, error) {
	if msg.Type != "mask_ready" {
		return nil, nil
	}
	raw, err := json.Marshal(msg.Data)
	if err != nil {
		return nil, fmt.Errorf("re-marshal mask handle: %w", err)
	}
	var mh MaskHandleMessage
	if err := json.Unmarshal(raw, &mh); err != nil {
		return nil, fmt.Errorf("unmarshal mask handle: %w", err)
	}
	return &mh, nil
}
