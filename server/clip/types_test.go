// server/clip/types_test.go
package clip

import (
	"testing"
	"time"
)

func TestClipSourceString(t *testing.T) {
	tests := []struct {
		s    ClipSource
		want string
	}{
		{SourceUpload, "upload"},
		{SourceReplay, "replay"},
		{SourceRecording, "recording"},
	}
	for _, tt := range tests {
		if got := string(tt.s); got != tt.want {
			t.Errorf("ClipSource = %q, want %q", got, tt.want)
		}
	}
}

func TestPlayerStateString(t *testing.T) {
	tests := []struct {
		s    PlayerState
		want string
	}{
		{StateEmpty, "empty"},
		{StateLoaded, "loaded"},
		{StatePlaying, "playing"},
		{StatePaused, "paused"},
		{StateHolding, "holding"},
	}
	for _, tt := range tests {
		if got := string(tt.s); got != tt.want {
			t.Errorf("PlayerState = %q, want %q", got, tt.want)
		}
	}
}

func TestClipPlayerStateDefaults(t *testing.T) {
	ps := ClipPlayerState{ID: 1}
	if ps.State != "" {
		t.Errorf("default State = %q, want empty", ps.State)
	}
	if ps.Speed != 0 {
		t.Errorf("default Speed = %f, want 0", ps.Speed)
	}
}

func TestClipValidation(t *testing.T) {
	c := Clip{
		ID:       "test-id",
		Name:     "Test Clip",
		Filename: "test.ts",
		Source:   SourceUpload,
		Codec:    "h264",
		Width:    1920,
		Height:   1080,
	}
	if c.ID == "" {
		t.Error("ID should not be empty")
	}
	if c.Source != SourceUpload {
		t.Errorf("Source = %q, want %q", c.Source, SourceUpload)
	}
}

func TestClipIsEphemeral(t *testing.T) {
	c := Clip{Ephemeral: true, CreatedAt: time.Now().Add(-25 * time.Hour)}
	if !c.Ephemeral {
		t.Error("should be ephemeral")
	}
}
