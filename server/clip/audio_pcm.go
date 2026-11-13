package clip

import (
	"errors"
	"fmt"
	"sort"

	"github.com/zsiec/switchframe/server/codec"
)

// aacPriming is the encoder priming an AAC stream is taken to start with
// when its container says nothing about it: one frame, as an MPEG-TS
// stream or an MP4 without an edit list or iTunSMPB carries it.
const aacPriming = 1024

// PCMCodec is what a PCMStream decodes and resamples a clip's audio with.
type PCMCodec struct {
	// NewDecoder opens an AAC decoder, ADTS in, that adds no delay
	// beyond what its OutputDelay reports, so a sample comes out where
	// the container counts it.
	NewDecoder func(sampleRate, channels int) (PCMDecoder, error)
	// NewResampler converts interleaved PCM from srcRate to dstRate.
	NewResampler func(srcRate, dstRate, channels int) PCMResampler
}

// PCMDecoder decodes ADTS-framed AAC to interleaved float32 PCM.
type PCMDecoder interface {
	Decode(adts []byte) ([]float32, error)
	// Channels is the channel count of the last decoded frame.
	Channels() int
	// OutputDelay is how many samples per channel the decoder delays its
	// output by beyond the stream's own.
	OutputDelay() int
	Close() error
}

// PCMResampler is a polyphase resampler of upsampling factor L,
// downsampling factor M and TapsPerPhase taps per phase, whose output
// lags its input by (L*TapsPerPhase-1)/2 samples of the upsampled rate.
type PCMResampler interface {
	Resample(in []float32) []float32
	Reset()
	UpFactor() int
	DownFactor() int
	TapsPerPhase() int
}

// pcmTrack decodes a clip's AAC frames to the samples they present, at
// the clip's own rate: each frame's skip and cut samples, the encoder
// priming and the padding, are decoded, so the samples after them come
// out whole, and dropped.
type pcmTrack struct {
	frames   []BufferedAudioFrame
	rate     int
	channels int     // of the decoded PCM
	frameLen int     // samples a frame decodes to, per channel
	starts   []int64 // presented samples before frame k, len(frames)+1 of them

	codec PCMCodec
	dec   PCMDecoder
	fresh bool      // dec has decoded nothing yet
	next  int       // next frame to decode
	from  int64     // coded sample from which samples are presented
	coded int64     // coded sample of the next decoded sample
	pend  []float32 // presented samples decoded and not read
	pos   int64     // presented sample the next read starts at
}

// newPCMTrack decodes the first frame of frames to learn their layout.
func newPCMTrack(frames []BufferedAudioFrame, c PCMCodec) (*pcmTrack, error) {
	if len(frames) == 0 {
		return nil, errors.New("no audio")
	}
	if c.NewDecoder == nil {
		return nil, errors.New("no AAC decoder")
	}
	t := &pcmTrack{frames: frames, rate: frames[0].sampleRate, codec: c}
	if t.rate <= 0 {
		t.rate = 48000
	}
	if err := t.open(0); err != nil {
		return nil, err
	}
	pcm, err := t.decode(0)
	if err != nil {
		t.close()
		return nil, err
	}
	t.channels = t.dec.Channels()
	if t.channels <= 0 || len(pcm) == 0 {
		t.close()
		return nil, errors.New("audio decodes to nothing")
	}
	t.frameLen = len(pcm) / t.channels
	t.starts = make([]int64, len(frames)+1)
	for k, f := range frames {
		if !f.hidden {
			t.starts[k+1] = t.starts[k] + int64(max(0, t.frameLen-f.skip-f.cut))
		} else {
			t.starts[k+1] = t.starts[k]
		}
	}
	t.seek(0)
	return t, nil
}

// samples is how many samples the track presents, per channel.
func (t *pcmTrack) samples() int64 { return t.starts[len(t.frames)] }

func (t *pcmTrack) open(from int) error {
	t.close()
	f := t.frames[from]
	dec, err := t.codec.NewDecoder(t.rate, max(1, min(f.channels, 8)))
	if err != nil {
		return fmt.Errorf("open AAC decoder: %w", err)
	}
	t.dec, t.next, t.fresh = dec, from, true
	return nil
}

func (t *pcmTrack) close() {
	if t.dec != nil {
		_ = t.dec.Close()
		t.dec = nil
	}
}

