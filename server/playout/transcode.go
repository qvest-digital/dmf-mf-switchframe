package playout

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProgressFunc receives transcode progress as a float64 in [0.0, 1.0].
type ProgressFunc func(progress float64)

// transcodeExtensions are formats that always need transcoding to H.264 TS.
// Mirrors clip/validator.go's transcodeExtensions.
var transcodeExtensions = map[string]bool{
	".mkv":  true,
	".webm": true,
	".avi":  true,
	".flv":  true,
	".mxf":  true,
	".wmv":  true,
	".mpg":  true,
	".mpeg": true,
	".ogv":  true,
}

// TranscodeChecker determines if a file needs transcoding based on extension.
type TranscodeChecker struct{}

// NeedsByExtension returns true if the file extension indicates a container
// format that requires transcoding to H.264 MPEG-TS. Native containers
// (.ts, .mp4, .mov, .m4v) are assumed to already contain H.264.
func (tc *TranscodeChecker) NeedsByExtension(ext string) bool {
	return transcodeExtensions[strings.ToLower(ext)]
}

// TranscoderConfig holds target parameters for transcode output.
type TranscoderConfig struct {
	TargetWidth   int // 0 = preserve source
	TargetHeight  int // 0 = preserve source
	TargetFPS     int // 0 = preserve source
	TargetBitrate int // 0 = auto (resolution-based)
}

// Transcoder wraps the codec transcode function for the playout cache pipeline.
type Transcoder struct {
	config TranscoderConfig

	// TranscodeFunc is the actual transcode implementation.
	// In production, wire this to codec.TranscodeFile or similar.
	// Nil means no transcode backend is available.
	TranscodeFunc func(ctx context.Context, input, output string, width, height, fps, bitrate int, progress func(float64)) error
}

// NewTranscoder creates a Transcoder with the given configuration.
// TranscodeFunc must be set separately (e.g., wired to codec.TranscodeFile).
func NewTranscoder(cfg TranscoderConfig) *Transcoder {
	return &Transcoder{
		config: cfg,
	}
}

// OutputPath computes the transcoded output path: same directory, same base
// name, with a .ts extension. For example, /cache/assets/abc123.mkv becomes
// /cache/assets/abc123.ts.
func (t *Transcoder) OutputPath(inputPath string) string {
	dir := filepath.Dir(inputPath)
	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	return filepath.Join(dir, base+".ts")
}

// Transcode normalizes a non-H.264 asset to pipeline-native H.264 MPEG-TS.
// It checks for context cancellation before starting, computes the output path,
// and delegates to TranscodeFunc. Returns the output path on success.
func (t *Transcoder) Transcode(ctx context.Context, inputPath string, progress ProgressFunc) (string, error) {
	// Check context before starting potentially long transcode.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if t.TranscodeFunc == nil {
		return "", fmt.Errorf("%w: no transcode implementation available", ErrTranscodeFailed)
	}

	outputPath := t.OutputPath(inputPath)

	// Adapt ProgressFunc to the raw func(float64) expected by the backend.
	var progressCb func(float64)
	if progress != nil {
		progressCb = func(p float64) {
			progress(p)
		}
	}

	err := t.TranscodeFunc(
		ctx,
		inputPath,
		outputPath,
		t.config.TargetWidth,
		t.config.TargetHeight,
		t.config.TargetFPS,
		t.config.TargetBitrate,
		progressCb,
	)
	if err != nil {
		// Clean up partial output file on failure.
		_ = os.Remove(outputPath)
		return "", fmt.Errorf("%w: %v", ErrTranscodeFailed, err)
	}

	return outputPath, nil
}
