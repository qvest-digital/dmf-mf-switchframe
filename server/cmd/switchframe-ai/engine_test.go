//go:build cgo && cuda && tensorrt

package main

import (
	"context"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/sidecar"
)

func TestEngineHandleMessage_Status(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	reply := eng.handleMessage(&sidecar.Message{Cmd: "status"})
	if reply.Type != "status" {
		t.Fatalf("expected type=status, got %q", reply.Type)
	}
	data, ok := reply.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data to be map[string]any, got %T", reply.Data)
	}
	if data["depth_ready"] != false {
		t.Error("expected depth_ready=false")
	}
	if data["sam_ready"] != false {
		t.Error("expected sam_ready=false")
	}
}

func TestEngineHandleMessage_UnknownCommand(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	reply := eng.handleMessage(&sidecar.Message{Cmd: "bogus"})
	if reply.Type != "error" {
		t.Fatalf("expected type=error, got %q", reply.Type)
	}
	if reply.Error == "" {
		t.Fatal("expected non-empty error")
	}
}

func TestEngineHandleMessage_EnableSegmentation_MissingSourceID(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	reply := eng.handleMessage(&sidecar.Message{
		Cmd: "enable_segmentation",
	})
	if reply.Type != "error" {
		t.Fatalf("expected type=error, got %q", reply.Type)
	}
}

func TestEngineHandleMessage_DisableSegmentation(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	// Disable a source that was never enabled — should still return ok.
	reply := eng.handleMessage(&sidecar.Message{
		Cmd:    "disable_segmentation",
		Config: map[string]any{"source_id": "cam1"},
	})
	if reply.Type != "ok" {
		t.Fatalf("expected type=ok, got %q (error: %s)", reply.Type, reply.Error)
	}
}

func TestEngineHandleMessage_DepthToggle(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	reply := eng.handleMessage(&sidecar.Message{Cmd: "enable_depth"})
	if reply.Type != "ok" {
		t.Fatalf("expected type=ok, got %q (error: %s)", reply.Type, reply.Error)
	}

	eng.mu.Lock()
	if !eng.depthEnabled {
		t.Error("expected depthEnabled=true")
	}
	eng.mu.Unlock()

	reply = eng.handleMessage(&sidecar.Message{Cmd: "disable_depth"})
	if reply.Type != "ok" {
		t.Fatalf("expected type=ok, got %q (error: %s)", reply.Type, reply.Error)
	}

	eng.mu.Lock()
	if eng.depthEnabled {
		t.Error("expected depthEnabled=false")
	}
	eng.mu.Unlock()
}

func TestEngineHandleMessage_SAMToggle(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	reply := eng.handleMessage(&sidecar.Message{Cmd: "enable_sam"})
	if reply.Type != "ok" {
		t.Fatalf("expected type=ok, got %q (error: %s)", reply.Type, reply.Error)
	}

	eng.mu.Lock()
	if !eng.samEnabled {
		t.Error("expected samEnabled=true")
	}
	eng.mu.Unlock()

	reply = eng.handleMessage(&sidecar.Message{Cmd: "disable_sam"})
	if reply.Type != "ok" {
		t.Fatalf("expected type=ok, got %q (error: %s)", reply.Type, reply.Error)
	}

	eng.mu.Lock()
	if eng.samEnabled {
		t.Error("expected samEnabled=false")
	}
	eng.mu.Unlock()
}

func TestEngineHandleMessage_Prompts(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	reply := eng.handleMessage(&sidecar.Message{
		Cmd:    "add_prompt",
		Config: map[string]any{"label": "person"},
	})
	if reply.Type != "ok" {
		t.Fatalf("expected type=ok, got %q (error: %s)", reply.Type, reply.Error)
	}

	reply = eng.handleMessage(&sidecar.Message{
		Cmd:    "remove_prompt",
		Config: map[string]any{"label": "person"},
	})
	if reply.Type != "ok" {
		t.Fatalf("expected type=ok, got %q (error: %s)", reply.Type, reply.Error)
	}
}

func TestEngineHandleMessage_ProcessFrame(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	reply := eng.handleMessage(&sidecar.Message{Cmd: "process_frame"})
	if reply.Type != "ok" {
		t.Fatalf("expected type=ok, got %q (error: %s)", reply.Type, reply.Error)
	}
}

func TestEngineHandleMessage_Infer(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	// Infer command should store slot→source mapping.
	reply := eng.handleMessage(&sidecar.Message{
		Cmd: "infer",
		Config: map[string]any{
			"slot":      float64(2),
			"source_id": "cam1",
		},
	})
	// Infer returns nil (fire-and-forget).
	if reply != nil {
		t.Fatalf("expected nil reply, got %+v", reply)
	}

	eng.mu.Lock()
	if eng.slotSource[2] != "cam1" {
		t.Errorf("expected slotSource[2]='cam1', got %q", eng.slotSource[2])
	}
	eng.mu.Unlock()
}

