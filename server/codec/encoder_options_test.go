package codec

import (
	"testing"
)

func TestOutputPreset_Valid(t *testing.T) {
	tests := []struct {
		name   string
		preset OutputPreset
		want   bool
	}{
		{"low-latency", OutputPresetLowLatency, true},
		{"balanced", OutputPresetBalanced, true},
		{"quality", OutputPresetQuality, true},
		{"empty string", OutputPreset(""), true},
		{"invalid", OutputPreset("bogus"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.preset.Valid(); got != tt.want {
				t.Errorf("OutputPreset(%q).Valid() = %v, want %v", tt.preset, got, tt.want)
			}
		})
	}
}

func TestOutputPreset_NVENCPreset(t *testing.T) {
	tests := []struct {
		name   string
		preset OutputPreset
		want   string
	}{
		{"low-latency", OutputPresetLowLatency, "p3"},
		{"balanced", OutputPresetBalanced, "p4"},
		{"quality", OutputPresetQuality, "p5"},
		{"fastest", OutputPresetFastest, "p1"},
		{"empty default", OutputPreset(""), "p4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.preset.NVENCPreset(); got != tt.want {
				t.Errorf("OutputPreset(%q).NVENCPreset() = %q, want %q", tt.preset, got, tt.want)
			}
		})
	}
}

func TestOutputPreset_X264Preset(t *testing.T) {
	tests := []struct {
		name   string
		preset OutputPreset
		want   string
	}{
		{"low-latency", OutputPresetLowLatency, "superfast"},
		{"balanced", OutputPresetBalanced, "superfast"},
		{"quality", OutputPresetQuality, "faster"},
		{"fastest", OutputPresetFastest, "ultrafast"},
		{"empty default", OutputPreset(""), "superfast"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.preset.X264Preset(); got != tt.want {
				t.Errorf("OutputPreset(%q).X264Preset() = %q, want %q", tt.preset, got, tt.want)
			}
		})
	}
}

func TestOutputPreset_X265Preset(t *testing.T) {
	tests := []struct {
		name   string
		preset OutputPreset
		want   string
	}{
		{"low-latency", OutputPresetLowLatency, "superfast"},
		{"balanced", OutputPresetBalanced, "superfast"},
		{"quality", OutputPresetQuality, "faster"},
		{"empty default", OutputPreset(""), "superfast"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.preset.X265Preset(); got != tt.want {
				t.Errorf("OutputPreset(%q).X265Preset() = %q, want %q", tt.preset, got, tt.want)
			}
		})
	}
}

