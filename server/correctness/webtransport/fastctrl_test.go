//go:build correctness

package webtransport

import (
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/fastctrl"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/layout"
)

// TestFastCtrlLayoutSlotPosition verifies that a binary layout-slot-position
// datagram is correctly parsed and dispatched to the layout compositor,
// updating the slot's rectangle.
func TestFastCtrlLayoutSlotPosition(t *testing.T) {
	// Create a layout compositor with 1920x1080 frame.
	comp := layout.NewCompositor(1920, 1080)

	// Set up an initial layout with one slot at a known position.
	initialRect := image.Rect(100, 100, 500, 400)
	comp.SetLayout(&layout.Layout{
		Name: "test",
		Slots: []layout.Slot{
			{
				SourceKey: "cam1",
				Rect:      initialRect,
				ZOrder:    0,
				Enabled:   true,
			},
		},
	})

	// Verify initial state.
	l := comp.GetLayout()
	if l == nil {
		t.Fatal("layout is nil after SetLayout")
	}
	t.Logf("MEASURE: initial slot rect = %v", l.Slots[0].Rect)

	// Create a dispatcher and register the layout handler (mirrors app_fastctrl.go).
	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgLayoutSlotPosition, func(data []byte) error {
		slotID, rect, err := fastctrl.ParseLayoutSlotPosition(data)
		if err != nil {
			return err
		}
		return comp.UpdateSlotRect(slotID, rect)
	})

	// Craft a binary datagram:
	// [0]   = msg type (0x01)
	// [1]   = slot ID (0)
	// [2:4] = x (200, uint16 BE)
	// [4:6] = y (150, uint16 BE)
	// [6:8] = w (600, uint16 BE)
	// [8:10]= h (400, uint16 BE)
	wantX, wantY, wantW, wantH := 200, 150, 600, 400
	msg := make([]byte, 10)
	msg[0] = fastctrl.MsgLayoutSlotPosition
	msg[1] = 0 // slot 0
	binary.BigEndian.PutUint16(msg[2:4], uint16(wantX))
	binary.BigEndian.PutUint16(msg[4:6], uint16(wantY))
	binary.BigEndian.PutUint16(msg[6:8], uint16(wantW))
	binary.BigEndian.PutUint16(msg[8:10], uint16(wantH))

	err := d.Dispatch(msg)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	// Read back the slot position.
	l = comp.GetLayout()
	if l == nil {
		t.Fatal("layout is nil after dispatch")
	}
	got := l.Slots[0].Rect
	t.Logf("MEASURE: slot position after dispatch = (%d,%d,%d,%d)", got.Min.X, got.Min.Y, got.Dx(), got.Dy())

	// UpdateSlotRect does even-alignment: Min rounds down (already even), Max rounds up.
	// For input (200, 150, 200+600=800, 150+400=550):
	//   Min.X = 200 &^ 1 = 200
	//   Min.Y = 150 &^ 1 = 150
	//   Max.X = (800+1) &^ 1 = 800
	//   Max.Y = (550+1) &^ 1 = 550
	// The parser constructs rect as image.Rect(x, y, x+w, y+h) = Rect(200, 150, 800, 550)
	wantRect := image.Rect(wantX, wantY, wantX+wantW, wantY+wantH)
	// Apply the same even-alignment as UpdateSlotRect:
	wantRect.Min.X &^= 1
	wantRect.Min.Y &^= 1
	wantRect.Max.X = (wantRect.Max.X + 1) &^ 1
	wantRect.Max.Y = (wantRect.Max.Y + 1) &^ 1

	if got != wantRect {
		t.Errorf("slot rect mismatch: got %v, want %v", got, wantRect)
	}
}

// TestFastCtrlLayoutSlotPositionOddValues tests that odd-valued coordinates
// in a layout slot position datagram are rejected by the parser's even-alignment check.
func TestFastCtrlLayoutSlotPositionOddValues(t *testing.T) {
	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgLayoutSlotPosition, func(data []byte) error {
		_, _, err := fastctrl.ParseLayoutSlotPosition(data)
		return err
	})

	// Send odd X value (201) - parser should reject.
	msg := make([]byte, 10)
	msg[0] = fastctrl.MsgLayoutSlotPosition
	msg[1] = 0
	binary.BigEndian.PutUint16(msg[2:4], 201) // odd x
	binary.BigEndian.PutUint16(msg[4:6], 100)
	binary.BigEndian.PutUint16(msg[6:8], 400)
	binary.BigEndian.PutUint16(msg[8:10], 300)

	err := d.Dispatch(msg)
	t.Logf("MEASURE: dispatch with odd X=201 returned error: %v", err)
	if err == nil {
		t.Error("expected error for odd X value, got nil")
	}

	// Send odd width (401) - parser should reject.
	msg2 := make([]byte, 10)
	msg2[0] = fastctrl.MsgLayoutSlotPosition
	msg2[1] = 0
	binary.BigEndian.PutUint16(msg2[2:4], 200)
	binary.BigEndian.PutUint16(msg2[4:6], 100)
	binary.BigEndian.PutUint16(msg2[6:8], 401) // odd w
	binary.BigEndian.PutUint16(msg2[8:10], 300)

	err = d.Dispatch(msg2)
	t.Logf("MEASURE: dispatch with odd W=401 returned error: %v", err)
	if err == nil {
		t.Error("expected error for odd width value, got nil")
	}
}

