package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/switcher"
)

func TestInitCommandQueue(t *testing.T) {
	relay := distribution.NewRelay()
	sw := switcher.New(relay)
	defer sw.Close()

	app := &App{
		sw: sw,
	}

	err := app.initCommandQueue()
	require.NoError(t, err)

	assert.NotNil(t, app.cmdQueue, "cmdQueue should be non-nil after init")
	assert.NotNil(t, app.cmdDispatcher, "cmdDispatcher should be non-nil after init")
}

func TestInitCommandQueue_DispatcherActions(t *testing.T) {
	relay := distribution.NewRelay()
	sw := switcher.New(relay)
	defer sw.Close()

	app := &App{
		sw: sw,
	}

	err := app.initCommandQueue()
	require.NoError(t, err)

	// Dispatch "cut" — should fail with source-not-found, NOT unknown action.
	cutPayload, _ := json.Marshal(map[string]string{"source": "cam1"})
	cutErr := app.cmdDispatcher.Dispatch(cmdqueue.Command{
		Action:  "cut",
		Payload: cutPayload,
	})
	require.Error(t, cutErr)
	assert.NotErrorIs(t, cutErr, cmdqueue.ErrUnknownAction,
		"cut action should be registered")
	assert.ErrorIs(t, cutErr, switcher.ErrSourceNotFound,
		"cut on non-existent source should return ErrSourceNotFound")

	// Dispatch "preview" — should fail with source-not-found, NOT unknown action.
	previewPayload, _ := json.Marshal(map[string]string{"source": "cam2"})
	previewErr := app.cmdDispatcher.Dispatch(cmdqueue.Command{
		Action:  "preview",
		Payload: previewPayload,
	})
	require.Error(t, previewErr)
	assert.NotErrorIs(t, previewErr, cmdqueue.ErrUnknownAction,
		"preview action should be registered")
	assert.ErrorIs(t, previewErr, switcher.ErrSourceNotFound,
		"preview on non-existent source should return ErrSourceNotFound")

	// Dispatch an unregistered action — should return ErrUnknownAction.
	unknownErr := app.cmdDispatcher.Dispatch(cmdqueue.Command{
		Action: "bogus",
	})
	assert.True(t, errors.Is(unknownErr, cmdqueue.ErrUnknownAction),
		"unregistered action should return ErrUnknownAction")
}

func TestInitCommandQueue_WithWAL(t *testing.T) {
	relay := distribution.NewRelay()
	sw := switcher.New(relay)
	defer sw.Close()

	stateDir := t.TempDir()
	app := &App{
		sw:  sw,
		cfg: AppConfig{StateDir: stateDir},
	}

	err := app.initCommandQueue()
	require.NoError(t, err)

	assert.NotNil(t, app.cmdQueue, "cmdQueue should be non-nil")
	assert.NotNil(t, app.cmdQueueWAL, "cmdQueueWAL should be non-nil when StateDir is set")
}

func TestInitCommandQueue_DVEPresetRecallAnimates(t *testing.T) {
	relay := distribution.NewRelay()
	sw := switcher.New(relay)
	defer sw.Close()

	format := sw.PipelineFormat()
	dc := dve.NewCompositor(format.Width, format.Height)
	from := dve.ResolveBuiltinPreset("pip-top-left", format.Width, format.Height)
	dc.SetLayout(&dve.Layout{Name: from.Name, Slots: from.Slots})

	app := &App{sw: sw, dveCompositor: dc}
	require.NoError(t, app.initCommandQueue())

	payload, _ := json.Marshal(map[string]any{"_path_id": "pip-bottom-right", "durationMs": dve.MaxMorphDurationMs, "easing": "ease-in-out"})
	require.NoError(t, app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: "dve_preset_recall", Payload: payload}))

	require.Greater(t, dc.GetLayout().Slots[0].Transform.OutputRect.X, float64(format.Width)/2, "state reports the target")
	snaps := dc.SnapshotSlots()
	require.Len(t, snaps, 1)
	require.Less(t, snaps[0].RectX, format.Width/2, "timed recall with durationMs must animate, not cut")

	bad, _ := json.Marshal(map[string]any{"_path_id": "pip-bottom-right", "durationMs": 500, "easing": "bounce"})
	require.Error(t, app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: "dve_preset_recall", Payload: bad}))

	long, _ := json.Marshal(map[string]any{"_path_id": "pip-bottom-right", "durationMs": dve.MaxMorphDurationMs + 1})
	require.Error(t, app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: "dve_preset_recall", Payload: long}))
}

