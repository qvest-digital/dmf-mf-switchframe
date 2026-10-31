//go:build cgo

package main

import (
	"sync"
	"testing"
	"time"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
)

type aacRecorder struct {
	mu     sync.Mutex
	frames []*media.AudioFrame
}

func (r *aacRecorder) ID() string                     { return "aac-recorder" }
func (r *aacRecorder) SendVideo(*media.VideoFrame)    {}
func (r *aacRecorder) SendCaptions(*ccx.CaptionFrame) {}
func (r *aacRecorder) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{}
}
func (r *aacRecorder) SendAudio(f *media.AudioFrame) {
	r.mu.Lock()
	r.frames = append(r.frames, f)
	r.mu.Unlock()
}

const impulseLen = 64

// impulseMixer produces silence with an impulse at sample index at, and
// records the program PTS it was produced at.
type impulseMixer struct {
	mu  sync.Mutex
	at  uint64
	pts int64
	buf []float32
}

func (m *impulseMixer) ProduceSamples(start uint64, n int, pts int64) []float32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buf = append(m.buf[:0], make([]float32, 2*n)...)
	for i := 0; i < n; i++ {
		if s := start + uint64(i); s >= m.at && s < m.at+impulseLen {
			m.buf[2*i], m.buf[2*i+1] = 0.8, 0.8
			if s == m.at {
				m.pts = pts + int64(i)*90000/48000
			}
		}
	}
	return m.buf
}

func (m *impulseMixer) AdvanceCrossfade() {}

// firstLoud returns the index of the first stereo frame of pcm above 0.3.
func firstLoud(pcm []float32) int {
	for i := 0; i < len(pcm)/2; i++ {
		if pcm[2*i] > 0.3 || pcm[2*i] < -0.3 {
			return i
		}
	}
	return -1
}

// codecDelay is how many samples an impulse comes out of an FDK encoder
// and this decoder later than it went in, decoded frame by frame.
func codecDelay(t *testing.T) int {
	enc, err := audio.NewFDKEncoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enc.Close() }()
	dec, err := audio.NewFDKDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dec.Close() }()
	const at = 5000
	var out []float32
	for k := 0; k < 16; k++ {
		pcm := make([]float32, 2048)
		for i := 0; i < 1024; i++ {
			if s := k*1024 + i; s >= at && s < at+impulseLen {
				pcm[2*i], pcm[2*i+1] = 0.8, 0.8
			}
		}
		aac, err := enc.Encode(pcm)
		if err != nil {
			t.Fatal(err)
		}
		d, err := dec.Decode(aac)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, d...)
	}
	return firstLoud(out) - at
}

// Every program AAC frame is stamped with the PTS of the first sample it
// carries. FDK-AAC's frames lag the PCM they are fed by the encoder delay,
// so a frame stamped with the PTS of the PCM it was fed plays the program
// audio that much late against the video. Decoded, an impulse must come
// out at its program PTS plus only the delay the decoder adds itself.
func TestProgramAACFramesCompensateEncoderDelay(t *testing.T) {
	enc, err := audio.NewFDKEncoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	encDelay := enc.Delay()
	_ = enc.Close()
	decoderDelay := codecDelay(t) - encDelay

	a := &App{programRelay: distribution.NewRelay()}
	a.cfg.Format, a.cfg.ColorFormat = "1080p29.97", "standard" // the flag defaults
	if err := a.initCoreEngine(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.sw.Close()
		_ = a.mixer.Close()
		_ = a.refClockEncoder.Close()
	})
	a.programRelayVideoStarted.Store(true)
	rec := &aacRecorder{}
	a.programRelay.AddViewer(rec)

	// The program audio, made by the sync loop's mixer: an impulse at a
	// sample index half a second in, whatever the clock's rate or drops.
	r48 := clock.Rate{Num: 48000, Den: 1}
	imp := &impulseMixer{at: clock.TimestampToIndex(r48, clock.TAINow()) + 24000, pts: -1}
	a.sw.SetSyncMixer(imp)
	a.sw.Start()
	time.Sleep(1500 * time.Millisecond)

	rec.mu.Lock()
	frames := append([]*media.AudioFrame(nil), rec.frames...)
	rec.mu.Unlock()
	if len(frames) < 20 {
		t.Fatalf("%d program AAC frames in 1.5 s, want about 70", len(frames))
	}
	dec, err := audio.NewFDKDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dec.Close() }()
	heard := int64(-1)
	for _, f := range frames {
		pcm, err := dec.Decode(f.Data)
		if err != nil {
			t.Fatal(err)
		}
		if i := firstLoud(pcm); i >= 0 {
			heard = f.PTS + int64(i)*90000/48000
			break
		}
	}
	if heard < 0 {
		t.Fatal("the impulse never came out of the program audio")
	}

	imp.mu.Lock()
	at := imp.pts
	imp.mu.Unlock()
	if at < 0 {
		t.Fatal("the tick carrying the impulse was dropped")
	}
	want := at + int64(decoderDelay)*90000/48000
	if d := heard - want; d < -4 || d > 4 {
		t.Fatalf("impulse heard at program PTS %d, want %d: off by %d ticks (%.1f samples; encoder delay %d samples)",
			heard, want, d, float64(d)*48000/90000, encDelay)
	}
}