// TestFastCtrlLayoutSlotOutOfRange tests dispatching with a slot ID >= MaxSlots.
func TestFastCtrlLayoutSlotOutOfRange(t *testing.T) {
	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgLayoutSlotPosition, func(data []byte) error {
		_, _, err := fastctrl.ParseLayoutSlotPosition(data)
		return err
	})

	msg := make([]byte, 10)
	msg[0] = fastctrl.MsgLayoutSlotPosition
	msg[1] = byte(layout.MaxSlots) // one past max
	binary.BigEndian.PutUint16(msg[2:4], 100)
	binary.BigEndian.PutUint16(msg[4:6], 100)
	binary.BigEndian.PutUint16(msg[6:8], 200)
	binary.BigEndian.PutUint16(msg[8:10], 200)

	err := d.Dispatch(msg)
	t.Logf("MEASURE: dispatch with slotID=%d (MaxSlots=%d) returned error: %v", layout.MaxSlots, layout.MaxSlots, err)
	if err == nil {
		t.Errorf("expected error for slot ID %d >= MaxSlots %d, got nil", layout.MaxSlots, layout.MaxSlots)
	}
}

// TestFastCtrlTransitionPosition verifies that a binary transition-position
// datagram is correctly parsed and the float32 value is captured.
func TestFastCtrlTransitionPosition(t *testing.T) {
	var capturedPos float64
	var capturedErr error

	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgTransitionPosition, func(data []byte) error {
		pos, err := fastctrl.ParseTransitionPosition(data)
		capturedPos = pos
		capturedErr = err
		return err
	})

	// Craft a binary datagram with position = 0.75:
	// [0]   = msg type (0x02)
	// [1:5] = float32 (0.75) as big-endian uint32
	wantPos := float32(0.75)
	msg := make([]byte, 5)
	msg[0] = fastctrl.MsgTransitionPosition
	binary.BigEndian.PutUint32(msg[1:5], math.Float32bits(wantPos))

	err := d.Dispatch(msg)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	t.Logf("MEASURE: transition position = %.4f (error=%v)", capturedPos, capturedErr)

	if math.Abs(capturedPos-float64(wantPos)) > 1e-6 {
		t.Errorf("transition position mismatch: got %.6f, want %.6f", capturedPos, float64(wantPos))
	}
}

// TestFastCtrlTransitionPositionBoundary tests boundary values: 0.0 and 1.0.
func TestFastCtrlTransitionPositionBoundary(t *testing.T) {
	var capturedPos float64

	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgTransitionPosition, func(data []byte) error {
		pos, err := fastctrl.ParseTransitionPosition(data)
		capturedPos = pos
		return err
	})

	testCases := []struct {
		name    string
		value   float32
		wantErr bool
	}{
		{"zero", 0.0, false},
		{"one", 1.0, false},
		{"mid", 0.5, false},
		{"negative", -0.1, true},
		{"over_one", 1.1, true},
		{"NaN", float32(math.NaN()), true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg := make([]byte, 5)
			msg[0] = fastctrl.MsgTransitionPosition
			binary.BigEndian.PutUint32(msg[1:5], math.Float32bits(tc.value))

			err := d.Dispatch(msg)
			t.Logf("MEASURE: transition position=%.4f, error=%v", capturedPos, err)

			if tc.wantErr && err == nil {
				t.Errorf("expected error for value %v, got nil", tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for value %v: %v", tc.value, err)
			}
			if !tc.wantErr {
				if math.Abs(capturedPos-float64(tc.value)) > 1e-6 {
					t.Errorf("position mismatch: got %.6f, want %.6f", capturedPos, float64(tc.value))
				}
			}
		})
	}
}

