package switcher

import (
	"bytes"
	"testing"

	"github.com/zsiec/prism/media"
)

// A repeat sends an earlier frame's bitstream again under a new PTS. A
// keyframe decodes on its own, so its copy shows the same picture again;
// a copy of a predicted frame applies its slice to the picture a second
// time and corrupts every frame up to the next keyframe.
func TestRepeatNeverResendsAPredictedFrame(t *testing.T) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()
	viewer := newMockProgramViewer("repeat-p")
	s.programRelay.AddViewer(viewer)

	pSlice := []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a, 0x02}
	s.lastBroadcastVFrame.Store(&media.VideoFrame{PTS: 1000, DTS: 1000, WireData: pSlice})
	s.repeatLastVideoFrame(4000)
	for _, f := range viewer.VideoFrames() {
		if !f.IsKeyframe && bytes.Equal(f.WireData, pSlice) {
			t.Fatalf("repeat at PTS %d re-sent the predicted frame of PTS 1000", f.PTS)
		}
	}

	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84}
	s.lastBroadcastVFrame.Store(&media.VideoFrame{PTS: 7000, DTS: 7000, WireData: idr, IsKeyframe: true})
	s.repeatLastVideoFrame(10000)
	frames := viewer.VideoFrames()
	if n := len(frames); n == 0 || frames[n-1].PTS != 10000 || !bytes.Equal(frames[n-1].WireData, idr) {
		t.Fatal("the last keyframe was not repeated at the new PTS")
	}
}