func TestInitCommandQueue_DVESlotUpdateAnimates(t *testing.T) {
	relay := distribution.NewRelay()
	sw := switcher.New(relay)
	defer sw.Close()

	dc := dve.NewCompositor(1920, 1080)
	dc.SetLayout(&dve.Layout{Name: "pip", Slots: []dve.Slot{
		{SourceKey: "cam1", Transform: dve.DefaultTransform(0, 0, 480, 270), Enabled: true},
	}})

	app := &App{sw: sw, dveCompositor: dc}
	require.NoError(t, app.initCommandQueue())

	payload, _ := json.Marshal(map[string]any{
		"_path_id":   "0",
		"transform":  map[string]any{"positionX": 1200, "positionY": 600, "width": 480, "height": 270},
		"durationMs": dve.MaxMorphDurationMs,
	})
	require.NoError(t, app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: "dve_slot_update", Payload: payload}))

	require.Equal(t, 1200.0, dc.GetLayout().Slots[0].Transform.PositionX)
	snaps := dc.SnapshotSlots()
	require.Len(t, snaps, 1)
	require.Less(t, snaps[0].RectX, 100, "timed slot update with durationMs must glide")

	long, _ := json.Marshal(map[string]any{
		"_path_id":   "0",
		"transform":  map[string]any{"positionX": 0, "positionY": 0, "width": 480, "height": 270},
		"durationMs": dve.MaxMorphDurationMs + 1,
	})
	require.Error(t, app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: "dve_slot_update", Payload: long}))
	require.Equal(t, 1200.0, dc.GetLayout().Slots[0].Transform.PositionX, "rejected update not applied")
}

// An animated slot update applies its edit to the layout current under the
// compositor lock; an edit to another slot landing meanwhile is kept.
func TestInitCommandQueue_DVESlotUpdateKeepsConcurrentEdits(t *testing.T) {
	relay := distribution.NewRelay()
	sw := switcher.New(relay)
	defer sw.Close()

	dc := dve.NewCompositor(1920, 1080)
	dc.SetLayout(&dve.Layout{Name: "two", Slots: []dve.Slot{
		{SourceKey: "cam1", Transform: dve.DefaultTransform(0, 0, 480, 270), Enabled: true},
		{SourceKey: "cam2", Transform: dve.DefaultTransform(0, 540, 480, 270), Enabled: true},
	}})
	app := &App{sw: sw, dveCompositor: dc}
	require.NoError(t, app.initCommandQueue())

	payload, _ := json.Marshal(map[string]any{
		"_path_id":   "0",
		"transform":  map[string]any{"positionX": 1200, "positionY": 0, "width": 480, "height": 270},
		"durationMs": 100,
	})
	// A writer keeps editing slot 1 while updates to slot 0 dispatch; an
	// update that stored a stale copy of slot 1 rolls its value back.
	stop := make(chan struct{})
	lost := make(chan float64, 1)
	go func() {
		defer close(lost)
		for i := 1.0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			var got float64
			dc.UpdateSlot(1, func(s *dve.Slot) { got, s.Transform.PositionX = s.Transform.PositionX, i })
			if got != i-1 && i > 1 {
				lost <- got
				return
			}
		}
	}()
	for range 500 {
		require.NoError(t, app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: "dve_slot_update", Payload: payload}))
	}
	close(stop)
	if got, ok := <-lost; ok {
		t.Fatalf("edit to slot 1 rolled back to %v", got)
	}
}

// A channel pair goes through the command queue, so a WAL replay restores
// it like the other audio settings.
func TestInitCommandQueue_AudioChannelPair(t *testing.T) {
	sw := switcher.New(distribution.NewRelay())
	defer sw.Close()
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	defer func() { _ = m.Close() }()
	app := &App{sw: sw, mixer: m}
	require.NoError(t, app.initCommandQueue())

	payload, _ := json.Marshal(map[string]any{"_path_source": "mxl:cam1", "firstChannel": 3})
	require.NoError(t, app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: "audio_channel_pair", Payload: payload}))
	require.Equal(t, 3, m.ChannelPair("mxl:cam1"))

	bad, _ := json.Marshal(map[string]any{"_path_source": "mxl:cam1", "firstChannel": 4})
	require.ErrorIs(t, app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: "audio_channel_pair", Payload: bad}), audio.ErrInvalidChannelPair)
	require.Equal(t, 3, m.ChannelPair("mxl:cam1"))
}

// A timed squeeze-back replays what the REST call would have done.
func TestInitCommandQueue_Squeezeback(t *testing.T) {
	relay := distribution.NewRelay()
	sw := switcher.New(relay)
	defer sw.Close()
	sw.RegisterSource("cam2", distribution.NewRelay())

	sq := dve.NewSqueeze(1920, 1080)
	app := &App{sw: sw, squeeze: sq}
	require.NoError(t, app.initCommandQueue())

	dispatch := func(action string, body map[string]any) error {
		payload, _ := json.Marshal(body)
		return app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: action, Payload: payload})
	}
	require.NoError(t, dispatch("squeezeback_set", map[string]any{
		"rect": map[string]any{"x": 480, "y": 0, "w": 1440, "h": 810}, "background": "cam2", "durationMs": 60000,
		"executeAt": 1,
	}))
	require.NoError(t, dispatch("squeezeback_on", map[string]any{"executeAt": 1}))
	st := sq.Snapshot()
	require.True(t, st.On)
	require.True(t, st.Animating, "on takes the configured duration")
	require.Equal(t, "cam2", sq.BackgroundOnAir())

	require.NoError(t, dispatch("squeezeback_off", map[string]any{"durationMs": 0}))
	require.False(t, sq.Active())

	require.Error(t, dispatch("squeezeback_set", map[string]any{
		"rect": map[string]any{"x": 0, "y": 0, "w": 960, "h": 540}, "background": "nope",
	}))
}
