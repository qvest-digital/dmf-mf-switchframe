package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/cmdqueue"
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
