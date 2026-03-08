package switcher

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFormatPresets(t *testing.T) {
	require.NotEmpty(t, FormatPresets)
	for name, f := range FormatPresets {
		t.Run(name, func(t *testing.T) {
			require.Positive(t, f.Width, "Width must be positive")
			require.Positive(t, f.Height, "Height must be positive")
			require.Positive(t, f.FPSNum, "FPSNum must be positive")
			require.Positive(t, f.FPSDen, "FPSDen must be positive")
			require.NotEmpty(t, f.Name, "Name must not be empty")
		})
	}
}

func TestFormatFPS(t *testing.T) {
	tests := []struct {
		preset string
		want   float64
		delta  float64
	}{
		{"1080p29.97", 29.970, 0.001},
		{"1080p25", 25.0, 0},
		{"1080p59.94", 59.940, 0.001},
		{"1080p23.976", 23.976, 0.001},
		{"1080p30", 30.0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.preset, func(t *testing.T) {
			f := FormatPresets[tt.preset]
			got := f.FPS()
			if tt.delta == 0 {
				require.Equal(t, tt.want, got)
			} else {
				require.InDelta(t, tt.want, got, tt.delta)
			}
		})
	}
}

func TestFormatFPSZeroDen(t *testing.T) {
	f := PipelineFormat{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 0}
	require.Equal(t, float64(0), f.FPS())
	require.Equal(t, float32(0), f.FPSFloat32())
}

func TestFormatFrameDuration(t *testing.T) {
	tests := []struct {
		preset   string
		wantNs   int64
		deltaNs  int64
	}{
		{"1080p30", 33333333, 0},
		{"1080p25", 40000000, 0},
		{"1080p24", 41666666, 0},
		{"1080p29.97", 33366700, 100},
		{"1080p59.94", 16683350, 100},
	}
	for _, tt := range tests {
		t.Run(tt.preset, func(t *testing.T) {
			f := FormatPresets[tt.preset]
			got := f.FrameDuration().Nanoseconds()
			if tt.deltaNs == 0 {
				require.Equal(t, tt.wantNs, got)
			} else {
				require.InDelta(t, tt.wantNs, got, float64(tt.deltaNs))
			}
		})
	}
}

func TestFormatFrameDurationZeroFPS(t *testing.T) {
	f := PipelineFormat{Width: 1920, Height: 1080, FPSNum: 0, FPSDen: 1}
	require.Equal(t, 33333*time.Microsecond, f.FrameDuration())
}

func TestFormatFrameBudgetNs(t *testing.T) {
	for name, f := range FormatPresets {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, f.FrameDuration().Nanoseconds(), f.FrameBudgetNs())
		})
	}
}

func TestFormatString(t *testing.T) {
	// Named format returns name
	f := FormatPresets["1080p29.97"]
	require.Equal(t, "1080p29.97", f.String())

	// Unnamed format returns "WxH@N/D"
	unnamed := PipelineFormat{Width: 640, Height: 480, FPSNum: 15, FPSDen: 1}
	require.Equal(t, "640x480@15/1", unnamed.String())
}

func TestDefaultFormat(t *testing.T) {
	require.Equal(t, 1920, DefaultFormat.Width)
	require.Equal(t, 1080, DefaultFormat.Height)
	require.Equal(t, 30000, DefaultFormat.FPSNum)
	require.Equal(t, 1001, DefaultFormat.FPSDen)
	require.Equal(t, "1080p29.97", DefaultFormat.Name)
}

func TestValidFormatPreset(t *testing.T) {
	for name := range FormatPresets {
		require.True(t, ValidFormatPreset(name), "expected %q to be valid", name)
	}
	require.False(t, ValidFormatPreset("bogus"))
	require.False(t, ValidFormatPreset(""))
}

func TestFormatEvenDimensions(t *testing.T) {
	for name, f := range FormatPresets {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, 0, f.Width%2, "Width must be even for YUV420")
			require.Equal(t, 0, f.Height%2, "Height must be even for YUV420")
		})
	}
}

func BenchmarkFormatFPS(b *testing.B) {
	f := FormatPresets["1080p29.97"]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = f.FPS()
	}
}

func BenchmarkFormatFrameDuration(b *testing.B) {
	f := FormatPresets["1080p29.97"]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = f.FrameDuration()
	}
}

func TestSetPipelineFormat_RejectedDuringTransition(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	sw.SetTransitionConfig(mockTransitionCodecs())
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	// Start a transition
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 2000, ""))

	// Attempt to change format during transition
	err := sw.SetPipelineFormat(FormatPresets["1080p25"])
	require.ErrorIs(t, err, ErrFormatDuringTransition)

	// Verify format is unchanged (still default)
	f := sw.PipelineFormat()
	require.Equal(t, "1080p29.97", f.Name, "format should be unchanged after rejection")
}

// Ensure fmt.Stringer is satisfied at compile time.
var _ fmt.Stringer = PipelineFormat{}
