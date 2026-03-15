package srt

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ModeListener = "listener"
	ModeCaller   = "caller"

	KeyPrefix      = "srt:"
	DefaultPort    = ":6464"
	DefaultLatency = 120 * time.Millisecond
	MaxLatency     = 10000 // ms
	MaxDelay       = 500   // ms
)

// SourceConfig defines an SRT source (persisted for caller, restored for listener).
type SourceConfig struct {
	Key       string `json:"key"`
	Mode      string `json:"mode"`
	Address   string `json:"address,omitempty"`
	StreamID  string `json:"streamID"`
	Label     string `json:"label,omitempty"`
	Position  int    `json:"position,omitempty"`
	LatencyMs int    `json:"latencyMs,omitempty"`
	DelayMs   int    `json:"delayMs,omitempty"`
}

func (c *SourceConfig) Validate() error {
	if c.Key == "" {
		return errors.New("key is required")
	}
	if !strings.HasPrefix(c.Key, KeyPrefix) {
		return fmt.Errorf("key must start with %q", KeyPrefix)
	}
	if c.Mode != ModeListener && c.Mode != ModeCaller {
		return fmt.Errorf("mode must be %q or %q", ModeListener, ModeCaller)
	}
	if c.Mode == ModeCaller && c.Address == "" {
		return errors.New("address is required for caller mode")
	}
	if c.StreamID == "" {
		return errors.New("streamID is required")
	}
	if c.LatencyMs < 0 || c.LatencyMs > MaxLatency {
		return fmt.Errorf("latencyMs must be 0-%d", MaxLatency)
	}
	if c.DelayMs < 0 || c.DelayMs > MaxDelay {
		return fmt.Errorf("delayMs must be 0-%d", MaxDelay)
	}
	return nil
}

// ExtractStreamKey derives a source key suffix from an SRT streamid.
func ExtractStreamKey(streamID string) string {
	s := strings.TrimPrefix(streamID, "/")
	s = strings.TrimPrefix(s, "live/")
	if s == "" {
		return "default"
	}
	return s
}

// SRTSourceInfo holds live SRT connection metadata broadcast in ControlRoomState.
type SRTSourceInfo struct {
	Mode        string  `json:"mode"`
	StreamID    string  `json:"streamID"`
	RemoteAddr  string  `json:"remoteAddr,omitempty"`
	LatencyMs   int     `json:"latencyMs"`
	RTTMs       float64 `json:"rttMs"`
	LossRate    float64 `json:"lossRate"`
	BitrateKbps float64 `json:"bitrateKbps"`
	RecvBufMs   float64 `json:"recvBufMs"`
	Connected   bool    `json:"connected"`
}
