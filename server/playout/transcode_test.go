package playout

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNeedsByExtension(t *testing.T) {
	tc := &TranscodeChecker{}

	tests := []struct {
		ext  string
		want bool
	}{
		// Native containers — no transcode needed.
		{".ts", false},
		{".mp4", false},
		{".mov", false},
		{".m4v", false},

		// Transcode-required containers.
		{".mkv", true},
		{".webm", true},
		{".avi", true},
		{".flv", true},
		{".mxf", true},
		{".wmv", true},
		{".mpg", true},
		{".mpeg", true},
		{".ogv", true},

		// Unknown extensions — no transcode.
		{".txt", false},
		{".jpg", false},
		{"", false},

		// Case insensitivity.
		{".MKV", true},
		{".Webm", true},
		{".TS", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			got := tc.NeedsByExtension(tt.ext)
			if got != tt.want {
				t.Errorf("NeedsByExtension(%q) = %v, want %v", tt.ext, got, tt.want)
			}
		})
	}
}

func TestTranscoderConfig(t *testing.T) {
	cfg := TranscoderConfig{
		TargetWidth:   1920,
		TargetHeight:  1080,
		TargetFPS:     30,
		TargetBitrate: 8_000_000,
	}
	tr := NewTranscoder(cfg)
	if tr.config.TargetWidth != 1920 {
		t.Errorf("TargetWidth = %d, want 1920", tr.config.TargetWidth)
	}
	if tr.config.TargetHeight != 1080 {
		t.Errorf("TargetHeight = %d, want 1080", tr.config.TargetHeight)
	}
	if tr.config.TargetFPS != 30 {
		t.Errorf("TargetFPS = %d, want 30", tr.config.TargetFPS)
	}
	if tr.config.TargetBitrate != 8_000_000 {
		t.Errorf("TargetBitrate = %d, want 8000000", tr.config.TargetBitrate)
	}
}

