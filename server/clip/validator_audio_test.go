//go:build cgo && !noffmpeg

package clip

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The MP4 demuxer hands any sound track on as AAC frames and Validate
// reports it as AAC, so an H.264 upload with AC-3 or MP3 audio, or AAC
// in a layout ADTS framing cannot carry, was stored as it came and
// played silent: every frame failed in the AAC decoder. Such audio is
// transcoded at upload, like a video codec the engine does not take.
func TestNeedsTranscode_AudioTheEngineCannotDecode(t *testing.T) {
	for file, want := range map[string]bool{
		"testdata/h264-aac-stereo.mp4": false,
		"testdata/h264-ac3.mp4":        true,
		"testdata/h264-mp3.mp4":        true,
		"testdata/h264-aac-6.1.mp4":    true,
		// The demuxer plays the first sound track.
		"testdata/h264-ac3-then-aac.mp4": true,
	} {
		assert.Equal(t, want, NeedsTranscode(file), file)
	}
}
