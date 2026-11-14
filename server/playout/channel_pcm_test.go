//go:build cgo

package playout

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/internal/aacfixture"
)

// fileAssets opens the files it maps asset URIs to.
type fileAssets map[string]string

func (p fileAssets) Open(uri string) (FrameIterator, error) {
	path, ok := p[uri]
	if !ok {
		return nil, fmt.Errorf("asset not found: %s", uri)
	}
	return NewFileFrameIterator(path)
}

// pcmRig runs a 50 fps channel on a scripted clock into a mixer, wired as
// the engine wires it: the channel's PCM placed by its PTS through the
// anchor its video shares, a start placing the mixer channel afresh. Each
// tick reads the program two periods behind the clock.
type pcmRig struct {
	ch     *Channel
	m      *audio.Mixer
	wall   time.Duration
	start  time.Time
	anchor clock.Anchor
	shown  [][]byte // each tick's picture
	pts    []int64  // and its engine PTS
	items  []string // and the item it is of, "" under a transition
	out    []float32
	from   uint64 // sample index of out[0]
}

const pcmKey = "playout:1"

func newPCMRig(t *testing.T, assets fileAssets) *pcmRig {
	t.Helper()
	r := &pcmRig{start: time.Now()}
	tai := clock.TAINow()
	clock.SetTAISource(func() int64 { return tai + int64(r.wall) })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	r.ch = NewChannel(1, ChannelConfig{
		AssetProvider: assets, Width: 64, Height: 48, FPS: 50, AllowedLocalPrefixes: []string{"/"},
	})
	r.m = audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = r.m.Close() })
	r.m.AddChannel(pcmKey)
	r.m.SetActive(pcmKey, true)
	r.ch.SetRawVideoOutput(func(yuv []byte, _, _ int, pts int64, _ bool) {
		r.shown = append(r.shown, yuv)
		r.pts = append(r.pts, r.anchor.PTS(pts))
	})
	r.ch.SetPCMOutput(func(pcm []float32, pts int64, channels int, start bool) {
		r.m.IngestPCM(pcmKey, pcm, r.anchor.PTS(pts), channels)
		if start {
			r.m.PlaceChannel(pcmKey)
		}
	})
	return r
}

// run puts the playlist on air and runs ticks ticks, calling at on each.
func (r *pcmRig) run(t *testing.T, pl *Playlist, ticks int, at func(i int)) {
	t.Helper()
	require.NoError(t, r.ch.LoadPlaylist(pl))
	r.ch.mu.Lock()
	r.ch.state = StateOnAir
	r.ch.scheduler.Start()
	r.ch.mu.Unlock()
	now := func() time.Time { return r.start.Add(r.wall) }
	period := r.ch.frameDuration
	k := tickClock{period: period, next: now().Add(period)}
	rate := clock.Rate{Num: 48000, Den: 1}
	for i := 1; i <= ticks; i++ {
		r.wall = time.Duration(i) * period
		if at != nil {
			at(i)
		}
		if r.ch.runDue(&k, now) {
			return
		}
		item := r.ch.Status().CurrentItemID
		if r.ch.scheduler.State() == SchedTransitioning {
			item = ""
		}
		for len(r.items) < len(r.pts) {
			r.items = append(r.items, item)
		}
		read := clock.TimestampToIndex(rate, clock.TAINow()) - 2*960
		if r.from == 0 {
			r.from = read
		}
		r.out = append(r.out, r.m.ProduceSamples(read, 960, clock.IndexPTS(rate, read))...)
	}
}

// program returns the program audio from the PTS pts on.
func (r *pcmRig) program(pts int64) []float32 {
	idx := clock.TimestampToIndex(clock.Rate{Num: 48000, Den: 1}, clock.TimestampOf(pts))
	return r.out[2*int(idx-r.from):]
}

// firstOf returns the engine PTS of the first picture that shows item
// id, alone or in a transition to it.
func (r *pcmRig) firstOf(t *testing.T, id string) int64 {
	t.Helper()
	for i, item := range r.items {
		if item == id || item == "" {
			return r.pts[i]
		}
	}
	t.Fatalf("item %s never went out", id)
	return 0
}