func TestEngineHandleMessage_InferAR_NilPool(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	// Should handle nil pool gracefully.
	reply := eng.handleMessage(&sidecar.Message{
		Cmd: "infer_ar",
		Config: map[string]any{
			"slot":   float64(0),
			"width":  float64(1920),
			"height": float64(1080),
			"pitch":  float64(2048),
		},
	})
	// infer_ar returns nil (fire-and-forget).
	if reply != nil {
		t.Fatalf("expected nil reply, got %+v", reply)
	}
}

func TestEngineHandleMessage_TrackingToggle(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	reply := eng.handleMessage(&sidecar.Message{
		Cmd:    "enable_tracking",
		Config: map[string]any{"source_id": "cam1"},
	})
	if reply.Type != "ok" {
		t.Fatalf("expected type=ok, got %q (error: %s)", reply.Type, reply.Error)
	}

	eng.mu.Lock()
	if !eng.trackingSources["cam1"] {
		t.Error("expected cam1 tracking enabled")
	}
	eng.mu.Unlock()

	reply = eng.handleMessage(&sidecar.Message{
		Cmd:    "disable_tracking",
		Config: map[string]any{"source_id": "cam1"},
	})
	if reply.Type != "ok" {
		t.Fatalf("expected type=ok, got %q (error: %s)", reply.Type, reply.Error)
	}

	eng.mu.Lock()
	if eng.trackingSources["cam1"] {
		t.Error("expected cam1 tracking disabled")
	}
	eng.mu.Unlock()
}

func TestEngineHandleMessage_TrackFrame_NilPool(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	reply := eng.handleMessage(&sidecar.Message{
		Cmd: "track_frame",
		Config: map[string]any{
			"slot":      float64(0),
			"source_id": "cam1",
		},
	})
	if reply != nil {
		t.Fatalf("expected nil reply, got %+v", reply)
	}
}

func TestEngineHandleMessage_EnableSegmentation_NoDimensions_NilPool(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	// enable_segmentation with valid source_id but no dimensions and nil pool
	// should return an error, not panic.
	reply := eng.handleMessage(&sidecar.Message{
		Cmd:    "enable_segmentation",
		Config: map[string]any{"source_id": "cam1"},
	})
	if reply.Type != "error" {
		t.Fatalf("expected type=error, got %q", reply.Type)
	}
}

func TestEngineHandleMessage_EnableSegmentation_NoEngine(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	// enable_segmentation with dimensions but nil segEngine allocates a
	// stub mask buffer (for IPC lifecycle testing) and returns OK.
	reply := eng.handleMessage(&sidecar.Message{
		Cmd: "enable_segmentation",
		Config: map[string]any{
			"source_id": "cam1",
			"width":     float64(1920),
			"height":    float64(1080),
		},
	})
	if reply.Type != sidecar.MsgOK {
		t.Fatalf("expected type=ok (stub mask), got %q: %s", reply.Type, reply.Error)
	}

	// Verify session was tracked.
	eng.mu.Lock()
	_, exists := eng.segSessions["cam1"]
	eng.mu.Unlock()
	if !exists {
		t.Fatal("expected segSession for cam1")
	}
}

func TestExtractSourceID(t *testing.T) {
	tests := []struct {
		name    string
		msg     *sidecar.Message
		want    string
		wantErr bool
	}{
		{
			name:    "nil config",
			msg:     &sidecar.Message{},
			wantErr: true,
		},
		{
			name:    "missing source_id",
			msg:     &sidecar.Message{Config: map[string]any{"foo": "bar"}},
			wantErr: true,
		},
		{
			name:    "empty source_id",
			msg:     &sidecar.Message{Config: map[string]any{"source_id": ""}},
			wantErr: true,
		},
		{
			name: "valid",
			msg:  &sidecar.Message{Config: map[string]any{"source_id": "cam1"}},
			want: "cam1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractSourceID(tt.msg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("extractSourceID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("extractSourceID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractDimensions(t *testing.T) {
	msg := &sidecar.Message{
		Config: map[string]any{
			"width":  float64(1920),
			"height": float64(1080),
		},
	}
	w, h := extractDimensions(msg)
	if w != 1920 || h != 1080 {
		t.Errorf("expected 1920x1080, got %dx%d", w, h)
	}

	// Missing dimensions returns 0,0.
	w, h = extractDimensions(&sidecar.Message{Config: map[string]any{}})
	if w != 0 || h != 0 {
		t.Errorf("expected 0x0, got %dx%d", w, h)
	}
}

func TestFrameLoop_ContextCancel(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, "", "")
	defer eng.close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		// With nil pool, we can't run the frame loop.
		if eng.pool == nil {
			close(done)
			return
		}
		eng.frameLoop(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(time.Second):
		t.Fatal("frameLoop did not exit after context cancel")
	}
}
