package control

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

func TestErrorStatus(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		// switcher
		{switcher.ErrSourceNotFound, http.StatusNotFound},
		{switcher.ErrAlreadyOnProgram, http.StatusBadRequest},
		{switcher.ErrInvalidDelay, http.StatusBadRequest},
		{switcher.ErrNoTransition, http.StatusConflict},

		// transition
		{transition.ErrTransitionActive, http.StatusConflict},
		{transition.ErrFTBActive, http.StatusConflict},

		// audio
		{audio.ErrChannelNotFound, http.StatusNotFound},
		{audio.ErrInvalidTrim, http.StatusBadRequest},
		{audio.ErrInvalidBand, http.StatusBadRequest},
		{audio.ErrInvalidFrequency, http.StatusBadRequest},
		{audio.ErrInvalidGain, http.StatusBadRequest},
		{audio.ErrInvalidQ, http.StatusBadRequest},
		{audio.ErrInvalidThreshold, http.StatusBadRequest},
		{audio.ErrInvalidRatio, http.StatusBadRequest},
		{audio.ErrInvalidAttack, http.StatusBadRequest},
		{audio.ErrInvalidRelease, http.StatusBadRequest},
		{audio.ErrInvalidMakeupGain, http.StatusBadRequest},

		// output
		{output.ErrRecorderActive, http.StatusConflict},
		{output.ErrRecorderNotActive, http.StatusConflict},
		{output.ErrSRTActive, http.StatusConflict},
		{output.ErrSRTNotActive, http.StatusConflict},

		// preset
		{preset.ErrNotFound, http.StatusNotFound},
		{preset.ErrEmptyName, http.StatusBadRequest},

		// stinger
		{stinger.ErrNotFound, http.StatusNotFound},
		{stinger.ErrInvalidName, http.StatusBadRequest},
		{stinger.ErrInvalidCutPoint, http.StatusBadRequest},
		{stinger.ErrAlreadyExists, http.StatusConflict},
		{stinger.ErrMaxClipsReached, http.StatusConflict},

		// graphics
		{graphics.ErrNoOverlay, http.StatusBadRequest},
		{graphics.ErrAlreadyActive, http.StatusConflict},
		{graphics.ErrNotActive, http.StatusConflict},
		{graphics.ErrFadeActive, http.StatusConflict},

		// macro
		{macro.ErrNotFound, http.StatusNotFound},
		{macro.ErrEmptyName, http.StatusBadRequest},
		{macro.ErrNoSteps, http.StatusBadRequest},

		// operator
		{operator.ErrNotFound, http.StatusNotFound},
		{operator.ErrSessionNotFound, http.StatusNotFound},
		{operator.ErrEmptyName, http.StatusBadRequest},
		{operator.ErrInvalidRole, http.StatusBadRequest},
		{operator.ErrInvalidSubsystem, http.StatusBadRequest},
		{operator.ErrDuplicateName, http.StatusConflict},
		{operator.ErrNoPermission, http.StatusForbidden},
		{operator.ErrSubsystemLocked, http.StatusConflict},
		{operator.ErrNotLocked, http.StatusBadRequest},
		{operator.ErrLockNotOwned, http.StatusBadRequest},

		// replay
		{replay.ErrNoSource, http.StatusNotFound},
		{replay.ErrNoMarkIn, http.StatusBadRequest},
		{replay.ErrNoMarkOut, http.StatusBadRequest},
		{replay.ErrInvalidMarks, http.StatusBadRequest},
		{replay.ErrInvalidSpeed, http.StatusBadRequest},
		{replay.ErrEmptyClip, http.StatusBadRequest},
		{replay.ErrPlayerActive, http.StatusConflict},
		{replay.ErrNoPlayer, http.StatusBadRequest},
		{replay.ErrBufferDisabled, http.StatusBadRequest},
		{replay.ErrSourceMismatch, http.StatusBadRequest},
		{replay.ErrMaxSources, http.StatusConflict},
	}

	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			got := errorStatus(tt.err)
			require.Equal(t, tt.status, got, "errorStatus(%v)", tt.err)
		})
	}
}

func TestErrorStatus_WrappedErrors(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", switcher.ErrSourceNotFound)
	require.Equal(t, http.StatusNotFound, errorStatus(wrapped))

	doubleWrapped := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", replay.ErrPlayerActive))
	require.Equal(t, http.StatusConflict, errorStatus(doubleWrapped))
}

func TestErrorStatus_UnknownError(t *testing.T) {
	unknown := errors.New("some random error")
	require.Equal(t, http.StatusInternalServerError, errorStatus(unknown))
}