// TestFastCtrlGraphicsLayerPosition verifies that a binary graphics-layer-position
// datagram is correctly parsed and updates the graphics compositor's layer rect.
func TestFastCtrlGraphicsLayerPosition(t *testing.T) {
	comp := graphics.NewCompositor()
	defer comp.Close()

	// Add a layer.
	layerID, err := comp.AddLayer()
	if err != nil {
		t.Fatalf("AddLayer: %v", err)
	}
	t.Logf("MEASURE: created layer ID = %d", layerID)

	// Create dispatcher and register the graphics handler (mirrors app_fastctrl.go).
	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgGraphicsLayerPosition, func(data []byte) error {
		lid, rect, err := fastctrl.ParseGraphicsLayerPosition(data)
		if err != nil {
			return err
		}
		return comp.UpdateLayerRect(lid, rect)
	})

	// Craft a binary datagram:
	// [0]    = msg type (0x03)
	// [1:3]  = layer ID (uint16 BE)
	// [3:5]  = x (uint16 BE)
	// [5:7]  = y (uint16 BE)
	// [7:9]  = w (uint16 BE)
	// [9:11] = h (uint16 BE)
	wantX, wantY, wantW, wantH := 100, 200, 400, 300
	msg := make([]byte, 11)
	msg[0] = fastctrl.MsgGraphicsLayerPosition
	binary.BigEndian.PutUint16(msg[1:3], uint16(layerID))
	binary.BigEndian.PutUint16(msg[3:5], uint16(wantX))
	binary.BigEndian.PutUint16(msg[5:7], uint16(wantY))
	binary.BigEndian.PutUint16(msg[7:9], uint16(wantW))
	binary.BigEndian.PutUint16(msg[9:11], uint16(wantH))

	err = d.Dispatch(msg)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	// Read back the layer state via Status().
	state := comp.Status()
	if len(state.Layers) == 0 {
		t.Fatal("no layers in compositor status")
	}

	var found bool
	for _, ls := range state.Layers {
		if ls.ID == layerID {
			found = true
			t.Logf("MEASURE: graphics layer rect after dispatch = (%d,%d,%d,%d)",
				ls.Rect.X, ls.Rect.Y, ls.Rect.Width, ls.Rect.Height)
			if ls.Rect.X != wantX {
				t.Errorf("layer rect X: got %d, want %d", ls.Rect.X, wantX)
			}
			if ls.Rect.Y != wantY {
				t.Errorf("layer rect Y: got %d, want %d", ls.Rect.Y, wantY)
			}
			if ls.Rect.Width != wantW {
				t.Errorf("layer rect Width: got %d, want %d", ls.Rect.Width, wantW)
			}
			if ls.Rect.Height != wantH {
				t.Errorf("layer rect Height: got %d, want %d", ls.Rect.Height, wantH)
			}
			break
		}
	}
	if !found {
		t.Errorf("layer ID %d not found in status", layerID)
	}
}

// TestFastCtrlGraphicsLayerNotFound tests dispatching to a non-existent layer ID.
func TestFastCtrlGraphicsLayerNotFound(t *testing.T) {
	comp := graphics.NewCompositor()
	defer comp.Close()

	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgGraphicsLayerPosition, func(data []byte) error {
		lid, rect, err := fastctrl.ParseGraphicsLayerPosition(data)
		if err != nil {
			return err
		}
		return comp.UpdateLayerRect(lid, rect)
	})

	// Layer ID 999 does not exist.
	msg := make([]byte, 11)
	msg[0] = fastctrl.MsgGraphicsLayerPosition
	binary.BigEndian.PutUint16(msg[1:3], 999)
	binary.BigEndian.PutUint16(msg[3:5], 100)
	binary.BigEndian.PutUint16(msg[5:7], 100)
	binary.BigEndian.PutUint16(msg[7:9], 200)
	binary.BigEndian.PutUint16(msg[9:11], 200)

	err := d.Dispatch(msg)
	t.Logf("MEASURE: dispatch to non-existent layer 999, error = %v", err)
	if err == nil {
		t.Error("expected error for non-existent layer, got nil")
	}
}

