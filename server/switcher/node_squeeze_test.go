package switcher

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/internal"
)

func zeroMs() *int { v := 0; return &v }

// squeezeSwitcher returns a switcher with cam1 and cam2 and a 16x16
// squeeze-back into the bottom-right quarter over cam2.
func squeezeSwitcher(t *testing.T) (*Switcher, *dve.Squeeze) {
	t.Helper()
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.RegisterSource("cam1", newTestRelay())
	sw.RegisterSource("cam2", newTestRelay())
	sq := dve.NewSqueeze(16, 16)
	require.NoError(t, sq.Set(dve.SqueezeConfig{
		Rect:       dve.Rect{X: 8, Y: 8, W: 8, H: 8},
		Background: "cam2",
	}))
	sw.SetSqueeze(sq)
	return sw, sq
}

func frame8(y byte) *ProcessingFrame {
	data := make([]byte, ColorYUV420_8bit.FrameSize(16, 16))
	for i := range data {
		data[i] = 128
	}
	for i := range 16 * 16 {
		data[i] = y
	}
	return &ProcessingFrame{Data: data, Width: 16, Height: 16, Format: ColorYUV420_8bit}
}

func frame10y(y uint16) *ProcessingFrame {
	data := make([]byte, ColorYUV422_10bit.FrameSize(16, 16))
	for i := 0; i < len(data); i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 512)
	}
	for i := range 16 * 16 {
		binary.LittleEndian.PutUint16(data[i*2:], y)
	}
	return &ProcessingFrame{Data: data, Width: 16, Height: 16, Format: ColorYUV422_10bit}
}

func TestSqueezeNode_InactiveWhenNil(t *testing.T) {
	n := &squeezeNode{}
	require.False(t, n.Active())
	require.Equal(t, "squeezeback", n.Name())
}

func TestSqueezeNode_AlwaysActiveWhenAttached(t *testing.T) {
	// Toggling on and off must not rebuild the pipeline.
	n := &squeezeNode{squeeze: dve.NewSqueeze(16, 16)}
	require.True(t, n.Active(), "active while off: Process has its own fast path")
}

func TestSqueezeNode_OffLeavesFrameUntouched(t *testing.T) {
	_, sq := squeezeSwitcher(t)
	n := &squeezeNode{squeeze: sq}
	pf := frame8(235)
	want := append([]byte(nil), pf.Data...)
	out := n.Process(nil, pf)
	require.Same(t, pf, out)
	require.Equal(t, want, out.Data)
}

// The background is fed from source ingest, so any source can surround
// the program, not only the one on program.
func TestSqueezeNode_BackgroundFedFromIngest(t *testing.T) {
	sw, sq := squeezeSwitcher(t)
	sw.IngestRawVideo("cam2", frame8(50))
	require.NoError(t, sq.On(dve.SqueezeMove{DurationMs: zeroMs()}))

	out := (&squeezeNode{squeeze: sq}).Process(nil, frame8(235))
	require.Equal(t, byte(50), out.Data[0], "background around the program")
	require.Equal(t, byte(235), out.Data[12*16+12], "program inside the rect")
}

func TestState_SqueezeBackgroundTally(t *testing.T) {
	sw, sq := squeezeSwitcher(t)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.Equal(t, string(TallyIdle), sw.State().TallyState["cam2"], "configured but off")

	require.NoError(t, sq.On(dve.SqueezeMove{DurationMs: zeroMs()}))
	require.Equal(t, string(TallyProgram), sw.State().TallyState["cam2"], "background on air")

	states := make(chan internal.ControlRoomState, 8)
	sw.OnStateChange(func(s internal.ControlRoomState) { states <- s })
	ms := 20
	require.NoError(t, sq.Off(dve.SqueezeMove{DurationMs: &ms}))
	require.Equal(t, string(TallyProgram), sw.State().TallyState["cam2"], "on air while moving out")

	// The pipeline ticks the animation; the arrival is broadcast.
	n := &squeezeNode{squeeze: sq}
	require.Eventually(t, func() bool {
		n.Process(nil, frame8(235))
		return !sq.Active()
	}, time.Second, 5*time.Millisecond)
	select {
	case s := <-states:
		require.Equal(t, string(TallyIdle), s.TallyState["cam2"])
	case <-time.After(time.Second):
		t.Fatal("no state broadcast when the background left the air")
	}
}

// The arrival is reported from the pipeline goroutine, which must not
// wait on the state lock to build the broadcast.
func TestSqueezeNode_ArrivalDoesNotBlockOnStateLock(t *testing.T) {
	sw, sq := squeezeSwitcher(t)
	states := make(chan internal.ControlRoomState, 8)
	sw.OnStateChange(func(s internal.ControlRoomState) { states <- s })
	ms := 1
	require.NoError(t, sq.On(dve.SqueezeMove{DurationMs: &ms}))
	time.Sleep(5 * time.Millisecond)

	sw.configMu.Lock()
	done := make(chan struct{})
	go func() {
		(&squeezeNode{squeeze: sq}).Process(nil, frame8(235))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		sw.configMu.Unlock()
		<-done
		t.Fatal("frame blocked on the state lock")
	}
	sw.configMu.Unlock()

	// The On arrival is broadcast too: animating is part of state.
	select {
	case <-states:
	case <-time.After(time.Second):
		t.Fatal("no state broadcast when the On arrived")
	}
}