func (t *pcmTrack) decode(k int) ([]float32, error) {
	f := t.frames[k]
	return t.dec.Decode(codec.EnsureADTS(f.data, t.rate, f.channels))
}

// seek moves the next read to presented sample s. Decoding restarts one
// frame before the frame holding s: a frame decodes right only after the
// one before it, whose output is dropped.
func (t *pcmTrack) seek(s int64) {
	s = max(0, min(s, t.samples()))
	k := sort.Search(len(t.frames), func(k int) bool { return t.starts[k+1] > s })
	t.pend, t.pos = t.pend[:0], s
	if k == len(t.frames) {
		t.close()
		t.next = k
		return
	}
	start := max(k-1, 0)
	if err := t.open(start); err != nil {
		t.next = len(t.frames)
		return
	}
	t.from = int64(k*t.frameLen+t.frames[k].skip) + s - t.starts[k]
}

// read returns up to n presented samples per channel, interleaved, fewer
// only at the end of the track. The slice is valid until the next call.
func (t *pcmTrack) read(n int) []float32 {
	want := n * t.channels
	for len(t.pend) < want && t.next < len(t.frames) && t.dec != nil {
		k := t.next
		t.next++
		pcm, err := t.decode(k)
		if err != nil || len(pcm) == 0 {
			// A frame that does not decode is silence of its length: the
			// samples after it keep their place.
			pcm = make([]float32, t.frameLen*t.channels)
		}
		if t.fresh {
			t.fresh = false
			t.coded = int64(k*t.frameLen) - int64(t.dec.OutputDelay())
		}
		t.present(pcm)
	}
	m := min(want, len(t.pend))
	out := append([]float32(nil), t.pend[:m]...)
	t.pend = t.pend[:copy(t.pend, t.pend[m:])]
	t.pos += int64(m / t.channels)
	return out
}

// present appends the samples of pcm, which start at coded sample
// t.coded, that a frame presents and the read has not passed.
func (t *pcmTrack) present(pcm []float32) {
	n := int64(len(pcm) / t.channels)
	L := int64(t.frameLen)
	for i := int64(0); i < n; {
		c := t.coded + i
		k := c / L
		if c < 0 || k >= int64(len(t.frames)) {
			i++
			continue
		}
		end := min(n, (k+1)*L-t.coded) // this run stays in frame k
		if t.frames[k].hidden {
			i = end
			continue
		}
		lo := max(c, k*L+int64(t.frames[k].skip), t.from)
		hi := (k+1)*L - int64(t.frames[k].cut)
		if lo < hi {
			a, b := lo-t.coded, min(hi-t.coded, end)
			if a < b {
				t.pend = append(t.pend, pcm[a*int64(t.channels):b*int64(t.channels)]...)
			}
		}
		i = end
	}
	t.coded += n
}

// PCMStream reads a clip's audio as PCM at a fixed rate, one pass of the
// clip after the other. The clip's own samples are its presented ones,
// without the encoder priming and padding: clip sample 0 is its first
// read. A pass that runs past the clip's samples goes on in silence, and
// the next pass follows the last read without a gap, so a loop that
// gives each pass its video's length keeps the audio on the video. A clip
// at another rate is resampled; the filter's delay is taken off, so a
// sample keeps its place within one output sample.
type PCMStream struct {
	t    *pcmTrack
	rate int

	rs   PCMResampler
	hist int64   // source samples fed ahead of a start to fill the filter
	dly  float64 // filter delay in output samples

	passLen  int64 // source samples of the pass under way
	inPass   int64 // of those, read
	nextPass func() (int64, bool)
	ended    bool

	// Since the last start: source samples fed, output samples
	// returned, output samples still to drop.
	fed, put int64
	drop     int
	out      []float32
}

// NewPCMStream decodes the audio of a clip, frames as the demuxer returns
// them, to PCM at rate, with c.
func NewPCMStream(frames []BufferedAudioFrame, rate int, c PCMCodec) (*PCMStream, error) {
	t, err := newPCMTrack(frames, c)
	if err != nil {
		return nil, err
	}
	s := &PCMStream{t: t, rate: rate}
	if t.rate != rate {
		if c.NewResampler == nil {
			t.close()
			return nil, fmt.Errorf("no resampler for %d Hz", t.rate)
		}
		s.rs = c.NewResampler(t.rate, rate, t.channels)
		L, M, taps := s.rs.UpFactor(), s.rs.DownFactor(), s.rs.TapsPerPhase()
		s.hist = int64(taps)
		s.dly = float64(L*taps-1) / float64(2*M)
	}
	s.Start(0, t.samples(), nil)
	return s, nil
}

