//go:build cgo

package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/internal/aacfixture"
	"github.com/zsiec/switchframe/server/internal/filepcm"
)

var rate48 = clock.Rate{Num: 48000, Den: 1}

// startRecorder finds the first picture of a start among a source's
// pictures and follows how far its audio reaches.
type startRecorder struct {
	mu     sync.Mutex
	pts    []int64
	pcmEnd atomic.Int64 // PTS the audio handed over so far reaches
}

func (r *startRecorder) video(pts int64) {
	r.mu.Lock()
	r.pts = append(r.pts, pts)
	r.mu.Unlock()
}

func (r *startRecorder) pcm(pcm []float32, pts int64, channels int) {
	r.pcmEnd.Store(pts + int64(len(pcm)/channels)*90000/48000)
}

// forget drops the pictures recorded so far.
func (r *startRecorder) forget() {
	r.mu.Lock()
	r.pts = nil
	r.mu.Unlock()
}

// cued waits for the still a cued player shows, which it repeats only a
// second later, and forgets it.
func (r *startRecorder) cued(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.pts) > 0
	}, 20*time.Second, time.Millisecond)
	r.forget()
}

// firstPicture waits for the first picture of the playback started at
// TAI released on a 50 fps output: the first recorded with a PTS past the
// read point of the tick running then. A cued player's stills are stamped
// with the read point of the tick running when they go out, all before the
// release; the playback's first picture is due on a later tick.
func (r *startRecorder) firstPicture(t *testing.T, released int64) int64 {
	t.Helper()
	r50 := clock.Rate{Num: 50, Den: 1}
	tick := clock.TimestampToIndex(r50, released)
	if clock.IndexToTimestamp(r50, tick) > released {
		tick--
	}
	read := clock.IndexPTS(r50, tick-clock.ReaderDelayGrains)
	var first int64
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, pts := range r.pts {
			if pts > read {
				first = pts
				return true
			}
		}
		return false
	}, 20*time.Second, time.Millisecond)
	return first
}

// readProgram reads the mixer's program from sample index from on, n
// samples, a period at a time once the source has handed over the audio
// the period reads, as the program tick does.
func readProgram(t *testing.T, m *audio.Mixer, rec *startRecorder, from uint64, n int) []float32 {
	t.Helper()
	const period = 960
	var out []float32
	for at := from; at < from+uint64(n); at += period {
		end := clock.IndexPTS(rate48, at+period)
		require.Eventually(t, func() bool { return rec.pcmEnd.Load() >= end }, 20*time.Second, time.Millisecond)
		out = append(out, m.ProduceSamples(at, period, clock.IndexPTS(rate48, at))...)
		m.AdvanceCrossfade()
	}
	return out
}

// newClipAudioRig returns a mixer with the channel of clip player 1, a
// timed manager whose players put out the pictures of path undecoded,
// with their audio wired as the engine wires it, and the recorder.
func newClipAudioRig(t *testing.T, path string) (*audio.Mixer, *clip.Manager, *startRecorder, *clip.Clip) {
	t.Helper()
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2, DecoderFactory: audioDecoderFactory()})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("clip:1")

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)
	c := &clip.Clip{Name: "c", Filename: "c.ts", Source: clip.SourceUpload, Codec: "h264", Width: 64, Height: 48, FPSNum: 25, FPSDen: 1, DurationMs: 2000, ByteSize: 1}
	require.NoError(t, store.Add(c))
	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc:  func(string) ([]clip.BufferedFrame, []clip.BufferedAudioFrame, error) { return clip.DemuxFile(path) },
		OutputRate: func() (int, int) { return 50, 1 },
		Timed:      true,
		PCMCodec:   filepcm.Codec(),
	})
	t.Cleanup(mgr.Close)

	a := &App{mixer: m}
	a.wireClipAudio(mgr)
	rec := &startRecorder{}
	mgr.SetRawVideoOutput(func(_ string, _ []byte, _, _ int, pts int64, _ bool) { rec.video(pts) })
	inner := a.ingestFilePCM
	mgr.SetPCMOutput(func(key string, pcm []float32, pts int64, channels int, start bool) {
		inner(key, pcm, pts, channels, start)
		rec.pcm(pcm, pts, channels)
	})
	return m, mgr, rec, c
}

