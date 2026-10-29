package main

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/preview"
)

// playoutRelayFeed encodes a playout channel's pictures for its browser
// relay when no GPU preview encoder does. The relay feeds only the
// channel's tile and the preview monitor, so it is encoded at the preview
// size and bitrate, as the GPU preview proxy does. Without it the relay of
// a playout channel on an engine without one carries no video at all.
type playoutRelayFeed struct {
	w, h          int
	bitrate       int
	frameInterval int

	encs [playout.MaxChannels]atomic.Pointer[preview.Encoder]
	// Copies of pictures on their way to an encoder, reused once encoded.
	bufs sync.Pool
}

func newPlayoutRelayFeed(w, h, bitrate, frameInterval int) *playoutRelayFeed {
	return &playoutRelayFeed{w: w, h: h, bitrate: bitrate, frameInterval: frameInterval}
}

// start begins encoding channel idx's pictures onto relay at the pipeline
// rate the channel starts with, replacing an encoder a previous run of the
// channel left behind.
func (f *playoutRelayFeed) start(idx int, key string, relay preview.Relay, fpsNum, fpsDen int) {
	if idx < 0 || idx >= len(f.encs) {
		return
	}
	enc, err := preview.NewEncoder(preview.Config{
		SourceKey:     key,
		Width:         f.w,
		Height:        f.h,
		Bitrate:       f.bitrate,
		FPSNum:        fpsNum,
		FPSDen:        fpsDen,
		Relay:         relay,
		FrameInterval: f.frameInterval,
		ScaleDown:     true,
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

// frame hands one picture of channel idx to its encoder: 8-bit 4:2:0, or
// 10-bit 4:2:2 as a 10-bit pipeline's idle black is. The channel's output
// goroutine only copies it, into a reused buffer; the encoder converts and
// scales it on its own goroutine and drops the oldest queued picture when
// it falls behind.
func (f *playoutRelayFeed) frame(idx int, yuv []byte, w, h int, pts int64) {
	if idx < 0 || idx >= len(f.encs) {
		return
	}
	enc := f.encs[idx].Load()
	if enc == nil || (len(yuv) != w*h*3/2 && len(yuv) != w*h*4) {
		return
	}
	bp, _ := f.bufs.Get().(*[]byte)
	if bp == nil {
		bp = new([]byte)
	}
	if cap(*bp) < len(yuv) {
		*bp = make([]byte, len(yuv))
	}
	*bp = (*bp)[:len(yuv)]
	copy(*bp, yuv)
	enc.SendOwned(*bp, w, h, pts, func([]byte) { f.bufs.Put(bp) })
}

// playoutChannelIDFromKey returns N for "playout:N", 0 for anything else.
func playoutChannelIDFromKey(key string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(key, "playout:"))
	if err != nil || !strings.HasPrefix(key, "playout:") {
		return 0
	}
	return n
}