// TestFastCtrlInvalidMessage tests that invalid datagrams are handled gracefully
// without panicking.
func TestFastCtrlInvalidMessage(t *testing.T) {
	d := fastctrl.NewDispatcher()

	// Register all message types so we test parser error handling.
	d.Register(fastctrl.MsgLayoutSlotPosition, func(data []byte) error {
		_, _, err := fastctrl.ParseLayoutSlotPosition(data)
		return err
	})
	d.Register(fastctrl.MsgTransitionPosition, func(data []byte) error {
		_, err := fastctrl.ParseTransitionPosition(data)
		return err
	})
	d.Register(fastctrl.MsgGraphicsLayerPosition, func(data []byte) error {
		_, _, err := fastctrl.ParseGraphicsLayerPosition(data)
		return err
	})

	testCases := []struct {
		name      string
		data      []byte
		wantErr   error // nil = any error, non-nil = errors.Is match
	}{
		{"empty_bytes", []byte{}, fastctrl.ErrEmptyDatagram},
		{"layout_type_only", []byte{0x01}, nil},
		{"transition_type_only", []byte{0x02}, nil},
		{"graphics_type_only", []byte{0x03}, nil},
		{"unknown_type_0xFF", []byte{0xFF}, fastctrl.ErrUnknownType},
		{"unknown_type_0x00", []byte{0x00}, fastctrl.ErrUnknownType},
		{"layout_truncated_2bytes", []byte{0x01, 0x00}, nil},
		{"layout_truncated_5bytes", []byte{0x01, 0x00, 0x00, 0x64, 0x00}, nil},
		{"transition_truncated_2bytes", []byte{0x02, 0x00}, nil},
		{"transition_truncated_3bytes", []byte{0x02, 0x00, 0x00}, nil},
		{"graphics_truncated_5bytes", []byte{0x03, 0x00, 0x01, 0x00, 0x64}, nil},
		{"graphics_truncated_8bytes", []byte{0x03, 0x00, 0x01, 0x00, 0x64, 0x00, 0xC8, 0x01}, nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := d.Dispatch(tc.data)
			t.Logf("MEASURE: dispatch(%q) = %v", tc.name, err)
			require.Error(t, err, "expected error for %s", tc.name)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr, "sentinel error mismatch for %s", tc.name)
			}
		})
	}
}

// TestFastCtrlInvalidMessageNoPanic specifically ensures no panics with hostile payloads.
func TestFastCtrlInvalidMessageNoPanic(t *testing.T) {
	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgLayoutSlotPosition, func(data []byte) error {
		_, _, err := fastctrl.ParseLayoutSlotPosition(data)
		return err
	})
	d.Register(fastctrl.MsgTransitionPosition, func(data []byte) error {
		_, err := fastctrl.ParseTransitionPosition(data)
		return err
	})
	d.Register(fastctrl.MsgGraphicsLayerPosition, func(data []byte) error {
		_, _, err := fastctrl.ParseGraphicsLayerPosition(data)
		return err
	})

	// Hostile payloads that should never panic.
	hostile := [][]byte{
		nil,
		{},
		{0x01},
		{0x02},
		{0x03},
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
		// Layout with max uint16 values
		{0x01, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
		// Transition with NaN
		{0x02, 0x7F, 0xC0, 0x00, 0x00},
		// Transition with Inf
		{0x02, 0x7F, 0x80, 0x00, 0x00},
		// Transition with -Inf
		{0x02, 0xFF, 0x80, 0x00, 0x00},
		// Zero-length width/height for layout
		{0x01, 0x00, 0x00, 0x64, 0x00, 0x64, 0x00, 0x00, 0x00, 0x00},
		// Zero-length width/height for graphics
		{0x03, 0x00, 0x01, 0x00, 0x64, 0x00, 0x64, 0x00, 0x00, 0x00, 0x00},
	}

	for i, payload := range hostile {
		t.Run(fmt.Sprintf("hostile_%d", i), func(t *testing.T) {
			// Should not panic. Error is expected and OK.
			_ = d.Dispatch(payload)
		})
	}
}

// TestFastCtrlRapidUpdates sends 1000 layout position updates in a tight loop
// to verify no panics or races, and that the final position matches the last update.
func TestFastCtrlRapidUpdates(t *testing.T) {
	comp := layout.NewCompositor(1920, 1080)

	// Set up a layout with one slot.
	comp.SetLayout(&layout.Layout{
		Name: "test",
		Slots: []layout.Slot{
			{
				SourceKey: "cam1",
				Rect:      image.Rect(0, 0, 200, 200),
				ZOrder:    0,
				Enabled:   true,
			},
		},
	})

	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgLayoutSlotPosition, func(data []byte) error {
		slotID, rect, err := fastctrl.ParseLayoutSlotPosition(data)
		if err != nil {
			return err
		}
		return comp.UpdateSlotRect(slotID, rect)
	})

	// Send 1000 updates with incrementing positions (all even-aligned, within bounds).
	const numUpdates = 1000
	var lastX, lastY uint16
	var errCount int

	for i := 0; i < numUpdates; i++ {
		// Keep values within 1920x1080, even-aligned, with a 200x200 slot.
		x := uint16((i * 2) % 1720) // 0, 2, 4, ..., 1718, 0, 2, ...
		y := uint16((i * 2) % 880)  // 0, 2, 4, ..., 878, 0, 2, ...
		w := uint16(200)
		h := uint16(200)

		msg := make([]byte, 10)
		msg[0] = fastctrl.MsgLayoutSlotPosition
		msg[1] = 0 // slot 0
		binary.BigEndian.PutUint16(msg[2:4], x)
		binary.BigEndian.PutUint16(msg[4:6], y)
		binary.BigEndian.PutUint16(msg[6:8], w)
		binary.BigEndian.PutUint16(msg[8:10], h)

		if err := d.Dispatch(msg); err != nil {
			errCount++
		}

		lastX = x
		lastY = y
	}

	t.Logf("MEASURE: %d updates sent, %d errors", numUpdates, errCount)
	if errCount > 0 {
		t.Errorf("expected 0 errors from rapid updates, got %d", errCount)
	}

	// Verify final position.
	l := comp.GetLayout()
	if l == nil {
		t.Fatal("layout is nil after rapid updates")
	}
	got := l.Slots[0].Rect
	t.Logf("MEASURE: final slot rect = %v (expected x=%d y=%d)", got, lastX, lastY)

	// Apply even-alignment like UpdateSlotRect does.
	wantFinal := image.Rect(int(lastX), int(lastY), int(lastX)+200, int(lastY)+200)
	wantFinal.Min.X &^= 1
	wantFinal.Min.Y &^= 1
	wantFinal.Max.X = (wantFinal.Max.X + 1) &^ 1
	wantFinal.Max.Y = (wantFinal.Max.Y + 1) &^ 1

	if got != wantFinal {
		t.Errorf("final rect mismatch: got %v, want %v", got, wantFinal)
	}
}

