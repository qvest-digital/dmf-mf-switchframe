package main

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/preview"
	"github.com/zsiec/switchframe/server/transition"
)

// playoutRelayFeed encodes a playout channel's pictures for its browser
// relay when no GPU preview encoder does. The relay feeds only the
// channel's tile and the preview monitor, so it is encoded at the preview
// size and bitrate, as the GPU preview proxy does. Without it the relay of
// a playout channel on an engine without one carries no video at all.
type playoutRelayFeed struct {
	w, h          int
	bitrate       int
	fpsNum        int
	fpsDen        int
	frameInterval int

	encs [playout.MaxChannels]atomic.Pointer[preview.Encoder]
	bufs sync.Pool
}

func newPlayoutRelayFeed(w, h, bitrate, fpsNum, fpsDen, frameInterval int) *playoutRelayFeed {
	f := &playoutRelayFeed{w: w, h: h, bitrate: bitrate, fpsNum: fpsNum, fpsDen: fpsDen, frameInterval: frameInterval}
	size := w * h * 3 / 2
	f.bufs.New = func() any { b := make([]byte, size); return &b }
	return f
}

// start begins encoding channel idx's pictures onto relay, replacing an
// encoder a previous run of the channel left behind.
func (f *playoutRelayFeed) start(idx int, key string, relay preview.Relay) {
	if idx < 0 || idx >= len(f.encs) {
		return
	}
	enc, err := preview.NewEncoder(preview.Config{
		SourceKey:     key,
		Width:         f.w,
		Height:        f.h,
		Bitrate:       f.bitrate,
		FPSNum:        f.fpsNum,
		FPSDen:        f.fpsDen,
		Relay:         relay,
		FrameInterval: f.frameInterval,
	})
	if err != nil {
		slog.Error("playout: relay encoder failed", "key", key, "error", err)
		return
	}
	if old := f.encs[idx].Swap(enc); old != nil {
		old.Stop()
	}
	slog.Info("playout: CPU relay encoder registered", "key", key, "width", f.w, "height", f.h)
}

// stop ends encoding for channel idx.
func (f *playoutRelayFeed) stop(idx int) {
	if idx < 0 || idx >= len(f.encs) {
		return
	}
	if old := f.encs[idx].Swap(nil); old != nil {
		old.Stop()
	}
}

// frame hands one 8-bit 4:2:0 picture of channel idx to its encoder,
// scaled to the preview size. The encoder drops the oldest queued picture
// when it falls behind, so the channel's output goroutine never waits.
func (f *playoutRelayFeed) frame(idx int, yuv []byte, w, h int, pts int64) {
	if idx < 0 || idx >= len(f.encs) {
		return
	}
	enc := f.encs[idx].Load()
	if enc == nil || len(yuv) != w*h*3/2 {
		return
	}
	bp := f.bufs.Get().(*[]byte)
	transition.ScaleYUV420(yuv, w, h, *bp, f.w, f.h)
	enc.SendOwned(*bp, f.w, f.h, pts, func([]byte) { f.bufs.Put(bp) })
}

// playoutChannelIDFromKey returns N for "playout:N", 0 for anything else.
func playoutChannelIDFromKey(key string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(key, "playout:"))
	if err != nil || !strings.HasPrefix(key, "playout:") {
		return 0
	}
	return n
}