func TestTranscoderOutputPath(t *testing.T) {
	tr := NewTranscoder(TranscoderConfig{})

	tests := []struct {
		input string
		want  string
	}{
		{"/cache/assets/abc123.mkv", "/cache/assets/abc123.ts"},
		{"/cache/assets/video.webm", "/cache/assets/video.ts"},
		{"/cache/assets/clip.avi", "/cache/assets/clip.ts"},
		{"/cache/assets/already.ts", "/cache/assets/already.ts"},
		{"/cache/assets/movie.mp4", "/cache/assets/movie.ts"},
		{"/deep/nested/path/file.mxf", "/deep/nested/path/file.ts"},
	}

	for _, tt := range tests {
		t.Run(filepath.Base(tt.input), func(t *testing.T) {
			got := tr.OutputPath(tt.input)
			if got != tt.want {
				t.Errorf("OutputPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTranscoderCancellation(t *testing.T) {
	tr := NewTranscoder(TranscoderConfig{})
	called := false
	tr.TranscodeFunc = func(ctx context.Context, input, output string, width, height, fps, bitrate int, progress func(float64)) error {
		called = true
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := tr.Transcode(ctx, "/some/file.mkv", nil)
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	if called {
		t.Error("TranscodeFunc should not have been called with cancelled context")
	}
}

func TestTranscoderSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "test.mkv")
	if err := os.WriteFile(inputPath, []byte("fake video data"), 0644); err != nil {
		t.Fatal(err)
	}

	tr := NewTranscoder(TranscoderConfig{
		TargetWidth:   1920,
		TargetHeight:  1080,
		TargetFPS:     30,
		TargetBitrate: 8_000_000,
	})

	var gotInput, gotOutput string
	var gotWidth, gotHeight, gotFPS, gotBitrate int
	tr.TranscodeFunc = func(ctx context.Context, input, output string, width, height, fps, bitrate int, progress func(float64)) error {
		gotInput = input
		gotOutput = output
		gotWidth = width
		gotHeight = height
		gotFPS = fps
		gotBitrate = bitrate
		// Create the output file to simulate real transcode.
		return os.WriteFile(output, []byte("transcoded data"), 0644)
	}

	outputPath, err := tr.Transcode(context.Background(), inputPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedOutput := filepath.Join(tmpDir, "test.ts")
	if outputPath != expectedOutput {
		t.Errorf("outputPath = %q, want %q", outputPath, expectedOutput)
	}
	if gotInput != inputPath {
		t.Errorf("TranscodeFunc input = %q, want %q", gotInput, inputPath)
	}
	if gotOutput != expectedOutput {
		t.Errorf("TranscodeFunc output = %q, want %q", gotOutput, expectedOutput)
	}
	if gotWidth != 1920 {
		t.Errorf("TranscodeFunc width = %d, want 1920", gotWidth)
	}
	if gotHeight != 1080 {
		t.Errorf("TranscodeFunc height = %d, want 1080", gotHeight)
	}
	if gotFPS != 30 {
		t.Errorf("TranscodeFunc fps = %d, want 30", gotFPS)
	}
	if gotBitrate != 8_000_000 {
		t.Errorf("TranscodeFunc bitrate = %d, want 8000000", gotBitrate)
	}
}

func TestTranscoderFailure(t *testing.T) {
	tr := NewTranscoder(TranscoderConfig{})
	tr.TranscodeFunc = func(ctx context.Context, input, output string, width, height, fps, bitrate int, progress func(float64)) error {
		return errors.New("codec exploded")
	}

	_, err := tr.Transcode(context.Background(), "/some/file.mkv", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrTranscodeFailed) {
		t.Errorf("expected ErrTranscodeFailed, got: %v", err)
	}
	// The underlying error message should be included.
	if got := err.Error(); !containsStr(got, "codec exploded") {
		t.Errorf("error should contain underlying message, got: %q", got)
	}
}

func TestTranscoderNilFunc(t *testing.T) {
	tr := NewTranscoder(TranscoderConfig{})
	// TranscodeFunc is nil by default.

	_, err := tr.Transcode(context.Background(), "/some/file.mkv", nil)
	if err == nil {
		t.Fatal("expected error for nil TranscodeFunc, got nil")
	}
	if !errors.Is(err, ErrTranscodeFailed) {
		t.Errorf("expected ErrTranscodeFailed, got: %v", err)
	}
	if got := err.Error(); !containsStr(got, "no transcode implementation available") {
		t.Errorf("error should mention no implementation, got: %q", got)
	}
}

func TestTranscoderProgress(t *testing.T) {
	tr := NewTranscoder(TranscoderConfig{})

	var progressValues []float64
	tr.TranscodeFunc = func(ctx context.Context, input, output string, width, height, fps, bitrate int, progress func(float64)) error {
		if progress != nil {
			progress(0.0)
			progress(0.5)
			progress(1.0)
		}
		return os.WriteFile(output, []byte("data"), 0644)
	}

	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "test.mkv")
	if err := os.WriteFile(inputPath, []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	progressFn := func(p float64) {
		progressValues = append(progressValues, p)
	}

	_, err := tr.Transcode(context.Background(), inputPath, progressFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(progressValues) != 3 {
		t.Fatalf("expected 3 progress callbacks, got %d", len(progressValues))
	}
	if progressValues[0] != 0.0 || progressValues[1] != 0.5 || progressValues[2] != 1.0 {
		t.Errorf("unexpected progress values: %v", progressValues)
	}
}

func TestTranscoderDefaultBitrate(t *testing.T) {
	// When TargetBitrate is 0, the transcoder should pass 0 to let the
	// underlying codec auto-select.
	tr := NewTranscoder(TranscoderConfig{
		TargetWidth:  1920,
		TargetHeight: 1080,
		TargetFPS:    30,
		// TargetBitrate: 0 (auto)
	})

	var gotBitrate int
	tr.TranscodeFunc = func(ctx context.Context, input, output string, width, height, fps, bitrate int, progress func(float64)) error {
		gotBitrate = bitrate
		return os.WriteFile(output, []byte("data"), 0644)
	}

	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "test.mkv")
	if err := os.WriteFile(inputPath, []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := tr.Transcode(context.Background(), inputPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBitrate != 0 {
		t.Errorf("expected bitrate=0 (auto), got %d", gotBitrate)
	}
}

func TestTranscoderCleansUpOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "test.mkv")
	if err := os.WriteFile(inputPath, []byte("fake video"), 0644); err != nil {
		t.Fatal(err)
	}

	tr := NewTranscoder(TranscoderConfig{})
	tr.TranscodeFunc = func(ctx context.Context, input, output string, width, height, fps, bitrate int, progress func(float64)) error {
		// Write partial data to the output file, then fail.
		_ = os.WriteFile(output, []byte("partial"), 0644)
		return errors.New("transcode interrupted")
	}

	_, err := tr.Transcode(context.Background(), inputPath, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Verify the output file was cleaned up.
	outputPath := tr.OutputPath(inputPath)
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Errorf("partial output file should have been removed, stat returned: %v", statErr)
	}
}

// containsStr is a small helper to avoid importing strings in tests.
func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