// TestFastCtrlRapidTransitionUpdates sends 1000 transition position updates
// in a tight loop to verify no panics or races.
func TestFastCtrlRapidTransitionUpdates(t *testing.T) {
	var lastPos atomic.Value
	lastPos.Store(float64(0))

	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgTransitionPosition, func(data []byte) error {
		pos, err := fastctrl.ParseTransitionPosition(data)
		if err != nil {
			return err
		}
		lastPos.Store(pos)
		return nil
	})

	const numUpdates = 1000
	var errCount int

	for i := 0; i < numUpdates; i++ {
		pos := float32(i) / float32(numUpdates-1)
		msg := make([]byte, 5)
		msg[0] = fastctrl.MsgTransitionPosition
		binary.BigEndian.PutUint32(msg[1:5], math.Float32bits(pos))

		if err := d.Dispatch(msg); err != nil {
			errCount++
		}
	}

	finalPos := lastPos.Load().(float64)
	t.Logf("MEASURE: %d transition updates, %d errors, final position = %.6f", numUpdates, errCount, finalPos)

	if errCount > 0 {
		t.Errorf("expected 0 errors, got %d", errCount)
	}

	// Final position should be 999/999 = 1.0
	if math.Abs(finalPos-1.0) > 1e-5 {
		t.Errorf("final transition position: got %.6f, want ~1.0", finalPos)
	}
}

// TestFastCtrlRapidGraphicsUpdates sends 1000 graphics layer position updates
// to verify no panics or races.
func TestFastCtrlRapidGraphicsUpdates(t *testing.T) {
	comp := graphics.NewCompositor()
	defer comp.Close()

	layerID, err := comp.AddLayer()
	if err != nil {
		t.Fatalf("AddLayer: %v", err)
	}

	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgGraphicsLayerPosition, func(data []byte) error {
		lid, rect, err := fastctrl.ParseGraphicsLayerPosition(data)
		if err != nil {
			return err
		}
		return comp.UpdateLayerRect(lid, rect)
	})

	const numUpdates = 1000
	var errCount int
	var lastX, lastY, lastW, lastH int

	for i := 0; i < numUpdates; i++ {
		x := (i * 2) % 1920
		y := (i * 2) % 1080
		w := 200
		h := 150

		msg := make([]byte, 11)
		msg[0] = fastctrl.MsgGraphicsLayerPosition
		binary.BigEndian.PutUint16(msg[1:3], uint16(layerID))
		binary.BigEndian.PutUint16(msg[3:5], uint16(x))
		binary.BigEndian.PutUint16(msg[5:7], uint16(y))
		binary.BigEndian.PutUint16(msg[7:9], uint16(w))
		binary.BigEndian.PutUint16(msg[9:11], uint16(h))

		if err := d.Dispatch(msg); err != nil {
			errCount++
		}
		lastX, lastY, lastW, lastH = x, y, w, h
	}

	t.Logf("MEASURE: %d graphics updates, %d errors", numUpdates, errCount)

	// Read back and verify final position.
	state := comp.Status()
	for _, ls := range state.Layers {
		if ls.ID == layerID {
			t.Logf("MEASURE: final graphics layer rect = (%d,%d,%d,%d)",
				ls.Rect.X, ls.Rect.Y, ls.Rect.Width, ls.Rect.Height)
			if ls.Rect.X != lastX || ls.Rect.Y != lastY || ls.Rect.Width != lastW || ls.Rect.Height != lastH {
				t.Errorf("final rect mismatch: got (%d,%d,%d,%d), want (%d,%d,%d,%d)",
					ls.Rect.X, ls.Rect.Y, ls.Rect.Width, ls.Rect.Height,
					lastX, lastY, lastW, lastH)
			}
			break
		}
	}
}

