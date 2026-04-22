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
		preset  string
		wantNs  int64
		deltaNs int64
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
	sw := newTestSwitcher(t, programRelay)
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
	// The test switcher uses a "test" format (320x240) matching its pool.
	require.NotEqual(t, "1080p25", f.Name, "format should be unchanged after rejection")
}

func TestPipelineColorFormat_FrameSize(t *testing.T) {
	tests := []struct {
		name   string
		format PipelineColorFormat
		w, h   int
		want   int
	}{
		{"420_8bit_1080p", ColorYUV420_8bit, 1920, 1080, 1920 * 1080 * 3 / 2},
		{"422_10bit_1080p", ColorYUV422_10bit, 1920, 1080, 1920 * 1080 * 4},
		{"420_8bit_720p", ColorYUV420_8bit, 1280, 720, 1280 * 720 * 3 / 2},
		{"422_10bit_720p", ColorYUV422_10bit, 1280, 720, 1280 * 720 * 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.format.FrameSize(tt.w, tt.h)
			if got != tt.want {
				t.Errorf("FrameSize(%d, %d) = %d, want %d", tt.w, tt.h, got, tt.want)
			}
		})
	}
}

func TestPipelineColorFormat_ChromaDimensions(t *testing.T) {
	if ColorYUV420_8bit.ChromaWidth(1920) != 960 {
		t.Fatal("420 chroma width")
	}
	if ColorYUV420_8bit.ChromaHeight(1080) != 540 {
		t.Fatal("420 chroma height")
	}
	if ColorYUV422_10bit.ChromaWidth(1920) != 960 {
		t.Fatal("422 chroma width")
	}
	if ColorYUV422_10bit.ChromaHeight(1080) != 1080 {
		t.Fatal("422 chroma height")
	}
}

func TestPipelineColorFormat_BytesPerSample(t *testing.T) {
	if ColorYUV420_8bit.BytesPerSample() != 1 {
		t.Fatal("8-bit should be 1 byte")
	}
	if ColorYUV422_10bit.BytesPerSample() != 2 {
		t.Fatal("10-bit should be 2 bytes")
	}
}

func TestPipelineColorConfig_Validate(t *testing.T) {
	cfg := PipelineColorConfig{ColorFormat: ColorYUV420_8bit, Codec: CodecAVC}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg = PipelineColorConfig{ColorFormat: ColorYUV420_8bit, Codec: CodecHEVC}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg = PipelineColorConfig{ColorFormat: ColorYUV422_10bit, Codec: CodecHEVC}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg = PipelineColorConfig{ColorFormat: ColorYUV422_10bit, Codec: CodecAVC}
	if err := cfg.Validate(); err == nil {
		t.Fatal("AVC + 10-bit should fail")
	}
}

func TestColorProfilePresets(t *testing.T) {
	std := ColorProfileStandard()
	if std.ColorFormat != ColorYUV420_8bit || std.Codec != CodecAVC {
		t.Fatal("standard profile mismatch")
	}
	hevc := ColorProfileStandardHEVC()
	if hevc.ColorFormat != ColorYUV420_8bit || hevc.Codec != CodecHEVC {
		t.Fatal("standard-hevc profile mismatch")
	}
	pro := ColorProfileProfessional()
	if pro.ColorFormat != ColorYUV422_10bit || pro.Codec != CodecHEVC {
		t.Fatal("professional profile mismatch")
	}
}

func TestPipelineColorFormat_String(t *testing.T) {
	if ColorYUV420_8bit.String() != "yuv420p" {
		t.Fatal("420 string")
	}
	if ColorYUV422_10bit.String() != "yuv422p10le" {
		t.Fatal("422 string")
	}
}

func TestPipelineCodec_String(t *testing.T) {
	if CodecAVC.String() != "h264" {
		t.Fatal("avc string")
	}
	if CodecHEVC.String() != "h265" {
		t.Fatal("hevc string")
	}
}

func TestPipelineColorFormat_BlackLevel(t *testing.T) {
	if ColorYUV420_8bit.BlackY() != 16 {
		t.Fatal("8-bit black Y")
	}
	if ColorYUV420_8bit.NeutralChroma() != 128 {
		t.Fatal("8-bit neutral chroma")
	}
	if ColorYUV422_10bit.BlackY() != 64 {
		t.Fatal("10-bit black Y")
	}
	if ColorYUV422_10bit.NeutralChroma() != 512 {
		t.Fatal("10-bit neutral chroma")
	}
}

func TestParseColorProfile(t *testing.T) {
	cfg, err := ParseColorProfile("standard")
	if err != nil || cfg.ColorFormat != ColorYUV420_8bit || cfg.Codec != CodecAVC {
		t.Fatal("standard parse failed")
	}
	cfg, err = ParseColorProfile("standard-hevc")
	if err != nil || cfg.Codec != CodecHEVC {
		t.Fatal("standard-hevc parse failed")
	}
	cfg, err = ParseColorProfile("professional")
	if err != nil || cfg.ColorFormat != ColorYUV422_10bit {
		t.Fatal("professional parse failed")
	}
	_, err = ParseColorProfile("invalid")
	if err == nil {
		t.Fatal("should reject invalid profile")
	}
}

func TestPipelineColorFormat_PlaneSize(t *testing.T) {
	// 420 8-bit: Y = 1920*1080 = 2073600, chroma = 960*540 = 518400
	if ColorYUV420_8bit.YPlaneSize(1920, 1080) != 2073600 {
		t.Fatal("420 Y plane size")
	}
	if ColorYUV420_8bit.ChromaPlaneSize(1920, 1080) != 518400 {
		t.Fatal("420 chroma plane size")
	}
	// 422 10-bit: Y = 1920*1080*2 = 4147200, chroma = 960*1080*2 = 2073600
	if ColorYUV422_10bit.YPlaneSize(1920, 1080) != 4147200 {
		t.Fatal("422 Y plane size")
	}
	if ColorYUV422_10bit.ChromaPlaneSize(1920, 1080) != 2073600 {
		t.Fatal("422 chroma plane size")
	}
}

func TestPipelineColorFormat_MaxSampleValue(t *testing.T) {
	if ColorYUV420_8bit.MaxSampleValue() != 255 {
		t.Fatal("8-bit max")
	}
	if ColorYUV422_10bit.MaxSampleValue() != 1023 {
		t.Fatal("10-bit max")
	}
}

// Ensure fmt.Stringer is satisfied at compile time.
var _ fmt.Stringer = PipelineFormat{}
var _ fmt.Stringer = ColorYUV420_8bit
var _ fmt.Stringer = CodecAVC
