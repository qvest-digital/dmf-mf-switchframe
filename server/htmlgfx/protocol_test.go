package htmlgfx

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

func TestControlCommandSerialize(t *testing.T) {
	tests := []struct {
		name string
		cmd  ControlCommand
		want map[string]any
	}{
		{
			name: "create",
			cmd: ControlCommand{
				Cmd:    "create",
				ID:     intPtr(0),
				Width:  1920,
				Height: 1080,
				FPS:    30,
			},
			want: map[string]any{
				"cmd":    "create",
				"id":     float64(0),
				"width":  float64(1920),
				"height": float64(1080),
				"fps":    float64(30),
			},
		},
		{
			name: "load",
			cmd: ControlCommand{
				Cmd: "load",
				ID:  intPtr(0),
				URL: "file:///test.html",
			},
			want: map[string]any{
				"cmd": "load",
				"id":  float64(0),
				"url": "file:///test.html",
			},
		},
		{
			name: "play",
			cmd: ControlCommand{
				Cmd: "play",
				ID:  intPtr(0),
			},
			want: map[string]any{
				"cmd": "play",
				"id":  float64(0),
			},
		},
		{
			name: "stop",
			cmd: ControlCommand{
				Cmd: "stop",
				ID:  intPtr(0),
			},
			want: map[string]any{
				"cmd": "stop",
				"id":  float64(0),
			},
		},
		{
			name: "next",
			cmd: ControlCommand{
				Cmd: "next",
				ID:  intPtr(0),
			},
			want: map[string]any{
				"cmd": "next",
				"id":  float64(0),
			},
		},
		{
			name: "update with data",
			cmd: ControlCommand{
				Cmd:  "update",
				ID:   intPtr(0),
				Data: `{"name":"John"}`,
			},
			want: map[string]any{
				"cmd":  "update",
				"id":   float64(0),
				"data": `{"name":"John"}`,
			},
		},
		{
			name: "custom action",
			cmd: ControlCommand{
				Cmd:     "custom",
				ID:      intPtr(0),
				Action:  "highlight",
				Payload: "{}",
			},
			want: map[string]any{
				"cmd":     "custom",
				"id":      float64(0),
				"action":  "highlight",
				"payload": "{}",
			},
		},
		{
			name: "destroy",
			cmd: ControlCommand{
				Cmd: "destroy",
				ID:  intPtr(0),
			},
			want: map[string]any{
				"cmd": "destroy",
				"id":  float64(0),
			},
		},
		{
			name: "shutdown has no id",
			cmd: ControlCommand{
				Cmd: "shutdown",
			},
			want: map[string]any{
				"cmd": "shutdown",
			},
		},
		{
			name: "create omits zero url and data",
			cmd: ControlCommand{
				Cmd:    "create",
				ID:     intPtr(2),
				Width:  1280,
				Height: 720,
				FPS:    60,
			},
			want: map[string]any{
				"cmd":    "create",
				"id":     float64(2),
				"width":  float64(1280),
				"height": float64(720),
				"fps":    float64(60),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.cmd)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}

			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}

			// Check that all expected keys are present with correct values.
			for k, wantVal := range tt.want {
				gotVal, ok := got[k]
				if !ok {
					t.Errorf("missing key %q", k)
					continue
				}
				if gotVal != wantVal {
					t.Errorf("key %q: got %v (%T), want %v (%T)", k, gotVal, gotVal, wantVal, wantVal)
				}
			}

			// Check that no extra keys are present.
			for k := range got {
				if _, ok := tt.want[k]; !ok {
					t.Errorf("unexpected key %q with value %v", k, got[k])
				}
			}
		})
	}
}

