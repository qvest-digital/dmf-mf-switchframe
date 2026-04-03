//go:build correctness

package pipeline

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ==========================================================================
// Static analysis guard: detect hardcoded 8-bit frame sizing in
// format-aware code paths.
//
// When code uses video.YUV420FrameSize(w,h) (always returns w*h*3/2) in a path that
// handles both 8-bit and 10-bit frames, it silently truncates 10-bit buffers.
// The correct pattern is colorFormat.FrameSize(w,h) or e.colorFormat.frameSize().
//
// This test scans production source files for dangerous patterns and fails
// if found. It acts as a code review assistant — catching the pattern before
// it reaches production.
// ==========================================================================

// serverDir returns the absolute path to the server directory.
func serverDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile is .../server/correctness/pipeline/format_static_audit_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

// TestStaticAudit_NoHardcoded8bitInFormatAwarePaths scans specific production
// files that must be format-aware and fails if they contain video.YUV420FrameSize() calls
// outside of comments. These files handle both 8-bit and 10-bit frames, so
// using the always-8-bit video.YUV420FrameSize() is a bug.
func TestStaticAudit_NoHardcoded8bitInFormatAwarePaths(t *testing.T) {
	t.Parallel()

	root := serverDir()

	// Files that must NEVER use yuvFrameSize (they handle both formats).
	dangerousFiles := []string{
		"switcher/switcher.go",
		"switcher/source_decoder.go",
		"switcher/frame_sync.go",
		"switcher/pipeline_loop.go",
		"switcher/node_encode.go",
		"switcher/node_raw_sink.go",
		"switcher/node_compositor.go",
		"switcher/node_layout_compositor.go",
		"switcher/node_upstream_key.go",
		"switcher/processing_frame.go",
	}

	for _, relPath := range dangerousFiles {
		absPath := filepath.Join(root, relPath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			// File might not exist yet — skip rather than fail.
			t.Logf("skipping %s: %v", relPath, err)
			continue
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)

			// Skip comments.
			if strings.HasPrefix(trimmed, "//") {
				continue
			}

			// Skip lines where yuvFrameSize appears only in a comment suffix.
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}

			if strings.Contains(line, "video.YUV420FrameSize(") {
				t.Errorf("%s:%d uses video.YUV420FrameSize() which is always 8-bit (w*h*3/2). "+
					"Use colorFormat.FrameSize() or e.colorFormat.frameSize() for format-aware sizing.\n"+
					"  Line: %s",
					relPath, i+1, strings.TrimSpace(lines[i]))
			}
		}
	}
}

// TestStaticAudit_NoHardcodedColorYUV420InFormatAwarePaths scans for direct
// use of ColorYUV420_8bit.FrameSize() in paths that should use the pipeline's
// configured color format. This is subtler than yuvFrameSize -- it uses the
// type-safe API but hardcodes the 8-bit variant.
//
// Note: source_decoder.go is excluded because it legitimately references both
// ColorYUV420_8bit and ColorYUV422_10bit for format detection and
// format-specific scaling branches. Its 8-bit references are in code paths
// guarded by `decodedIs420_8bit` or `outputFormat == ColorYUV420_8bit`.
func TestStaticAudit_NoHardcodedColorYUV420InFormatAwarePaths(t *testing.T) {
	t.Parallel()

	root := serverDir()

	// Files that should use colorFmt.FrameSize() (from pipeline config),
	// NOT ColorYUV420_8bit.FrameSize() specifically.
	// source_decoder.go is excluded — it does format detection and needs both.
	dangerousFiles := []string{
		"switcher/switcher.go",
	}

	for _, relPath := range dangerousFiles {
		absPath := filepath.Join(root, relPath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			t.Logf("skipping %s: %v", relPath, err)
			continue
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)

			// Skip comments.
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}

			if strings.Contains(line, "ColorYUV420_8bit.FrameSize(") {
				t.Errorf("%s:%d hardcodes ColorYUV420_8bit.FrameSize(). "+
					"Use the pipeline's configured color format instead.\n"+
					"  Line: %s",
					relPath, i+1, strings.TrimSpace(lines[i]))
			}
		}
	}
}

// TestStaticAudit_TransitionEngineNoYuvFrameSize verifies the transition engine
// does not use video.YUV420FrameSize() in its production code paths (engine.go).
// The transition engine has its own ColorFormat.frameSize() and must use it.
func TestStaticAudit_TransitionEngineNoYuvFrameSize(t *testing.T) {
	t.Parallel()

	root := serverDir()
	absPath := filepath.Join(root, "transition", "engine.go")

	content, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("cannot read transition/engine.go: %v", err)
	}

	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}

		if strings.Contains(line, "video.YUV420FrameSize(") {
			t.Errorf("transition/engine.go:%d uses video.YUV420FrameSize() which is always 8-bit. "+
				"Use e.colorFormat.frameSize() instead.\n"+
				"  Line: %s",
				i+1, strings.TrimSpace(lines[i]))
		}
	}
}
