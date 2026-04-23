package output

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/media"
)

type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// captureSlog redirects the process-global slog default to an in-memory buffer.
// Callers must NOT use t.Parallel(): slog.SetDefault is global and the cleanup
// would race with sibling parallel tests inheriting the override.
func captureSlog(t *testing.T, level slog.Level) *syncBuf {
	t.Helper()
	prev := slog.Default()
	buf := &syncBuf{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func countWarnLines(s, substr string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "level=WARN") && strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

func TestOutputViewerStopIdempotent(t *testing.T) {
	t.Parallel()
	muxer := &TSMuxer{}
	v := NewViewer(muxer, func(_ *media.VideoFrame) {})
	go v.Run()
	v.Stop()
	v.Stop() // must not panic
}

func TestOutputViewerVideoPriority(t *testing.T) {
	t.Parallel()

	var videoMuxed atomic.Int64
	var audioMuxed atomic.Int64

	muxer := NewTSMuxer()
	muxer.SetOutput(func(_ []byte) {})

	v := NewViewer(muxer, func(_ *media.VideoFrame) {
		videoMuxed.Add(1)
	})

	// Fill the audio channel to near capacity before starting.
	for i := 0; i < audioChSize-1; i++ {
		v.SendAudio(&media.AudioFrame{
			PTS:        int64(i * 1000),
			Data:       []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x80, 0xFC, 0xAA, 0xBB},
			SampleRate: 48000,
			Channels:   2,
		})
	}

	// Send one video keyframe (needed to initialize the muxer).
	v.SendVideo(&media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		SPS:        []byte{0x67, 0x64, 0x00, 0x28},
		PPS:        []byte{0x68, 0xEE, 0x3C, 0x80},
	})

	go v.Run()

	// The video frame should be processed promptly despite the audio backlog.
	require.Eventually(t, func() bool {
		return videoMuxed.Load() >= 1
	}, 2*time.Second, 5*time.Millisecond,
		"video frame should be processed with priority over queued audio")

	v.Stop()

	require.GreaterOrEqual(t, videoMuxed.Load(), int64(1),
		"video frame must have been muxed")
	t.Logf("video muxed: %d, audio muxed: %d", videoMuxed.Load(), audioMuxed.Load())
}

func TestViewer_DropLogRateLimited(t *testing.T) {
	buf := captureSlog(t, slog.LevelDebug)

	v := NewViewer(&TSMuxer{}, nil)
	// Deliberately do NOT start Run(): channels will never drain.

	// Fill each channel to capacity.
	for i := 0; i < videoChSize; i++ {
		v.SendVideo(&media.VideoFrame{PTS: int64(i)})
	}
	for i := 0; i < audioChSize; i++ {
		v.SendAudio(&media.AudioFrame{PTS: int64(i)})
	}
	for i := 0; i < captionChSize; i++ {
		v.SendCaptions(&ccx.CaptionFrame{PTS: int64(i)})
	}

	// Reset drop counters so the burst-only delta is observable.
	v.videoDropped.Store(0)
	v.audioDropped.Store(0)
	v.captionDropped.Store(0)

	const burst = 1000
	for i := 0; i < burst; i++ {
		v.SendVideo(&media.VideoFrame{PTS: int64(i + videoChSize)})
		v.SendAudio(&media.AudioFrame{PTS: int64(i + audioChSize)})
		v.SendCaptions(&ccx.CaptionFrame{PTS: int64(i + captionChSize)})
	}

	// Each channel's drop counter must reflect every burst frame (unchanged behavior).
	require.Equal(t, int64(burst), v.videoDropped.Load(), "video drop counter must keep counting")
	require.Equal(t, int64(burst), v.audioDropped.Load(), "audio drop counter must keep counting")
	require.Equal(t, int64(burst), v.captionDropped.Load(), "caption drop counter must keep counting")

	// During a sub-second burst, each channel should log at most 2 warn lines
	// (first drop fires; subsequent drops within the 1s window are suppressed).
	out := buf.String()
	vWarns := countWarnLines(out, "dropped video frame")
	aWarns := countWarnLines(out, "dropped audio frame")
	cWarns := countWarnLines(out, "dropped caption frame")
	require.LessOrEqualf(t, vWarns, 2, "video warn lines (%d) exceed rate limit\n%s", vWarns, out)
	require.LessOrEqualf(t, aWarns, 2, "audio warn lines (%d) exceed rate limit\n%s", aWarns, out)
	require.LessOrEqualf(t, cWarns, 2, "caption warn lines (%d) exceed rate limit\n%s", cWarns, out)

	// Lower bound: each channel must have emitted at least one warn, else the
	// rate-limit gate is inverted and every drop is being silently suppressed.
	require.GreaterOrEqualf(t, vWarns, 1, "no video warn lines — gate inverted?\n%s", out)
	require.GreaterOrEqualf(t, aWarns, 1, "no audio warn lines — gate inverted?\n%s", out)
	require.GreaterOrEqualf(t, cWarns, 1, "no caption warn lines — gate inverted?\n%s", out)
}

func TestViewer_DropLogPerChannelIndependent(t *testing.T) {
	buf := captureSlog(t, slog.LevelDebug)

	v := NewViewer(&TSMuxer{}, nil)

	// Fill video channel and trigger exactly one drop — video rate-limiter starts running.
	for i := 0; i < videoChSize; i++ {
		v.SendVideo(&media.VideoFrame{PTS: int64(i)})
	}
	v.SendVideo(&media.VideoFrame{PTS: 9001}) // first drop -> logs (video)

	// Fill audio channel and trigger exactly one drop — audio must log even though
	// the video rate-limiter just fired: each channel owns its own lastDropLog.
	for i := 0; i < audioChSize; i++ {
		v.SendAudio(&media.AudioFrame{PTS: int64(i)})
	}
	v.SendAudio(&media.AudioFrame{PTS: 9001}) // first drop -> logs (audio)

	// Same for captions.
	for i := 0; i < captionChSize; i++ {
		v.SendCaptions(&ccx.CaptionFrame{PTS: int64(i)})
	}
	v.SendCaptions(&ccx.CaptionFrame{PTS: 9001}) // first drop -> logs (caption)

	out := buf.String()
	require.GreaterOrEqualf(t, countWarnLines(out, "dropped video frame"), 1, "video drop not logged\n%s", out)
	require.GreaterOrEqualf(t, countWarnLines(out, "dropped audio frame"), 1, "audio drop not logged (per-channel gate broken?)\n%s", out)
	require.GreaterOrEqualf(t, countWarnLines(out, "dropped caption frame"), 1, "caption drop not logged (per-channel gate broken?)\n%s", out)
}

func TestViewer_DropLogIncludesAccumulatedCount(t *testing.T) {
	buf := captureSlog(t, slog.LevelDebug)

	v := NewViewer(&TSMuxer{}, nil)

	for i := 0; i < videoChSize; i++ {
		v.SendVideo(&media.VideoFrame{PTS: int64(i)})
	}
	for i := 0; i < 5; i++ {
		v.SendVideo(&media.VideoFrame{PTS: int64(9000 + i)})
	}

	require.Contains(t, buf.String(), "dropped=",
		"drop warn must include accumulated dropped count (like async_adapter)")
}