func TestControlCommandWriteNewlineDelimited(t *testing.T) {
	var buf bytes.Buffer

	cmd1 := ControlCommand{Cmd: "create", ID: intPtr(0), Width: 1920, Height: 1080, FPS: 30}
	cmd2 := ControlCommand{Cmd: "load", ID: intPtr(0), URL: "file:///test.html"}

	if err := WriteControlCommand(&buf, cmd1); err != nil {
		t.Fatalf("write cmd1: %v", err)
	}
	if err := WriteControlCommand(&buf, cmd2); err != nil {
		t.Fatalf("write cmd2: %v", err)
	}

	// Should be two newline-terminated JSON lines.
	lines := bytes.Split(buf.Bytes(), []byte("\n"))
	// Last element is empty string after trailing newline.
	if len(lines) != 3 {
		t.Fatalf("expected 3 parts (2 lines + trailing empty), got %d", len(lines))
	}

	var parsed ControlCommand
	if err := json.Unmarshal(lines[0], &parsed); err != nil {
		t.Fatalf("parse line 0: %v", err)
	}
	if parsed.Cmd != "create" {
		t.Errorf("line 0 cmd: got %q, want %q", parsed.Cmd, "create")
	}

	if err := json.Unmarshal(lines[1], &parsed); err != nil {
		t.Fatalf("parse line 1: %v", err)
	}
	if parsed.Cmd != "load" {
		t.Errorf("line 1 cmd: got %q, want %q", parsed.Cmd, "load")
	}
}

