package playout

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// During a transition the blend carries the incoming item's audio, and the
// outgoing item's on a tick the incoming one has none. A clip's AAC frames
// do not fall on every picture - 48 kHz at 60 fps is 0.78 AAC frames a
// picture - so on such ticks the audio went back to the outgoing item and
// then forward again, alternating between two streams; the channel starts
// each stream on its video's PTS, so every switch also moved the audio on
// the timeline. Once the incoming item has supplied audio, the transition
// carries no other.
func TestChannel_TransitionAudioStaysOnTheIncomingItem(t *testing.T) {
	a := fakeFramesWithAudioTag(90, 0xAA)
	b := fakeFramesWithAudioTag(90, 0xBB)
	for i := range b {
		if i%4 == 3 {
			b[i].AudioFrames = nil
		}
	}
	provider := &multiAssetProvider{assets: map[string][]DecodedFrame{
		"local:///clips/a.ts": a,
		"local:///clips/b.ts": b,
	}}
	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"},
		AssetProvider:        provider,
		Width:                64,
		Height:               48,
		FPS:                  30.0,
	})
	ch.SetRawVideoOutput(func([]byte, int, int, int64, bool) {})
	var mu sync.Mutex
	var tags []byte
	ch.SetAudioOutput(func(data []byte, _ int64, _, _ int) {
		mu.Lock()
		tags = append(tags, data[0])
		mu.Unlock()
	})

	itemA := clipItem("a")
	itemB := clipItem("b")
	itemB.Transition = &TransitionConfig{Type: "dissolve", DurationMs: 1000}
	require.NoError(t, ch.LoadPlaylist(testPlaylist(itemA, itemB)))
	ch.mu.Lock()
	ch.state = StateOnAir
	ch.scheduler.Start()
	ch.mu.Unlock()

	now := time.Now()
	var during []byte
	for i := 0; i < 90; i++ {
		if i == 20 {
			// Next mid-clip: the outgoing item still has audio.
			require.NoError(t, ch.Next())
		}
		mu.Lock()
		before := len(tags)
		mu.Unlock()
		ch.tick(now)
		if ch.scheduler.State() == SchedTransitioning {
			mu.Lock()
			during = append(during, tags[before:]...)
			mu.Unlock()
		}
		now = now.Add(33 * time.Millisecond)
	}
	require.NotEmpty(t, during, "the transition never ran")
	seenIn := false
	for i, tag := range during {
		if tag == 0xBB {
			seenIn = true
		} else if seenIn {
			t.Fatalf("transition audio %d is the outgoing item's after the incoming item's: % X", i, during)
		}
	}
	require.True(t, seenIn, "the incoming item's audio never went out during the transition")
}