// Channels returns the channel count of the PCM.
func (s *PCMStream) Channels() int { return s.t.channels }

// SourceRate returns the clip's sample rate.
func (s *PCMStream) SourceRate() int { return s.t.rate }

// Samples returns how many samples per channel the clip presents, at
// its own rate.
func (s *PCMStream) Samples() int64 { return s.t.samples() }

// Start restarts the stream at clip sample pos, at the clip's own rate,
// in a pass that ends passLen samples from the clip's start. At the end of
// the pass nextPass gives the next one's length, which starts at clip
// sample 0, or false to end the stream; nil ends it.
func (s *PCMStream) Start(pos, passLen int64, nextPass func() (int64, bool)) {
	s.passLen, s.inPass, s.nextPass, s.ended = passLen, pos, nextPass, false
	s.fed, s.put, s.drop, s.out = 0, 0, 0, s.out[:0]
	if s.rs == nil {
		s.t.seek(pos)
		return
	}
	// The filter starts on the samples before pos, so pos comes out as it
	// does in a stream that runs through it; what they put out is dropped
	// with the filter's delay.
	s.rs.Reset()
	L, M := s.rs.UpFactor(), s.rs.DownFactor()
	s.drop = int(float64(s.hist)*float64(L)/float64(M) + s.dly + 0.5)
	h := min(s.hist, pos)
	if zeros := s.hist - h; zeros > 0 {
		s.push(s.rs.Resample(make([]float32, int(zeros)*s.t.channels)))
	}
	s.t.seek(pos - h)
	if h > 0 {
		s.push(s.rs.Resample(s.t.read(int(h))))
	}
}

// Read returns the next n samples per channel, interleaved, fewer only at
// the end of the stream, nil after it. The slice is valid until the next
// call.
func (s *PCMStream) Read(n int) []float32 {
	c := s.t.channels
	for len(s.out) < n*c && !s.ended {
		src := s.source(max(256, n))
		if src == nil {
			s.ended = true
			if s.rs != nil {
				s.flush()
			}
			break
		}
		if s.rs == nil {
			s.out = append(s.out, src...)
			continue
		}
		s.push(s.rs.Resample(src))
	}
	m := min(n*c, len(s.out))
	if m == 0 {
		return nil
	}
	out := append([]float32(nil), s.out[:m]...)
	s.out = s.out[:copy(s.out, s.out[m:])]
	return out
}

// push appends resampled samples, dropping the filter delay first.
func (s *PCMStream) push(pcm []float32) {
	c := s.t.channels
	if d := min(s.drop*c, len(pcm)); d > 0 {
		pcm = pcm[d:]
		s.drop -= d / c
	}
	s.out = append(s.out, pcm...)
	s.put += int64(len(pcm) / c)
}

// flush feeds the filter silence until it has put out every sample fed
// since the start.
func (s *PCMStream) flush() {
	L, M := int64(s.rs.UpFactor()), int64(s.rs.DownFactor())
	want := (s.fed*L + M/2) / M
	zeros := make([]float32, 64*s.t.channels)
	for s.put < want {
		s.push(s.rs.Resample(zeros))
	}
	if extra := s.put - want; extra > 0 {
		s.out = s.out[:len(s.out)-int(extra)*s.t.channels]
		s.put = want
	}
}

// source returns up to n source samples of the passes: the clip's, then
// silence to the end of the pass. Nil at the end of the stream.
func (s *PCMStream) source(n int) []float32 {
	for s.inPass >= s.passLen {
		if s.nextPass == nil {
			return nil
		}
		next, ok := s.nextPass()
		if !ok {
			return nil
		}
		s.passLen, s.inPass = next, 0
		s.t.seek(0)
	}
	n = int(min(int64(n), s.passLen-s.inPass))
	var pcm []float32
	if s.inPass < s.t.samples() {
		pcm = s.t.read(int(min(int64(n), s.t.samples()-s.inPass)))
	}
	if got := len(pcm) / s.t.channels; got < n {
		pcm = append(pcm, make([]float32, (n-got)*s.t.channels)...)
	}
	s.inPass += int64(n)
	s.fed += int64(n)
	return pcm
}

// Close releases the decoder.
func (s *PCMStream) Close() { s.t.close() }