func TestControlResponseParse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ControlResponse
	}{
		{
			name:  "ok status",
			input: `{"id":0,"status":"ok"}`,
			want: ControlResponse{
				ID:     0,
				Status: "ok",
			},
		},
		{
			name:  "error status with message",
			input: `{"id":1,"status":"error","error":"not found"}`,
			want: ControlResponse{
				ID:     1,
				Status: "error",
				Error:  "not found",
			},
		},
		{
			name:  "event with format",
			input: `{"id":0,"event":"format_detected","format":"ograf"}`,
			want: ControlResponse{
				ID:     0,
				Event:  "format_detected",
				Format: "ograf",
			},
		},
		{
			name:  "event with data payload",
			input: `{"id":2,"event":"schema_parsed","data":"{\"fields\":[\"name\"]}"}`,
			want: ControlResponse{
				ID:    2,
				Event: "schema_parsed",
				Data:  `{"fields":["name"]}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bytes.NewBufferString(tt.input + "\n")
			got, err := readControlResponse(r)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			if got.ID != tt.want.ID {
				t.Errorf("ID: got %d, want %d", got.ID, tt.want.ID)
			}
			if got.Status != tt.want.Status {
				t.Errorf("Status: got %q, want %q", got.Status, tt.want.Status)
			}
			if got.Error != tt.want.Error {
				t.Errorf("Error: got %q, want %q", got.Error, tt.want.Error)
			}
			if got.Event != tt.want.Event {
				t.Errorf("Event: got %q, want %q", got.Event, tt.want.Event)
			}
			if got.Format != tt.want.Format {
				t.Errorf("Format: got %q, want %q", got.Format, tt.want.Format)
			}
			if got.Data != tt.want.Data {
				t.Errorf("Data: got %q, want %q", got.Data, tt.want.Data)
			}
		})
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer

	width := uint16(4)
	height := uint16(2)
	pixels := make([]byte, int(width)*int(height)*4)
	// Fill with a recognizable pattern.
	for i := range pixels {
		pixels[i] = byte(i % 256)
	}

	frame := Frame{
		BrowserID: 3,
		Width:     width,
		Height:    height,
		Pixels:    pixels,
	}

	if err := WriteFrame(&buf, frame); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}

	if got.BrowserID != frame.BrowserID {
		t.Errorf("BrowserID: got %d, want %d", got.BrowserID, frame.BrowserID)
	}
	if got.Width != frame.Width {
		t.Errorf("Width: got %d, want %d", got.Width, frame.Width)
	}
	if got.Height != frame.Height {
		t.Errorf("Height: got %d, want %d", got.Height, frame.Height)
	}
	if !bytes.Equal(got.Pixels, frame.Pixels) {
		t.Errorf("Pixels mismatch: got %d bytes, want %d bytes", len(got.Pixels), len(frame.Pixels))
	}
}

func TestFrameSizeMismatch(t *testing.T) {
	var buf bytes.Buffer

	// Write a frame header that claims 4x2 but provide wrong pixel count.
	frame := Frame{
		BrowserID: 0,
		Width:     4,
		Height:    2,
		Pixels:    make([]byte, 10), // should be 4*2*4=32
	}

	err := WriteFrame(&buf, frame)
	if err == nil {
		t.Fatal("expected error for pixel size mismatch, got nil")
	}
}

func TestFrameReadSizeMismatch(t *testing.T) {
	// Manually craft a binary frame with inconsistent dimensions and pixel data.
	var buf bytes.Buffer

	width := uint16(4)
	height := uint16(2)
	expectedPixels := int(width) * int(height) * 4 // 32

	// Write header: total_length covers header (5 bytes) + fewer pixels than expected.
	badPixelLen := 10
	totalLen := uint32(5 + badPixelLen)

	writeBigEndianUint32(&buf, totalLen)
	buf.WriteByte(0) // browser_id
	writeBigEndianUint16(&buf, width)
	writeBigEndianUint16(&buf, height)
	buf.Write(make([]byte, badPixelLen))

	_, err := ReadFrame(&buf)
	if err == nil {
		t.Fatalf("expected error for pixel data mismatch (got %d bytes, expected %d)", badPixelLen, expectedPixels)
	}
}

func TestMultipleFrames(t *testing.T) {
	var buf bytes.Buffer

	frames := make([]Frame, 3)
	for i := range frames {
		w := uint16(2)
		h := uint16(2)
		pixels := make([]byte, int(w)*int(h)*4)
		for j := range pixels {
			pixels[j] = byte((i*16 + j) % 256)
		}
		frames[i] = Frame{
			BrowserID: uint8(i),
			Width:     w,
			Height:    h,
			Pixels:    pixels,
		}
	}

	// Write all frames.
	for _, f := range frames {
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatalf("write frame %d: %v", f.BrowserID, err)
		}
	}

	// Read them back.
	for i, want := range frames {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		if got.BrowserID != want.BrowserID {
			t.Errorf("frame %d BrowserID: got %d, want %d", i, got.BrowserID, want.BrowserID)
		}
		if got.Width != want.Width {
			t.Errorf("frame %d Width: got %d, want %d", i, got.Width, want.Width)
		}
		if got.Height != want.Height {
			t.Errorf("frame %d Height: got %d, want %d", i, got.Height, want.Height)
		}
		if !bytes.Equal(got.Pixels, want.Pixels) {
			t.Errorf("frame %d Pixels mismatch", i)
		}
	}

	// No more frames.
	_, err := ReadFrame(&buf)
	if err != io.EOF {
		t.Errorf("expected EOF after all frames, got %v", err)
	}
}

func TestFrameHeaderConstants(t *testing.T) {
	// Verify the frame header size constant.
	if FrameHeaderSize != 5 {
		t.Errorf("FrameHeaderSize: got %d, want 5", FrameHeaderSize)
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.PoolSize != 4 {
		t.Errorf("PoolSize: got %d, want 4", cfg.PoolSize)
	}
	if cfg.Width != 1920 {
		t.Errorf("Width: got %d, want 1920", cfg.Width)
	}
	if cfg.Height != 1080 {
		t.Errorf("Height: got %d, want 1080", cfg.Height)
	}
	if cfg.FPS != 30 {
		t.Errorf("FPS: got %d, want 30", cfg.FPS)
	}
}

func TestBrowserStateInitial(t *testing.T) {
	bs := NewBrowserState(0)
	if bs.ID != 0 {
		t.Errorf("ID: got %d, want 0", bs.ID)
	}
	if bs.LayerID != -1 {
		t.Errorf("LayerID: got %d, want -1", bs.LayerID)
	}
	if bs.Status != "empty" {
		t.Errorf("Status: got %q, want %q", bs.Status, "empty")
	}
}

// intPtr returns a pointer to the given int.
func intPtr(i int) *int {
	return &i
}

// writeBigEndianUint32 writes a uint32 in big-endian to the buffer.
// Used only in test helpers for crafting malformed frames.
func writeBigEndianUint32(buf *bytes.Buffer, v uint32) {
	buf.WriteByte(byte(v >> 24))
	buf.WriteByte(byte(v >> 16))
	buf.WriteByte(byte(v >> 8))
	buf.WriteByte(byte(v))
}

// writeBigEndianUint16 writes a uint16 in big-endian to the buffer.
// Used only in test helpers for crafting malformed frames.
func writeBigEndianUint16(buf *bytes.Buffer, v uint16) {
	buf.WriteByte(byte(v >> 8))
	buf.WriteByte(byte(v))
}