// clipPictures decodes the pictures of the clip at path.
func clipPictures(t *testing.T, path string) [][]byte {
	t.Helper()
	it, err := NewFileFrameIterator(path)
	require.NoError(t, err)
	defer it.Close()
	var out [][]byte
	for {
		f, err := it.Next()
		require.NoError(t, err)
		if f == nil {
			return out
		}
		out = append(out, f.YUV)
	}
}

// The next item's first sample comes out of the program on the tick that
// shows its first picture, to the sample, after a cut or under a
// dissolve, and whether the item before ran to its end or was cut short.
// The channel handed the mixer each picture's AAC frames as they were
// spread over the pictures, so an item's audio ran past its last
// picture, or stopped short of it, by the difference between the
// lengths of its audio and of its pictures; the mixer read the channel on
// as it had first placed it, so the next item's audio came that much late
// or early, and a picture that carried no AAC frame, as at 50 fps, pushed
// the next item's audio a picture later still.
func TestPlayoutNextItemSoundStartsOnItsFirstPicture(t *testing.T) {
	for _, ext := range []string{".ts", ".mp4"} {
		for _, fps := range []int{25, 50} {
			for _, mode := range []string{"cut", "dissolve", "dissolve mid-item"} {
				t.Run(fmt.Sprintf("%s/%dfps/%s", ext, fps, mode), func(t *testing.T) {
					dir := t.TempDir()
					pa, _ := aacfixture.Write(t, dir, aacfixture.Clip{Ext: ext, Channels: 2, Seconds: 1.5, FPS: fps})
					pb, refB := aacfixture.Write(t, dir, aacfixture.Clip{Ext: ext, Channels: 2, Seconds: 1.5, FPS: fps, Seed: 1})
					a, b := clipItem("a"), clipItem("b")
					a.AssetURI, b.AssetURI = "local:///a"+ext, "local:///b"+ext
					if mode != "cut" {
						b.Transition = &TransitionConfig{Type: "dissolve", DurationMs: 400}
					}
					r := newPCMRig(t, fileAssets{a.AssetURI: pa, b.AssetURI: pb})
					next := func(i int) {
						if mode == "dissolve mid-item" && i == 37 {
							require.NoError(t, r.ch.Next())
						}
					}
					r.run(t, testPlaylist(a, b), 250, next)

					first := r.firstOf(t, "b")
					lag := aacfixture.Lag(r.program(first), refB, 2, 0, 4000, 20000)
					require.InDelta(t, 0, lag, 1, "the item's first sample is %d samples off its first picture", lag)
				})
			}
		}
	}
}

// A looping item plays a pass of its sound with each pass of its
// pictures, every pass from the sound's first sample on the pass's first
// picture, without a gap, an overlap or the encoder priming between them.
func TestPlayoutLoopItemSoundIsGapless(t *testing.T) {
	for _, ext := range []string{".ts", ".mp4"} {
		t.Run(ext, func(t *testing.T) {
			path, ref := aacfixture.Write(t, t.TempDir(), aacfixture.Clip{Ext: ext, Channels: 2, Seconds: 1})
			a := clipItem("a")
			a.AssetURI, a.Loop = "local:///a"+ext, true
			r := newPCMRig(t, fileAssets{a.AssetURI: path})
			r.run(t, testPlaylist(a), 200, nil)

			pics := clipPictures(t, path)
			var starts []int64
			for i, yuv := range r.shown {
				if bytes.Equal(yuv, pics[0]) && (i == 0 || !bytes.Equal(r.shown[i-1], pics[0])) {
					starts = append(starts, r.pts[i])
				}
			}
			require.GreaterOrEqual(t, len(starts), 3, "passes")
			for k, pts := range starts[:3] {
				seg := r.program(pts)[:2*48000]
				lag := aacfixture.Lag(seg, ref, 2, 0, 1500, 15000)
				require.InDelta(t, 0, lag, 1, "pass %d starts %d samples off its first picture", k, lag)
				require.Less(t, aacfixture.RMS(seg, ref, 2, 0, 0), 0.02, "pass %d is not the sound", k)
				require.Equal(t, starts[0]+int64(k)*90000, pts, "pass %d is a second after the first", k)
			}
		})
	}
}
