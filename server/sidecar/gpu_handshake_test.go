package sidecar

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestGPUPoolHandshakeMarshalRoundTrip(t *testing.T) {
	// Create a handshake with known handle values.
	hs := GPUPoolHandshake{
		Config: GPUPoolConfig{
			SlotCount:  4,
			Width:      1920,
			Height:     1080,
			Pitch:      2048,
			ResultSize: 1920 * 1080,
		},
		ShmName:       "/sf-test-hs",
		FrameHandles:  make([]CUDAIPCHandle, 4),
		ResultHandles: make([]CUDAIPCHandle, 4),
		ReadyEvents:   make([]CUDAIPCEventHandle, 4),
		DoneEvents:    make([]CUDAIPCEventHandle, 4),
	}

	// Fill handles with recognizable patterns.
	for i := range hs.FrameHandles {
		for j := range hs.FrameHandles[i] {
			hs.FrameHandles[i][j] = byte(i*64 + j)
		}
	}
	for i := range hs.ResultHandles {
		for j := range hs.ResultHandles[i] {
			hs.ResultHandles[i][j] = byte(128 + i*64 + j)
		}
	}
	for i := range hs.ReadyEvents {
		hs.ReadyEvents[i][0] = byte(i + 1)
	}
	for i := range hs.DoneEvents {
		hs.DoneEvents[i][0] = byte(i + 10)
	}

	// Marshal to JSON.
	data, err := json.Marshal(hs)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Unmarshal back.
	var got GPUPoolHandshake
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Verify config.
	if got.Config.SlotCount != 4 {
		t.Errorf("Config.SlotCount = %d, want 4", got.Config.SlotCount)
	}
	if got.Config.Width != 1920 {
		t.Errorf("Config.Width = %d, want 1920", got.Config.Width)
	}
	if got.ShmName != "/sf-test-hs" {
		t.Errorf("ShmName = %q, want /sf-test-hs", got.ShmName)
	}

	// Verify handles round-tripped correctly.
	if len(got.FrameHandles) != 4 {
		t.Fatalf("FrameHandles len = %d, want 4", len(got.FrameHandles))
	}
	for i := range hs.FrameHandles {
		if got.FrameHandles[i] != hs.FrameHandles[i] {
			t.Errorf("FrameHandles[%d] mismatch", i)
		}
	}
	for i := range hs.ResultHandles {
		if got.ResultHandles[i] != hs.ResultHandles[i] {
			t.Errorf("ResultHandles[%d] mismatch", i)
		}
	}
	for i := range hs.ReadyEvents {
		if got.ReadyEvents[i] != hs.ReadyEvents[i] {
			t.Errorf("ReadyEvents[%d] mismatch", i)
		}
	}
	for i := range hs.DoneEvents {
		if got.DoneEvents[i] != hs.DoneEvents[i] {
			t.Errorf("DoneEvents[%d] mismatch", i)
		}
	}
}

func TestGPUPoolHandshakeNoResults(t *testing.T) {
	hs := GPUPoolHandshake{
		Config:       GPUPoolConfig{SlotCount: 2, Width: 1280, Height: 720},
		ShmName:      "/sf-test-nores",
		FrameHandles: make([]CUDAIPCHandle, 2),
		ReadyEvents:  make([]CUDAIPCEventHandle, 2),
		DoneEvents:   make([]CUDAIPCEventHandle, 2),
		// No ResultHandles.
	}

	data, err := json.Marshal(hs)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got GPUPoolHandshake
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(got.ResultHandles) != 0 {
		t.Errorf("ResultHandles len = %d, want 0", len(got.ResultHandles))
	}
}

func TestWriteReadGPUPoolHandshake(t *testing.T) {
	hs := &GPUPoolHandshake{
		Config: GPUPoolConfig{
			SlotCount: 2,
			Width:     1920,
			Height:    1080,
			Pitch:     2048,
		},
		ShmName:      "/sf-test-proto",
		FrameHandles: make([]CUDAIPCHandle, 2),
		ReadyEvents:  make([]CUDAIPCEventHandle, 2),
		DoneEvents:   make([]CUDAIPCEventHandle, 2),
	}

	// Fill frame handle 0 with a pattern.
	for j := range hs.FrameHandles[0] {
		hs.FrameHandles[0][j] = byte(j)
	}

	// Write via protocol.
	var buf bytes.Buffer
	if err := WriteGPUPoolHandshake(&buf, hs); err != nil {
		t.Fatalf("WriteGPUPoolHandshake: %v", err)
	}

	// Read back.
	got, err := ReadGPUPoolHandshake(&buf)
	if err != nil {
		t.Fatalf("ReadGPUPoolHandshake: %v", err)
	}

	if got.Config.SlotCount != 2 {
		t.Errorf("SlotCount = %d, want 2", got.Config.SlotCount)
	}
	if got.ShmName != "/sf-test-proto" {
		t.Errorf("ShmName = %q", got.ShmName)
	}
	if got.FrameHandles[0] != hs.FrameHandles[0] {
		t.Error("FrameHandles[0] mismatch after protocol round-trip")
	}
}

func TestReadGPUPoolHandshakeWrongCmd(t *testing.T) {
	var buf bytes.Buffer
	// Write a non-handshake message.
	_ = WriteMessage(&buf, &Message{Cmd: "shutdown"})

	_, err := ReadGPUPoolHandshake(&buf)
	if err == nil {
		t.Fatal("expected error for wrong cmd")
	}
}

func TestMaskHandleMessageRoundTrip(t *testing.T) {
	var h CUDAIPCHandle
	for i := range h {
		h[i] = byte(i)
	}

	orig := MaskHandleMessage{
		SourceID: "cam1",
		Handle:   h,
		Width:    1920,
		Height:   1080,
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got MaskHandleMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.SourceID != "cam1" {
		t.Errorf("SourceID = %q, want cam1", got.SourceID)
	}
	if got.Width != 1920 || got.Height != 1080 {
		t.Errorf("dims = %dx%d, want 1920x1080", got.Width, got.Height)
	}
	if got.Handle != orig.Handle {
		t.Error("handle mismatch after round-trip")
	}
}

func TestParseMaskHandleMessage(t *testing.T) {
	msg := &Message{
		Type: "mask_ready",
		Data: map[string]any{
			"source_id": "cam2",
			"handle":    "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==",
			"width":     float64(1280),
			"height":    float64(720),
		},
	}

	mh, err := ParseMaskHandleMessage(msg)
	if err != nil {
		t.Fatalf("ParseMaskHandleMessage: %v", err)
	}
	if mh == nil {
		t.Fatal("expected non-nil result")
	}
	if mh.SourceID != "cam2" {
		t.Errorf("SourceID = %q, want cam2", mh.SourceID)
	}
	if mh.Width != 1280 || mh.Height != 720 {
		t.Errorf("dims = %dx%d, want 1280x720", mh.Width, mh.Height)
	}
}

func TestParseMaskHandleMessageWrongType(t *testing.T) {
	msg := &Message{Type: "status", Data: map[string]any{}}
	mh, err := ParseMaskHandleMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mh != nil {
		t.Error("expected nil for non-mask message")
	}
}