func TestEncoderOptions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    EncoderOptions
		wantErr bool
	}{
		{
			name:    "zero value is valid",
			opts:    EncoderOptions{},
			wantErr: false,
		},
		{
			name:    "valid bitrate",
			opts:    EncoderOptions{Bitrate: 5_000_000},
			wantErr: false,
		},
		{
			name:    "min bitrate",
			opts:    EncoderOptions{Bitrate: MinBitrate},
			wantErr: false,
		},
		{
			name:    "max bitrate",
			opts:    EncoderOptions{Bitrate: MaxBitrate},
			wantErr: false,
		},
		{
			name:    "bitrate too low",
			opts:    EncoderOptions{Bitrate: 500_000},
			wantErr: true,
		},
		{
			name:    "bitrate too high",
			opts:    EncoderOptions{Bitrate: 60_000_000},
			wantErr: true,
		},
		{
			name:    "gop 1",
			opts:    EncoderOptions{GOPSecs: 1},
			wantErr: false,
		},
		{
			name:    "gop 2",
			opts:    EncoderOptions{GOPSecs: 2},
			wantErr: false,
		},
		{
			name:    "gop 4",
			opts:    EncoderOptions{GOPSecs: 4},
			wantErr: false,
		},
		{
			name:    "invalid gop 3",
			opts:    EncoderOptions{GOPSecs: 3},
			wantErr: true,
		},
		{
			name:    "invalid gop 5",
			opts:    EncoderOptions{GOPSecs: 5},
			wantErr: true,
		},
		{
			name:    "valid preset low-latency",
			opts:    EncoderOptions{Preset: OutputPresetLowLatency},
			wantErr: false,
		},
		{
			name:    "valid preset balanced",
			opts:    EncoderOptions{Preset: OutputPresetBalanced},
			wantErr: false,
		},
		{
			name:    "valid preset quality",
			opts:    EncoderOptions{Preset: OutputPresetQuality},
			wantErr: false,
		},
		{
			name:    "invalid preset",
			opts:    EncoderOptions{Preset: OutputPreset("bogus")},
			wantErr: true,
		},
		{
			name: "full valid config",
			opts: EncoderOptions{
				Bitrate:  8_000_000,
				GOPSecs:  2,
				Preset:   OutputPresetBalanced,
				Timecode: true,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("EncoderOptions.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEncoderOptions_WithDefaults(t *testing.T) {
	t.Run("zero value gets defaults", func(t *testing.T) {
		opts := EncoderOptions{}
		got := opts.WithDefaults()
		if got.GOPSecs != 2 {
			t.Errorf("GOPSecs = %d, want 2", got.GOPSecs)
		}
		if got.Preset != OutputPresetBalanced {
			t.Errorf("Preset = %q, want %q", got.Preset, OutputPresetBalanced)
		}
		if got.Bitrate != 0 {
			t.Errorf("Bitrate = %d, want 0 (auto)", got.Bitrate)
		}
		if got.Timecode != false {
			t.Errorf("Timecode = %v, want false", got.Timecode)
		}
	})

	t.Run("explicit values preserved", func(t *testing.T) {
		opts := EncoderOptions{
			Bitrate:  10_000_000,
			GOPSecs:  4,
			Preset:   OutputPresetQuality,
			Timecode: true,
		}
		got := opts.WithDefaults()
		if got.Bitrate != 10_000_000 {
			t.Errorf("Bitrate = %d, want 10000000", got.Bitrate)
		}
		if got.GOPSecs != 4 {
			t.Errorf("GOPSecs = %d, want 4", got.GOPSecs)
		}
		if got.Preset != OutputPresetQuality {
			t.Errorf("Preset = %q, want %q", got.Preset, OutputPresetQuality)
		}
		if got.Timecode != true {
			t.Errorf("Timecode = %v, want true", got.Timecode)
		}
	})
}

func TestEncoderOptions_BitrateWarning(t *testing.T) {
	tests := []struct {
		name    string
		opts    EncoderOptions
		width   int
		height  int
		fpsNum  int
		fpsDen  int
		wantMsg bool
	}{
		{
			name:    "auto bitrate never warns",
			opts:    EncoderOptions{Bitrate: 0},
			width:   1920,
			height:  1080,
			fpsNum:  60,
			fpsDen:  1,
			wantMsg: false,
		},
		{
			name:    "1080p60 adequate bitrate",
			opts:    EncoderOptions{Bitrate: 8_000_000},
			width:   1920,
			height:  1080,
			fpsNum:  60,
			fpsDen:  1,
			wantMsg: false,
		},
		{
			name:    "1080p60 low bitrate",
			opts:    EncoderOptions{Bitrate: 4_000_000},
			width:   1920,
			height:  1080,
			fpsNum:  60,
			fpsDen:  1,
			wantMsg: true,
		},
		{
			name:    "1080p30 adequate bitrate",
			opts:    EncoderOptions{Bitrate: 5_000_000},
			width:   1920,
			height:  1080,
			fpsNum:  30,
			fpsDen:  1,
			wantMsg: false,
		},
		{
			name:    "1080p30 low bitrate",
			opts:    EncoderOptions{Bitrate: 3_000_000},
			width:   1920,
			height:  1080,
			fpsNum:  30,
			fpsDen:  1,
			wantMsg: true,
		},
		{
			name:    "720p60 adequate bitrate",
			opts:    EncoderOptions{Bitrate: 4_000_000},
			width:   1280,
			height:  720,
			fpsNum:  60,
			fpsDen:  1,
			wantMsg: false,
		},
		{
			name:    "720p60 low bitrate",
			opts:    EncoderOptions{Bitrate: 2_000_000},
			width:   1280,
			height:  720,
			fpsNum:  60,
			fpsDen:  1,
			wantMsg: true,
		},
		{
			name:    "720p30 adequate bitrate",
			opts:    EncoderOptions{Bitrate: 3_000_000},
			width:   1280,
			height:  720,
			fpsNum:  30,
			fpsDen:  1,
			wantMsg: false,
		},
		{
			name:    "720p30 low bitrate",
			opts:    EncoderOptions{Bitrate: 1_500_000},
			width:   1280,
			height:  720,
			fpsNum:  30,
			fpsDen:  1,
			wantMsg: true,
		},
		{
			name:    "480p30 adequate bitrate",
			opts:    EncoderOptions{Bitrate: 2_500_000},
			width:   640,
			height:  480,
			fpsNum:  30,
			fpsDen:  1,
			wantMsg: false,
		},
		{
			name:    "480p30 low bitrate",
			opts:    EncoderOptions{Bitrate: 1_500_000},
			width:   640,
			height:  480,
			fpsNum:  30,
			fpsDen:  1,
			wantMsg: true,
		},
		{
			name:    "1080p50 is high fps",
			opts:    EncoderOptions{Bitrate: 5_000_000},
			width:   1920,
			height:  1080,
			fpsNum:  50,
			fpsDen:  1,
			wantMsg: true,
		},
		{
			name:    "fractional fps 59.94",
			opts:    EncoderOptions{Bitrate: 4_000_000},
			width:   1920,
			height:  1080,
			fpsNum:  60000,
			fpsDen:  1001,
			wantMsg: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.opts.BitrateWarning(tt.width, tt.height, tt.fpsNum, tt.fpsDen)
			if tt.wantMsg && got == "" {
				t.Error("BitrateWarning() returned empty string, want warning")
			}
			if !tt.wantMsg && got != "" {
				t.Errorf("BitrateWarning() = %q, want empty string", got)
			}
		})
	}
}