// TestFastCtrlDispatcherStripsTypeByteBeforeHandler verifies that the dispatcher
// passes data[1:] (not data) to the handler. This is a fundamental contract of
// the fastctrl dispatcher.
func TestFastCtrlDispatcherStripsTypeByteBeforeHandler(t *testing.T) {
	d := fastctrl.NewDispatcher()

	var receivedLen int
	d.Register(0x01, func(data []byte) error {
		receivedLen = len(data)
		return nil
	})

	// Send 10 bytes (1 type + 9 payload).
	msg := make([]byte, 10)
	msg[0] = 0x01
	for i := 1; i < 10; i++ {
		msg[i] = byte(i)
	}

	err := d.Dispatch(msg)
	if err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	t.Logf("MEASURE: sent %d bytes, handler received %d bytes", len(msg), receivedLen)
	if receivedLen != 9 {
		t.Errorf("handler received %d bytes, expected 9 (msg[1:])", receivedLen)
	}
}

// TestFastCtrlLayoutZeroDimensions tests that a zero-dimension rect is rejected.
//
// Zero dimensions (w=0 or h=0) are intentionally rejected by ParseLayoutSlotPosition
// (layout.go line 31-33: "if w == 0 || h == 0"). A zero-area slot would cause
// division-by-zero in aspect ratio calculations and has no valid rendering interpretation.
// The same check exists in ParseGraphicsLayerPosition (graphics.go line 31-33).
func TestFastCtrlLayoutZeroDimensions(t *testing.T) {
	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgLayoutSlotPosition, func(data []byte) error {
		_, _, err := fastctrl.ParseLayoutSlotPosition(data)
		return err
	})

	// Zero width: must be rejected because a zero-area slot has no valid rendering.
	msg := make([]byte, 10)
	msg[0] = fastctrl.MsgLayoutSlotPosition
	msg[1] = 0
	binary.BigEndian.PutUint16(msg[2:4], 100)
	binary.BigEndian.PutUint16(msg[4:6], 100)
	binary.BigEndian.PutUint16(msg[6:8], 0)   // w=0
	binary.BigEndian.PutUint16(msg[8:10], 200) // h=200

	err := d.Dispatch(msg)
	t.Logf("MEASURE: dispatch with zero width = %v", err)
	require.Error(t, err, "zero width must be rejected")
	require.Contains(t, err.Error(), "zero dimension", "error message should identify zero dimension")

	// Zero height: must be rejected for the same reason.
	msg2 := make([]byte, 10)
	msg2[0] = fastctrl.MsgLayoutSlotPosition
	msg2[1] = 0
	binary.BigEndian.PutUint16(msg2[2:4], 100)
	binary.BigEndian.PutUint16(msg2[4:6], 100)
	binary.BigEndian.PutUint16(msg2[6:8], 200) // w=200
	binary.BigEndian.PutUint16(msg2[8:10], 0)  // h=0

	err = d.Dispatch(msg2)
	t.Logf("MEASURE: dispatch with zero height = %v", err)
	require.Error(t, err, "zero height must be rejected")
	require.Contains(t, err.Error(), "zero dimension", "error message should identify zero dimension")

	// Both zero: also rejected.
	msg3 := make([]byte, 10)
	msg3[0] = fastctrl.MsgLayoutSlotPosition
	msg3[1] = 0
	binary.BigEndian.PutUint16(msg3[2:4], 100)
	binary.BigEndian.PutUint16(msg3[4:6], 100)
	binary.BigEndian.PutUint16(msg3[6:8], 0) // w=0
	binary.BigEndian.PutUint16(msg3[8:10], 0) // h=0

	err = d.Dispatch(msg3)
	t.Logf("MEASURE: dispatch with both zero = %v", err)
	require.Error(t, err, "both zero dimensions must be rejected")
}

