//go:build cgo && !noffmpeg

package main

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/playout"
)

type recordingRelay struct {
	mu     sync.Mutex
	video  []*media.VideoFrame
	info   []distribution.VideoInfo
	audios int
}

func (r *recordingRelay) BroadcastVideo(f *media.VideoFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.video = append(r.video, f)
}

func (r *recordingRelay) BroadcastAudio(*media.AudioFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audios++
}

func (r *recordingRelay) SetVideoInfo(i distribution.VideoInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.info = append(r.info, i)
}

func (r *recordingRelay) counts() (int, []distribution.VideoInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.video), append([]distribution.VideoInfo(nil), r.info...)
}

func grey420(w, h int) []byte {
	b := make([]byte, w*h*3/2)
	for i := range b {
		b[i] = 128
	}
	return b
}

// A playout channel's pictures reach its browser relay as H.264 at the
// preview size, starting with a keyframe the browser can configure a
// decoder from. Without the feed nothing writes video to a playout relay
// on an engine without a GPU preview encoder, and the channel's tile and
// the preview monitor stay black.
func TestPlayoutRelayFeed_EncodesChannelPicturesAtPreviewSize(t *testing.T) {
	feed := newPlayoutRelayFeed(640, 360, 500000, 1)
	relay := &recordingRelay{}
	feed.start(0, "playout:1", relay, 50, 1)

	pic := grey420(1920, 1080)
	deadline := time.Now().Add(10 * time.Second)
	for n := 0; ; n++ {
		feed.frame(0, pic, 1920, 1080, int64(n)*1800)
		if got, _ := relay.counts(); got >= 5 {
			break
		}
		require.True(t, time.Now().Before(deadline), "no encoded pictures on the relay")
		time.Sleep(20 * time.Millisecond)
	}
	feed.stop(0)

	_, info := relay.counts()
	require.NotEmpty(t, info)
	require.Equal(t, 640, info[0].Width)
	require.Equal(t, 360, info[0].Height)
	relay.mu.Lock()
	first := relay.video[0]
	relay.mu.Unlock()
	require.True(t, first.IsKeyframe)
	require.NotZero(t, first.GroupID, "group 0 is the relay's not-damaged sentinel")

	// A stopped channel feeds nothing more.
	before, _ := relay.counts()
	for n := 0; n < 10; n++ {
		feed.frame(0, pic, 1920, 1080, 0)
	}
	time.Sleep(100 * time.Millisecond)
	after, _ := relay.counts()
	require.Equal(t, before, after)
}

// Pictures of a channel without an encoder, out of range, or not 8-bit
// 4:2:0 at their stated size are dropped, never scaled as if they were.
func TestPlayoutRelayFeed_DropsWhatItCannotEncode(t *testing.T) {
	feed := newPlayoutRelayFeed(640, 360, 500000, 1)
	relay := &recordingRelay{}
	feed.frame(0, grey420(1920, 1080), 1920, 1080, 0)
	feed.frame(-1, grey420(1920, 1080), 1920, 1080, 0)
	feed.frame(99, grey420(1920, 1080), 1920, 1080, 0)

	feed.start(1, "playout:2", relay, 50, 1)
	defer feed.stop(1)
	for n := 0; n < 10; n++ {
		feed.frame(1, make([]byte, 1920*1080*2), 1920, 1080, 0)
	}
	time.Sleep(200 * time.Millisecond)
	got, _ := relay.counts()
	require.Zero(t, got)
}

func TestPlayoutChannelIDFromKey(t *testing.T) {
	require.Equal(t, 1, playoutChannelIDFromKey("playout:1"))
	require.Equal(t, 2, playoutChannelIDFromKey("playout:2"))
	require.Equal(t, 0, playoutChannelIDFromKey("clip:1"))
	require.Equal(t, 0, playoutChannelIDFromKey("1"))
	require.Equal(t, 0, playoutChannelIDFromKey("playout:x"))
}

// On a 10-bit pipeline an idling channel puts out 10-bit 4:2:2 black. It
// reaches the relay as black at the preview size, rather than being
// dropped and leaving the tile frozen on the last picture.
func TestPlayoutRelayFeed_EncodesTenBitIdleBlack(t *testing.T) {
	feed := newPlayoutRelayFeed(640, 360, 500000, 1)
	relay := &recordingRelay{}
	feed.start(0, "playout:1", relay, 50, 1)
	defer feed.stop(0)

	black := playout.NewIdleFrame10(1920, 1080)
	deadline := time.Now().Add(10 * time.Second)
	for n := 0; ; n++ {
		feed.frame(0, black, 1920, 1080, int64(n)*1800)
		if got, _ := relay.counts(); got >= 3 {
			break
		}
		require.True(t, time.Now().Before(deadline), "10-bit pictures never reached the relay")
		time.Sleep(20 * time.Millisecond)
	}
	_, info := relay.counts()
	require.NotEmpty(t, info)
	require.Equal(t, 640, info[0].Width)
	require.Equal(t, 360, info[0].Height)
}

// The channel's output goroutine hands a picture over without scaling it.
func BenchmarkPlayoutRelayFeed_Frame1080p(b *testing.B) {
	feed := newPlayoutRelayFeed(852, 480, 500000, 1)
	feed.start(0, "playout:1", &recordingRelay{}, 50, 1)
	defer feed.stop(0)
	pic := grey420(1920, 1080)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		feed.frame(0, pic, 1920, 1080, int64(n))
	}
}
