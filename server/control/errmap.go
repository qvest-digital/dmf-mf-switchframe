package control

import (
	"errors"
	"net/http"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// errorMapping pairs a sentinel error with its HTTP status code.
type errorMapping struct {
	err    error
	status int
}

// errorMappings defines the canonical mapping from sentinel errors to HTTP
// status codes. Checked in order with errors.Is() so wrapped errors work.
var errorMappings = []errorMapping{
	// 404 Not Found
	{switcher.ErrSourceNotFound, http.StatusNotFound},
	{audio.ErrChannelNotFound, http.StatusNotFound},
	{preset.ErrNotFound, http.StatusNotFound},
	{stinger.ErrNotFound, http.StatusNotFound},
	{macro.ErrNotFound, http.StatusNotFound},
	{graphics.ErrNoOverlay, http.StatusBadRequest}, // no overlay loaded is a bad request
	{graphics.ErrLayerNotFound, http.StatusNotFound},
	{operator.ErrNotFound, http.StatusNotFound},
	{operator.ErrSessionNotFound, http.StatusNotFound},
	{replay.ErrNoSource, http.StatusNotFound},
	{scte35.ErrRuleNotFound, http.StatusNotFound},
	{scte35.ErrTemplateNotFound, http.StatusNotFound},

	// 409 Conflict
	{switcher.ErrFormatDuringTransition, http.StatusConflict},
	{switcher.ErrNoTransition, http.StatusConflict},
	{transition.ErrActive, http.StatusConflict},
	{transition.ErrFTBActive, http.StatusConflict},
	{output.ErrRecorderActive, http.StatusConflict},
	{output.ErrRecorderNotActive, http.StatusConflict},
	{output.ErrSRTActive, http.StatusConflict},
	{output.ErrSRTNotActive, http.StatusConflict},
	{output.ErrDestinationNotFound, http.StatusNotFound},
	{output.ErrDestinationActive, http.StatusConflict},
	{output.ErrDestinationStopped, http.StatusConflict},
	{stinger.ErrAlreadyExists, http.StatusConflict},
	{stinger.ErrMaxClipsReached, http.StatusConflict},
	{graphics.ErrAlreadyActive, http.StatusConflict},
	{graphics.ErrNotActive, http.StatusConflict},
	{graphics.ErrFadeActive, http.StatusConflict},
	{graphics.ErrTooManyLayers, http.StatusConflict},
	{operator.ErrDuplicateName, http.StatusConflict},
	{operator.ErrSubsystemLocked, http.StatusConflict},
	{replay.ErrPlayerActive, http.StatusConflict},
	{replay.ErrMaxSources, http.StatusConflict},

	// 403 Forbidden
	{operator.ErrNoPermission, http.StatusForbidden},

	// 400 Bad Request
	{switcher.ErrAlreadyOnProgram, http.StatusBadRequest},
	{switcher.ErrInvalidDelay, http.StatusBadRequest},
	{switcher.ErrInvalidPosition, http.StatusBadRequest},
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
	{preset.ErrEmptyName, http.StatusBadRequest},
	{stinger.ErrInvalidName, http.StatusBadRequest},
	{stinger.ErrInvalidCutPoint, http.StatusBadRequest},
	{macro.ErrEmptyName, http.StatusBadRequest},
	{macro.ErrNoSteps, http.StatusBadRequest},
	{operator.ErrEmptyName, http.StatusBadRequest},
	{operator.ErrInvalidRole, http.StatusBadRequest},
	{operator.ErrInvalidSubsystem, http.StatusBadRequest},
	{operator.ErrNotLocked, http.StatusBadRequest},
	{operator.ErrLockNotOwned, http.StatusBadRequest},
	{replay.ErrNoMarkIn, http.StatusBadRequest},
	{replay.ErrNoMarkOut, http.StatusBadRequest},
	{replay.ErrInvalidMarks, http.StatusBadRequest},
	{replay.ErrInvalidSpeed, http.StatusBadRequest},
	{replay.ErrEmptyClip, http.StatusBadRequest},
	{replay.ErrNoPlayer, http.StatusBadRequest},
	{replay.ErrBufferDisabled, http.StatusBadRequest},
	{replay.ErrSourceMismatch, http.StatusBadRequest},
}

// errorStatus maps a sentinel error to an HTTP status code.
// It uses errors.Is() so wrapped errors are matched correctly.
// Unknown errors default to 500 Internal Server Error.
func errorStatus(err error) int {
	for _, m := range errorMappings {
		if errors.Is(err, m.err) {
			return m.status
		}
	}
	return http.StatusInternalServerError
}