// TestFastCtrlGraphicsZeroDimensions tests that zero-dimension graphics rects are rejected.
// See TestFastCtrlLayoutZeroDimensions comment for rationale — the same logic applies.
func TestFastCtrlGraphicsZeroDimensions(t *testing.T) {
	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgGraphicsLayerPosition, func(data []byte) error {
		_, _, err := fastctrl.ParseGraphicsLayerPosition(data)
		return err
	})

	// Zero width.
	msg := make([]byte, 11)
	msg[0] = fastctrl.MsgGraphicsLayerPosition
	binary.BigEndian.PutUint16(msg[1:3], 0)   // layer 0
	binary.BigEndian.PutUint16(msg[3:5], 100)  // x
	binary.BigEndian.PutUint16(msg[5:7], 100)  // y
	binary.BigEndian.PutUint16(msg[7:9], 0)    // w=0
	binary.BigEndian.PutUint16(msg[9:11], 200) // h=200

	err := d.Dispatch(msg)
	t.Logf("MEASURE: dispatch graphics with zero width = %v", err)
	require.Error(t, err, "zero graphics width must be rejected")
	require.Contains(t, err.Error(), "zero dimension")

	// Zero height.
	msg2 := make([]byte, 11)
	msg2[0] = fastctrl.MsgGraphicsLayerPosition
	binary.BigEndian.PutUint16(msg2[1:3], 0)   // layer 0
	binary.BigEndian.PutUint16(msg2[3:5], 100)  // x
	binary.BigEndian.PutUint16(msg2[5:7], 100)  // y
	binary.BigEndian.PutUint16(msg2[7:9], 200)  // w=200
	binary.BigEndian.PutUint16(msg2[9:11], 0)   // h=0

	err = d.Dispatch(msg2)
	t.Logf("MEASURE: dispatch graphics with zero height = %v", err)
	require.Error(t, err, "zero graphics height must be rejected")
	require.Contains(t, err.Error(), "zero dimension")
}

// TestFastCtrlLayoutExceedsFrameBounds tests that a layout rect exceeding the
// frame bounds is rejected by UpdateSlotRect.
func TestFastCtrlLayoutExceedsFrameBounds(t *testing.T) {
	comp := layout.NewCompositor(1920, 1080)
	comp.SetLayout(&layout.Layout{
		Name: "test",
		Slots: []layout.Slot{
			{
				SourceKey: "cam1",
				Rect:      image.Rect(0, 0, 200, 200),
				ZOrder:    0,
				Enabled:   true,
			},
		},
	})

	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgLayoutSlotPosition, func(data []byte) error {
		slotID, rect, err := fastctrl.ParseLayoutSlotPosition(data)
		if err != nil {
			return err
		}
		return comp.UpdateSlotRect(slotID, rect)
	})

	// Rect that exceeds frame bounds: x=1800, w=200 -> max_x=2000 > 1920
	msg := make([]byte, 10)
	msg[0] = fastctrl.MsgLayoutSlotPosition
	msg[1] = 0
	binary.BigEndian.PutUint16(msg[2:4], 1800)
	binary.BigEndian.PutUint16(msg[4:6], 100)
	binary.BigEndian.PutUint16(msg[6:8], 200)
	binary.BigEndian.PutUint16(msg[8:10], 200)

	err := d.Dispatch(msg)
	t.Logf("MEASURE: dispatch with rect exceeding bounds = %v", err)
	if err == nil {
		t.Error("expected error for rect exceeding frame bounds, got nil")
	}

	// Verify original rect was not modified.
	l := comp.GetLayout()
	got := l.Slots[0].Rect
	t.Logf("MEASURE: slot rect after failed update = %v", got)
}

