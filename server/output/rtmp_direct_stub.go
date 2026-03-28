//go:build !cgo || noffmpeg

package output

import (
	"context"

	"github.com/zsiec/prism/media"
)

// RTMPDirect is a stub for builds without FFmpeg support.
type RTMPDirect struct {
	config      RTMPCallerConfig
	onReconnect func()
}

// NewRTMPDirect returns an error when FFmpeg is not available.
func NewRTMPDirect(_ RTMPCallerConfig) (*RTMPDirect, error) {
	return nil, ErrRTMPNotAvailable
}

func (r *RTMPDirect) ID() string                           { return "rtmp-direct" }
func (r *RTMPDirect) Start(_ context.Context) error        { return ErrRTMPNotAvailable }
func (r *RTMPDirect) WriteVideo(_ *media.VideoFrame) error { return ErrRTMPNotAvailable }
func (r *RTMPDirect) WriteAudio(_ *media.AudioFrame) error { return ErrRTMPNotAvailable }
func (r *RTMPDirect) Close() error                         { return nil }
func (r *RTMPDirect) Status() AdapterStatus {
	return AdapterStatus{State: StateError, Error: ErrRTMPNotAvailable.Error()}
}

// RTMPStatusSnapshot returns status for ControlRoomState.
func (r *RTMPDirect) RTMPStatusSnapshot() RTMPStatus { return RTMPStatus{} }

// Compile-time check.
var _ FrameAdapter = (*RTMPDirect)(nil)
