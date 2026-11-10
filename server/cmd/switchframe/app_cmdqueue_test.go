package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
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

	payload, _ := json.Marshal(map[string]any{"_path_id": "pip-bottom-right", "durationMs": 60000, "easing": "ease-in-out"})
	require.NoError(t, app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: "dve_preset_recall", Payload: payload}))

	require.Greater(t, dc.GetLayout().Slots[0].Transform.OutputRect.X, float64(format.Width)/2, "state reports the target")
	snaps := dc.SnapshotSlots()
	require.Len(t, snaps, 1)
	require.Less(t, snaps[0].RectX, format.Width/2, "timed recall with durationMs must animate, not cut")

	bad, _ := json.Marshal(map[string]any{"_path_id": "pip-bottom-right", "durationMs": 500, "easing": "bounce"})
	require.Error(t, app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: "dve_preset_recall", Payload: bad}))
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
		"durationMs": 60000,
	})
	require.NoError(t, app.cmdDispatcher.Dispatch(cmdqueue.Command{Action: "dve_slot_update", Payload: payload}))

	require.Equal(t, 1200.0, dc.GetLayout().Slots[0].Transform.PositionX)
	snaps := dc.SnapshotSlots()
	require.Len(t, snaps, 1)
	require.Less(t, snaps[0].RectX, 100, "timed slot update with durationMs must glide")
}
