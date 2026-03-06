package output

import (
	"testing"

	"github.com/zsiec/prism/media"
)

func TestOutputViewerStopIdempotent(t *testing.T) {
	t.Parallel()
	muxer := &TSMuxer{}
	v := NewOutputViewer(muxer, func(_ *media.VideoFrame) {})
	go v.Run()
	v.Stop()
	v.Stop() // must not panic
}
