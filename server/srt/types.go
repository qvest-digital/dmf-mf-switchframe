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
// It handles both plain streamIDs ("live/camera1") and structured streamIDs
// per the SRT Access Control spec ("#!::u=admin,r=live/camera1,m=publish").
func ExtractStreamKey(streamID string) string {
	// Handle structured streamID format: #!::key=value,key=value
	if strings.HasPrefix(streamID, "#!::") {
		parts := strings.Split(streamID[4:], ",")
		for _, part := range parts {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 && kv[0] == "r" {
				return sanitizeStreamKey(stripLivePrefix(kv[1]))
			}
		}
		return "default"
	}
	return sanitizeStreamKey(stripLivePrefix(streamID))
}

// stripLivePrefix removes a leading "/" and "live/" prefix from a stream key.
func stripLivePrefix(s string) string {
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimPrefix(s, "live/")
	if s == "" {
		return "default"
	}
	return s
}

// sanitizeStreamKey removes path traversal sequences and unsafe characters
// from a stream key, preventing directory traversal attacks.
func sanitizeStreamKey(key string) string {
	// Remove path traversal
	key = strings.ReplaceAll(key, "..", "")
	key = strings.ReplaceAll(key, "//", "/")
	key = strings.TrimPrefix(key, "/")
	key = strings.TrimSuffix(key, "/")
	// Limit to safe characters: alphanumeric, hyphen, underscore, slash, dot
	var result strings.Builder
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '/' || r == '.' {
			result.WriteRune(r)
		}
	}
	s := result.String()
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
