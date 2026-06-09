package replay

import (
	"errors"
	"fmt"
	"testing"
)

// fakeClipDecoder simulates a decoder that buffers every fed packet for
// B-frame reordering (Decode always returns the EAGAIN-style "buffering"
// error) and releases the buffered frames only during the SendEOS +
// ReceiveFrame drain, in fed order. Packets whose feed index appears in
// hardErrOn fail with a hard FFmpeg-style decode error and produce no
// output frame, mirroring rc<0 from FFmpegDecoder.Decode.
type fakeClipDecoder struct {
	hardErrOn map[int]bool // 0-based feed index → hard decode error
	feedCount int
	buffered  []int64 // pretend-decoded frames, identified by feed order
	eosSent   bool
}

func (d *fakeClipDecoder) Decode(data []byte) ([]byte, int, int, error) {
	idx := d.feedCount
	d.feedCount++
	if d.hardErrOn[idx] {
		return nil, 0, 0, fmt.Errorf("FFmpeg decode error: code %d", -22)
	}
	d.buffered = append(d.buffered, int64(idx))
	return nil, 0, 0, errors.New("no output frame yet (buffering)")
}

func (d *fakeClipDecoder) SendEOS() error {
	d.eosSent = true
	return nil
}

func (d *fakeClipDecoder) ReceiveFrame() ([]byte, int, int, error) {
	if !d.eosSent || len(d.buffered) == 0 {
		return nil, 0, 0, errors.New("no more frames")
	}
	d.buffered = d.buffered[1:]
	return []byte{0}, 4, 4, nil
}

// avc1Frame builds a minimal AVC1 (4-byte length-prefixed) payload that
// codec.AVC1ToAnnexB converts to a non-empty Annex B buffer.
func avc1Frame() []byte {
	return []byte{0x00, 0x00, 0x00, 0x02, 0x41, 0x9A}
}

// makeTestClip builds n in-memory buffered frames with PTS 100, 200, ...
// The first frame is a keyframe carrying SPS/PPS.
func makeTestClip(n int) []bufferedFrame {
	clip := make([]bufferedFrame, n)
	for i := range clip {
		clip[i] = bufferedFrame{
			wireData: avc1Frame(),
			pts:      int64((i + 1) * 100),
		}
	}
	if n > 0 {
		clip[0].isKeyframe = true
		clip[0].sps = []byte{0x67, 0x42}
		clip[0].pps = []byte{0x68, 0xCE}
	}
	return clip
}

func TestIsDecoderBuffering(t *testing.T) {
	// Error shapes from server/codec/ffmpeg_decoder.go Decode.
	if !isDecoderBuffering(errors.New("no output frame yet (buffering)")) {
		t.Error("EAGAIN-style buffering error not recognized")
	}
	if isDecoderBuffering(fmt.Errorf("FFmpeg decode error: code %d", -22)) {
		t.Error("hard decode error misclassified as buffering")
	}
	if isDecoderBuffering(nil) {
		t.Error("nil error misclassified as buffering")
	}
}

// TestFeedClipToDecoderDrainPTS verifies the EOS-drain PTS mapping with a
// fully buffering decoder: every fed frame comes back during the drain and
// must carry its original PTS in order.
func TestFeedClipToDecoderDrainPTS(t *testing.T) {
	clip := makeTestClip(5)
	dec := &fakeClipDecoder{}

	var gotPTS []int64
	sps, pps, ok := feedClipToDecoder(clip, dec, func(yuv []byte, w, h int, pts int64) bool {
		gotPTS = append(gotPTS, pts)
		return true
	})
	if !ok {
		t.Fatal("feedClipToDecoder aborted unexpectedly")
	}
	if len(sps) == 0 || len(pps) == 0 {
		t.Errorf("first keyframe SPS/PPS not captured: sps=%v pps=%v", sps, pps)
	}

	want := []int64{100, 200, 300, 400, 500}
	if len(gotPTS) != len(want) {
		t.Fatalf("emitted %d frames, want %d (pts=%v)", len(gotPTS), len(want), gotPTS)
	}
	for i := range want {
		if gotPTS[i] != want[i] {
			t.Errorf("drained frame %d: pts=%d, want %d (all: %v)", i, gotPTS[i], want[i], gotPTS)
		}
	}
}

// TestFeedClipToDecoderHardErrorKeepsPTSAligned is a regression test for the
// fedPTS desync: a packet that hard-fails to decode (rc<0, not EAGAIN
// buffering) never produces an output frame, so its PTS must be removed from
// the fed-PTS ledger. Before the fix it was left in place, shifting every
// drained reorder-tail frame's PTS one position early (duplicate /
// non-monotonic timestamps at the clip end).
func TestFeedClipToDecoderHardErrorKeepsPTSAligned(t *testing.T) {
	clip := makeTestClip(5)
	dec := &fakeClipDecoder{hardErrOn: map[int]bool{2: true}} // pts=300 hard-fails

	var gotPTS []int64
	_, _, ok := feedClipToDecoder(clip, dec, func(yuv []byte, w, h int, pts int64) bool {
		gotPTS = append(gotPTS, pts)
		return true
	})
	if !ok {
		t.Fatal("feedClipToDecoder aborted unexpectedly")
	}

	// 4 frames survive; the failed packet's PTS (300) must not appear and
	// the remaining frames must keep their own timestamps, monotonically.
	want := []int64{100, 200, 400, 500}
	if len(gotPTS) != len(want) {
		t.Fatalf("emitted %d frames, want %d (pts=%v)", len(gotPTS), len(want), gotPTS)
	}
	for i := range want {
		if gotPTS[i] != want[i] {
			t.Errorf("drained frame %d: pts=%d, want %d (all: %v)", i, gotPTS[i], want[i], gotPTS)
		}
	}
	for i := 1; i < len(gotPTS); i++ {
		if gotPTS[i] <= gotPTS[i-1] {
			t.Errorf("non-monotonic PTS at index %d: %v", i, gotPTS)
		}
	}
}

// TestFeedClipToDecoderEmitAbort verifies that a failing emit (e.g. GPU pool
// exhaustion) stops the feed and reports ok=false.
func TestFeedClipToDecoderEmitAbort(t *testing.T) {
	clip := makeTestClip(3)
	dec := &fakeClipDecoder{}

	calls := 0
	_, _, ok := feedClipToDecoder(clip, dec, func(yuv []byte, w, h int, pts int64) bool {
		calls++
		return false
	})
	if ok {
		t.Fatal("feedClipToDecoder reported ok=true despite emit failure")
	}
	if calls != 1 {
		t.Errorf("emit called %d times after first failure, want 1", calls)
	}
}
