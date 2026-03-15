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
		// Structured SRT streamID tests (SRT Access Control spec)
		{
			name:     "structured streamID with resource",
			streamID: "#!::u=admin,r=live/camera1,m=publish",
			want:     "camera1",
		},
		{
			name:     "structured streamID resource without live prefix",
			streamID: "#!::u=admin,r=camera2,m=publish",
			want:     "camera2",
		},
		{
			name:     "structured streamID no resource field",
			streamID: "#!::u=admin,m=publish",
			want:     "default",
		},
		{
			name:     "structured streamID with session",
			streamID: "#!::u=admin,r=live/stream,m=publish,s=session123",
			want:     "stream",
		},
		{
			name:     "structured streamID resource with nested path",
			streamID: "#!::r=live/studio/cam1,m=publish",
			want:     "studio/cam1",
		},
		{
			name:     "structured streamID empty resource",
			streamID: "#!::r=,m=publish",
			want:     "default",
		},
		// Sanitization tests (path traversal prevention)
		{
			name:     "path traversal attempt",
			streamID: "live/../../etc/passwd",
			want:     "etc/passwd",
		},
		{
			name:     "path traversal in structured streamID",
			streamID: "#!::r=../../etc/passwd,m=publish",
			want:     "etc/passwd",
		},
		{
			name:     "special characters stripped",
			streamID: "live/cam<script>1",
			want:     "camscript1",
		},
		{
			name:     "double slashes collapsed",
			streamID: "live//camera1",
			want:     "camera1",
		},
		{
			name:     "only unsafe characters",
			streamID: "live/<>{}",
			want:     "default",
		},
		{
			name:     "dots allowed in normal names",
			streamID: "live/camera1.stream",
			want:     "camera1.stream",
		},
		{
			name:     "hyphens and underscores allowed",
			streamID: "live/my-camera_1",
			want:     "my-camera_1",
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

func TestSanitizeStreamKey(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clean key",
			input: "camera1",
			want:  "camera1",
		},
		{
			name:  "path traversal",
			input: "../../etc/passwd",
			want:  "etc/passwd",
		},
		{
			name:  "double dots in middle",
			input: "foo/../bar",
			want:  "foo/bar",
		},
		{
			name:  "double slashes",
			input: "foo//bar",
			want:  "foo/bar",
		},
		{
			name:  "special characters",
			input: "cam<script>alert(1)</script>",
			want:  "camscriptalert1/script",
		},
		{
			name:  "only dots",
			input: "....",
			want:  "default",
		},
		{
			name:  "empty after sanitization",
			input: "<>{}",
			want:  "default",
		},
		{
			name:  "leading and trailing slashes",
			input: "/foo/bar/",
			want:  "foo/bar",
		},
		{
			name:  "alphanumeric with allowed specials",
			input: "my-camera_1.stream",
			want:  "my-camera_1.stream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeStreamKey(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeStreamKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