// The clip's first sample comes out of the program on the tick that
// shows the clip's first picture, to the sample, whether a cut or a mix
// takes the clip to program, and again after the clip is ejected and
// loaded anew. The clip's AAC, decoded by the mixer, came 2768 samples
// late: 1024 of encoder priming played as sound and 1744 of the live
// decoder's own delay. The start does not rest on the cut flushing the
// channel: with the channel only activated, and the audio the previous
// playback handed over ahead of the clock still in it, the start lands
// the same. Started on a program tick, as a cut is, the start hands over
// the PCM it decoded with its cue, and lands the same.
func TestClipSoundStartsOnItsFirstPicture(t *testing.T) {
	path, ref := aacfixture.Write(t, t.TempDir(), aacfixture.Clip{Ext: ".ts", Channels: 2, Seconds: 2})
	for _, mode := range []string{"cut", "mix", "no flush", "tick"} {
		t.Run(mode, func(t *testing.T) {
			m, mgr, rec, c := newClipAudioRig(t, path)
			for load := 0; load < 2; load++ {
				if load > 0 {
					require.NoError(t, mgr.Eject(1))
				}
				rec.forget()
				require.NoError(t, mgr.Load(1, c.ID))
				rec.cued(t)
				switch mode {
				case "cut", "tick":
					m.OnCut("cam", "clip:1")
				case "mix":
					m.OnTransitionStart("cam", "clip:1", audio.Crossfade, 1000)
					m.OnTransitionPosition(0.5)
				default:
					m.SetActive("clip:1", true)
				}
				released := clock.TAINow()
				if mode == "tick" {
					// The tick after the one running.
					r50 := clock.Rate{Num: 50, Den: 1}
					mgr.StartCuedAt(1, clock.IndexPTS(r50, clock.TimestampToIndex(r50, released)+1-clock.ReaderDelayGrains))
				} else {
					require.True(t, mgr.StartCued(1))
				}
				first := rec.firstPicture(t, released)
				idx := clock.TimestampToIndex(rate48, clock.TimestampOf(first))
				const lead = 1920
				out := readProgram(t, m, rec, idx-lead, 30000)
				lag := aacfixture.Lag(out, ref, 2, 0, 6000, 20000)
				require.InDelta(t, lead, lag, 1, "load %d: the clip's first sample is %d samples off its first picture", load, lag-lead)
				require.NoError(t, mgr.Stop(1))
				if mode == "mix" {
					m.OnTransitionComplete()
				}
			}
		})
	}
}

// A channel's audio is read on as it was placed when it first arrived,
// and audio that moved by less than a period stays where the channel was
// placed. A clip started again before the audio it played last has run
// out, as a start from its cue does, has its new audio stamped before
// where that old audio ends: read on, it comes up to a period late. The
// first PCM of a start places the channel afresh.
func TestClipRestartPlacesItsSoundAfresh(t *testing.T) {
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	defer func() { _ = m.Close() }()
	const key = "clip:1"
	m.AddChannel(key)
	m.SetActive(key, true)
	a := &App{mixer: m}

	const period = 960
	base := clock.TimestampToIndex(rate48, clock.TAINow())
	block := func(v float32, n int) []float32 {
		pcm := make([]float32, 2*n)
		for i := range pcm {
			pcm[i] = v
		}
		return pcm
	}
	// The first playback, 3 periods of it handed over, 1 read.
	a.ingestFilePCM(key, block(0.25, 3*period), clock.IndexPTS(rate48, base), 2, true)
	m.ProduceSamples(base, period, 0)
	// The restart lands 500 samples before the old audio ends.
	restart := base + 3*period - 500
	a.ingestFilePCM(key, block(0.5, 4*period), clock.IndexPTS(rate48, restart), 2, true)

	var out []float32
	for at := base + period; at < base+6*period; at += period {
		out = append(out, m.ProduceSamples(at, period, 0)...)
	}
	first := -1
	for i := 0; i < len(out)/2; i++ {
		if out[2*i] > 0.4 {
			first = i
			break
		}
	}
	require.GreaterOrEqual(t, first, 0, "the restart's audio never played")
	got := int(base) + period + first - int(restart)
	require.InDelta(t, 0, got, 1, "the restart's first sample is %d samples off its stamp", got)
}
