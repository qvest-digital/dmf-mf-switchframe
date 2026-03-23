//go:build !cgo || noffmpeg

package output

import "context"

// RTMPCaller is a stub for builds without FFmpeg support.
type RTMPCaller struct{}

// NewRTMPCaller returns an error when FFmpeg is not available.
func NewRTMPCaller(_ RTMPCallerConfig) (*RTMPCaller, error) {
	return nil, ErrRTMPNotAvailable
}

func (c *RTMPCaller) ID() string                       { return "rtmp" }
func (c *RTMPCaller) Start(_ context.Context) error     { return ErrRTMPNotAvailable }
func (c *RTMPCaller) Write(_ []byte) (int, error)       { return 0, ErrRTMPNotAvailable }
func (c *RTMPCaller) Close() error                      { return nil }
func (c *RTMPCaller) Status() AdapterStatus             { return AdapterStatus{State: StateError, Error: ErrRTMPNotAvailable.Error()} }
func (c *RTMPCaller) RTMPStatusSnapshot() RTMPStatus    { return RTMPStatus{} }

// RTMPStatus holds runtime RTMP connection info for ControlRoomState.
type RTMPStatus struct {
	Active       bool   `json:"active"`
	URL          string `json:"url,omitempty"`
	State        string `json:"state,omitempty"`
	BytesWritten int64  `json:"bytesWritten,omitempty"`
	Error        string `json:"error,omitempty"`
}
