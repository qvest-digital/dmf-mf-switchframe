package srt

import (
	"testing"
)

func TestSourceConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  SourceConfig
		wantErr string
	}{
		{
			name: "valid listener",
			config: SourceConfig{
				Key:      "srt:camera1",
				Mode:     ModeListener,
				StreamID: "live/camera1",
			},
			wantErr: "",
		},
		{
			name: "valid caller",
			config: SourceConfig{
				Key:      "srt:remote1",
				Mode:     ModeCaller,
				Address:  "192.168.1.100:9000",
				StreamID: "live/remote1",
			},
			wantErr: "",
		},
		{
			name: "missing key",
			config: SourceConfig{
				Mode:     ModeListener,
				StreamID: "live/camera1",
			},
			wantErr: "key is required",
		},
		{
			name: "key without srt prefix",
			config: SourceConfig{
				Key:      "camera1",
				Mode:     ModeListener,
				StreamID: "live/camera1",
			},
			wantErr: `key must start with "srt:"`,
		},
		{
			name: "invalid mode",
			config: SourceConfig{
				Key:      "srt:camera1",
				Mode:     "rendezvous",
				StreamID: "live/camera1",
			},
			wantErr: `mode must be "listener" or "caller"`,
		},
		{
			name: "caller missing address",
			config: SourceConfig{
				Key:      "srt:remote1",
				Mode:     ModeCaller,
				StreamID: "live/remote1",
			},
			wantErr: "address is required for caller mode",
		},
		{
			name: "missing streamID",
			config: SourceConfig{
				Key:  "srt:camera1",
				Mode: ModeListener,
			},
			wantErr: "streamID is required",
		},
		{
			name: "negative latency",
			config: SourceConfig{
				Key:       "srt:camera1",
				Mode:      ModeListener,
				StreamID:  "live/camera1",
				LatencyMs: -1,
			},
			wantErr: "latencyMs must be 0-10000",
		},
		{
			name: "latency too high",
			config: SourceConfig{
				Key:       "srt:camera1",
				Mode:      ModeListener,
				StreamID:  "live/camera1",
				LatencyMs: 10001,
			},
			wantErr: "latencyMs must be 0-10000",
		},
		{
			name: "negative delay",
			config: SourceConfig{
				Key:      "srt:camera1",
				Mode:     ModeListener,
				StreamID: "live/camera1",
				DelayMs:  -1,
			},
			wantErr: "delayMs must be 0-500",
		},
		{
			name: "delay too high",
			config: SourceConfig{
				Key:      "srt:camera1",
				Mode:     ModeListener,
				StreamID: "live/camera1",
				DelayMs:  501,
			},
			wantErr: "delayMs must be 0-500",
		},
		{
			name: "valid with all optional fields",
			config: SourceConfig{
				Key:       "srt:camera1",
				Mode:      ModeListener,
				StreamID:  "live/camera1",
				Label:     "Camera 1",
				Position:  3,
				LatencyMs: 200,
				DelayMs:   50,
			},
			wantErr: "",
		},
		{
			name: "valid with zero latency and delay",
			config: SourceConfig{
				Key:       "srt:camera1",
				Mode:      ModeListener,
				StreamID:  "live/camera1",
				LatencyMs: 0,
				DelayMs:   0,
			},
			wantErr: "",
		},
		{
			name: "valid with max latency",
			config: SourceConfig{
				Key:       "srt:camera1",
				Mode:      ModeListener,
				StreamID:  "live/camera1",
				LatencyMs: MaxLatency,
			},
			wantErr: "",
		},
		{
			name: "valid with max delay",
			config: SourceConfig{
				Key:      "srt:camera1",
				Mode:     ModeListener,
				StreamID: "live/camera1",
				DelayMs:  MaxDelay,
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else if err.Error() != tt.wantErr {
					t.Errorf("expected error %q, got %q", tt.wantErr, err.Error())
				}
			}
		})
	}
}

func TestExtractStreamKey(t *testing.T) {
	tests := []struct {
		name     string
		streamID string
		want     string
	}{
		{
			name:     "live prefix",
			streamID: "live/camera1",
			want:     "camera1",
		},
		{
			name:     "no prefix",
			streamID: "camera1",
			want:     "camera1",
		},
		{
			name:     "leading slash with live prefix",
			streamID: "/live/camera1",
			want:     "camera1",
		},
		{
			name:     "nested path after live",
			streamID: "live/studio/cam1",
			want:     "studio/cam1",
		},
		{
			name:     "empty string",
			streamID: "",
			want:     "default",
		},
		{
			name:     "just live prefix",
			streamID: "live/",
			want:     "default",
		},
		{
			name:     "leading slash only",
			streamID: "/",
			want:     "default",
		},
		{
			name:     "leading slash with live and empty",
			streamID: "/live/",
			want:     "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractStreamKey(tt.streamID)
			if got != tt.want {
				t.Errorf("ExtractStreamKey(%q) = %q, want %q", tt.streamID, got, tt.want)
			}
		})
	}
}