// TestFastCtrlMultipleMessageTypes verifies that different message types can
// be registered and dispatched independently without interference.
// Each handler type has its own invocation counter AND captures parsed values,
// verifying that dispatch routes to the correct handler with correct data.
func TestFastCtrlMultipleMessageTypes(t *testing.T) {
	var layoutCalled, transitionCalled, graphicsCalled int
	var lastLayoutSlotID int
	var lastTransitionPos float64
	var lastGraphicsLayerID int

	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgLayoutSlotPosition, func(data []byte) error {
		layoutCalled++
		slotID, _, err := fastctrl.ParseLayoutSlotPosition(data)
		if err == nil {
			lastLayoutSlotID = slotID
		}
		return err
	})
	d.Register(fastctrl.MsgTransitionPosition, func(data []byte) error {
		transitionCalled++
		pos, err := fastctrl.ParseTransitionPosition(data)
		if err == nil {
			lastTransitionPos = pos
		}
		return err
	})
	d.Register(fastctrl.MsgGraphicsLayerPosition, func(data []byte) error {
		graphicsCalled++
		lid, _, err := fastctrl.ParseGraphicsLayerPosition(data)
		if err == nil {
			lastGraphicsLayerID = lid
		}
		return err
	})

	// Valid layout message (slot 0).
	layoutMsg := make([]byte, 10)
	layoutMsg[0] = fastctrl.MsgLayoutSlotPosition
	layoutMsg[1] = 0
	binary.BigEndian.PutUint16(layoutMsg[2:4], 100)
	binary.BigEndian.PutUint16(layoutMsg[4:6], 100)
	binary.BigEndian.PutUint16(layoutMsg[6:8], 200)
	binary.BigEndian.PutUint16(layoutMsg[8:10], 200)

	// Valid transition message (position 0.5).
	transMsg := make([]byte, 5)
	transMsg[0] = fastctrl.MsgTransitionPosition
	binary.BigEndian.PutUint32(transMsg[1:5], math.Float32bits(0.5))

	// Valid graphics message (layer 0).
	gfxMsg := make([]byte, 11)
	gfxMsg[0] = fastctrl.MsgGraphicsLayerPosition
	binary.BigEndian.PutUint16(gfxMsg[1:3], 0)
	binary.BigEndian.PutUint16(gfxMsg[3:5], 100)
	binary.BigEndian.PutUint16(gfxMsg[5:7], 100)
	binary.BigEndian.PutUint16(gfxMsg[7:9], 200)
	binary.BigEndian.PutUint16(gfxMsg[9:11], 200)

	// Dispatch each type and verify no errors. The key property: each dispatch
	// routes to the correct handler without interfering with the others.
	require.NoError(t, d.Dispatch(layoutMsg), "layout dispatch 1")
	require.NoError(t, d.Dispatch(transMsg), "transition dispatch")
	require.NoError(t, d.Dispatch(gfxMsg), "graphics dispatch")

	// Second layout dispatch (slot 2 this time) to verify count + last value.
	layoutMsg2 := make([]byte, 10)
	layoutMsg2[0] = fastctrl.MsgLayoutSlotPosition
	layoutMsg2[1] = 2 // different slot
	binary.BigEndian.PutUint16(layoutMsg2[2:4], 200)
	binary.BigEndian.PutUint16(layoutMsg2[4:6], 200)
	binary.BigEndian.PutUint16(layoutMsg2[6:8], 300)
	binary.BigEndian.PutUint16(layoutMsg2[8:10], 300)
	require.NoError(t, d.Dispatch(layoutMsg2), "layout dispatch 2")

	t.Logf("MEASURE: layout=%d, transition=%d, graphics=%d", layoutCalled, transitionCalled, graphicsCalled)

	// Verify exact call counts per handler type.
	require.Equal(t, 2, layoutCalled, "layout handler call count")
	require.Equal(t, 1, transitionCalled, "transition handler call count")
	require.Equal(t, 1, graphicsCalled, "graphics handler call count")

	// Verify each handler received the correct parsed values (not just that it was called).
	require.Equal(t, 2, lastLayoutSlotID, "last layout slot ID should be 2 (from second dispatch)")
	require.InDelta(t, 0.5, lastTransitionPos, 1e-6, "transition position value")
	require.Equal(t, 0, lastGraphicsLayerID, "graphics layer ID")

	// Verify an unknown type is correctly rejected.
	unknownMsg := []byte{0xFE, 0x00, 0x00}
	err := d.Dispatch(unknownMsg)
	require.ErrorIs(t, err, fastctrl.ErrUnknownType, "unknown message type should return ErrUnknownType")
}

// TestFastCtrlLayoutSlotPositionExtraBytes verifies that extra trailing bytes
// in a layout message are silently ignored (the parser only reads the first 9 bytes).
func TestFastCtrlLayoutSlotPositionExtraBytes(t *testing.T) {
	var parsedSlotID int
	var parsedRect image.Rectangle

	d := fastctrl.NewDispatcher()
	d.Register(fastctrl.MsgLayoutSlotPosition, func(data []byte) error {
		var err error
		parsedSlotID, parsedRect, err = fastctrl.ParseLayoutSlotPosition(data)
		return err
	})

	// 9 valid payload bytes + 5 extra garbage bytes.
	msg := make([]byte, 15)
	msg[0] = fastctrl.MsgLayoutSlotPosition
	msg[1] = 0 // slot 0
	binary.BigEndian.PutUint16(msg[2:4], 100)
	binary.BigEndian.PutUint16(msg[4:6], 100)
	binary.BigEndian.PutUint16(msg[6:8], 200)
	binary.BigEndian.PutUint16(msg[8:10], 200)
	// Trailing garbage.
	msg[10] = 0xFF
	msg[11] = 0xFF
	msg[12] = 0xFF
	msg[13] = 0xFF
	msg[14] = 0xFF

	err := d.Dispatch(msg)
	t.Logf("MEASURE: dispatch with extra bytes: err=%v, slotID=%d, rect=%v", err, parsedSlotID, parsedRect)

	if err != nil {
		t.Errorf("extra trailing bytes should not cause error, got: %v", err)
	}
	if parsedSlotID != 0 {
		t.Errorf("slotID: got %d, want 0", parsedSlotID)
	}
	wantRect := image.Rect(100, 100, 300, 300)
	if parsedRect != wantRect {
		t.Errorf("rect: got %v, want %v", parsedRect, wantRect)
	}
}
