package playout

import (
	"math"

	"github.com/zsiec/switchframe/server/audio"
)

// transitionPCM crossfades the PCM of a transition's two items into the
// channel's one PCM stream (see emitPCM), with the mixer's gain curves at
// the progress the pictures are blended at. Encoded audio cannot be mixed
// here and stays with one item (attachTransitionAudio).
//
// Positions count samples of the stream from the PTS origin on; emitted is
// the position of the next sample handed out, the one at c.pcmPTS.
type transitionPCM struct {
	incoming string        // ID of the incoming item
	outgoing *PlaylistItem // read on to the end of the completing tick
	mode     audio.TransitionMode
	rate     int // of the stream, set by the first PCM; other rates are not mixed

	originPTS, originFrac int64
	emitted               int64

	// carry is the source PTS the stream under way ends at, while no side
	// has continued it yet: a clip slower than the channel has no PCM on
	// a picture it holds, and its next PCM carries the stream on.
	carry    bool
	carrySrc int64

	// The gain runs through a knot at the last sample of each tick's
	// picture period, the curve at that tick's progress, and in a straight
	// line between knots: anchor is the knot at or before the samples
	// handed out, knots the ones ahead.
	anchor gainKnot
	knots  []gainKnot
	// The knots of ticks before the rate was known, by their picture's
	// end.
	early []earlyKnot

	// final is set from the tick the transition completes on until the
	// incoming item's PCM has reached its last knot.
	final bool

	out, in xfadeSide

	// What transitionFrame queued for this tick's emitTransitionPCM.
	queued         bool
	qOut, qIn      []AudioFrameData
	queuedProgress float64
}

type gainKnot struct {
	pos     int64
	out, in float64
}

type earlyKnot struct {
	startPTS, startFrac int64
	endPTS, endFrac     int64
	progress            float64
	completing          bool
}

// xfadeSide is one item's PCM in a transition: buf holds what is not
// handed out yet, from position at on.
type xfadeSide struct {
	heard    bool  // PCM came in during the transition
	set      bool  // srcNext holds
	srcNext  int64 // source PTS the side's last chunk ends at
	channels int
	at       int64
	buf      []float32
}

func (s *xfadeSide) end() int64 {
	if s.channels == 0 {
		return s.at
	}
	return s.at + int64(len(s.buf)/s.channels)
}

// sample returns channel ch of the side's sample at position x, silence
// where the side has none.
func (s *xfadeSide) sample(x int64, ch int) float32 {
	i := x - s.at
	if s.channels == 0 || i < 0 || i >= int64(len(s.buf)/s.channels) {
		return 0
	}
	return s.buf[int(i)*s.channels+ch]
}

// transitionAudioMode is the gain curve of a transition type, as the
// switcher picks it: dips go through silence, every other blend crossfades.
func transitionAudioMode(typ string) audio.TransitionMode {
	switch typ {
	case "dip", "dip_black", "dip_white":
		return audio.DipToSilence
	}
	return audio.Crossfade
}

// transitionFromGain and transitionToGain are the gains of the outgoing and
// incoming items at progress p: the curves the mixer runs a switcher
// transition's sources on, a crossfade at equal power, a dip out over the
// first half and in over the second. The mixer keeps its own unexported;
// TestTransitionGainsAreTheMixers holds the two equal.
func transitionFromGain(mode audio.TransitionMode, p float64) float64 {
	if mode == audio.DipToSilence {
		if p < 0.5 {
			return math.Cos(p * math.Pi)
		}
		return 0
	}
	return math.Cos(p * math.Pi / 2)
}

func transitionToGain(mode audio.TransitionMode, p float64) float64 {
	if mode == audio.DipToSilence {
		if p >= 0.5 {
			return math.Sin((p*2 - 1) * math.Pi / 2)
		}
		return 0
	}
	return math.Sin(p * math.Pi / 2)
}

// queueTransitionPCM hands the PCM of a blended transition tick to
// emitTransitionPCM, starting the crossfade on the transition's first such
// tick.
func (c *Channel) queueTransitionPCM(t *TransitionReq, out, in *DecodedFrame) {
	x := c.xfade
	if x != nil && x.incoming != t.Incoming.ID {
		c.dropTransitionPCM()
		x = nil
	}
	if x == nil {
		mode := transitionAudioMode(t.Config.Type)
		x = &transitionPCM{
			incoming: t.Incoming.ID,
			outgoing: t.Outgoing,
			mode:     mode,
			anchor:   gainKnot{math.MinInt64, transitionFromGain(mode, 0), transitionToGain(mode, 0)},
		}
		if c.pcmSet {
			x.originPTS, x.originFrac = c.pcmPTS, c.pcmPTSFrac
			x.carry, x.carrySrc = true, c.pcmSrcNext
		} else {
			x.originPTS, x.originFrac = c.outputPTS, c.outputPTSFrac
		}
		c.xfade = x
	}
	x.queued = true
	x.qOut, x.qIn = framePCM(out), framePCM(in)
	x.queuedProgress = min(max(t.Progress, 0), 1)
}

// dropTransitionPCM ends a crossfade that did not complete into its
// incoming item. What plays next starts a stream of its own, even where
// its PCM's timestamps happen to continue the stream the crossfade took
// over.
func (c *Channel) dropTransitionPCM() {
	c.xfade = nil
	c.pcmSet = false
}

// framePCM returns the frame's audio, none for no frame.
func framePCM(f *DecodedFrame) []AudioFrameData {
	if f == nil {
		return nil
	}
	return f.AudioFrames
}

// readOutgoingTail reads the outgoing clip on, on the tick its transition
// completes, until its PCM covers the stream up to the gain's last knot
// (finalKnot): the pictures no longer show the clip, but its gain still
// falls to 0 there. The frame rates do not tell how far that is, as a
// clip slower than the channel hands a picture's PCM out ahead of it. The
// tick's picture goes out at the channel's output PTS. The clip is closed
// later in the tick, so neither its picture nor its position matter.
func (c *Channel) readOutgoingTail(x *transitionPCM) {
	c.mu.Lock()
	iter, item := c.clipIter, x.outgoing
	ok := item != nil && item.Type == ItemClip && c.clipItemID == item.ID && iter != nil
	clipFPS := c.clipFPS
	pts, frac := c.outputPTS, c.outputPTSFrac
	c.mu.Unlock()
	if !ok {
		return
	}
	pipelineFPS := c.config.FPS
	if pipelineFPS == 0 {
		pipelineFPS = 30.0
	}
	stepInt, stepFrac := c.videoStep()
	endPTS, endFrac := advancePTS(pts, frac, stepInt, stepFrac)
	// A clip without sound, or one that has run out, gives none however
	// far it is read.
	reads := int(math.Ceil(clipFPS/pipelineFPS)) + 1
	for ; reads > 0 && (x.rate == 0 || x.out.end() <= x.finalKnot(x.pos(pts, frac), x.pos(endPTS, endFrac))); reads-- {
		f, err := iter.Next()
		if err != nil || f == nil {
			return
		}
		x.addAll(&x.out, f.AudioFrames, x.posAt(pts, frac))
	}
}

// emitTransitionPCM hands pcmFn the crossfade of one tick, whose picture
// went out at framePTS: on a transition tick what queueTransitionPCM
// queued, at its progress; from the tick the transition completes on
// (completing) the incoming item's PCM, in, as it fades the rest of the
// way in, after which it runs on as the stream.
//
// The gain of each tick reaches the curve at the tick's progress on the
// last sample of its picture period, as the mixer's does on the last
// sample of a period, so it trails the pictures by up to a period. Each
// side's PCM is placed by its source PTS: a chunk that continues the
// side's last one, or the stream under way, follows it; any other starts
// on the picture.
func (c *Channel) emitTransitionPCM(pcmFn func([]float32, int64, int, bool), framePTS, framePTSFrac int64, completing bool, in []AudioFrameData) {
	x := c.xfade
	if pcmFn == nil {
		c.xfade = nil
		return
	}
	var out []AudioFrameData
	progress := 1.0
	if !completing {
		out, in, progress = x.qOut, x.qIn, x.queuedProgress
	}
	x.queued, x.qOut, x.qIn = false, nil, nil

	at := x.posAt(framePTS, framePTSFrac)
	x.addAll(&x.out, out, at)
	x.addAll(&x.in, in, at)

	stepInt, stepFrac := c.videoStep()
	endPTS, endFrac := advancePTS(framePTS, framePTSFrac, stepInt, stepFrac)
	if !x.final {
		x.early = append(x.early, earlyKnot{framePTS, framePTSFrac, endPTS, endFrac, progress, completing})
		x.final = completing
	}
	if x.rate == 0 {
		if completing {
			c.xfade = nil
		}
		return
	}
	for _, e := range x.early {
		x.addKnot(e)
	}
	x.early = x.early[:0]
	start, h := x.pos(framePTS, framePTSFrac), x.pos(endPTS, endFrac)

	// The stream goes out up to the end of the tick's picture, held back
	// by a side, or by the stream under way until a side continues it,
	// for as long as it is due to deliver: a clip slower than the channel
	// hands its PCM out on the pictures it moves on to. One that has none
	// for half a picture before this one has stopped, and is silent from
	// there.
	floor := start - (h-start)/2
	limit := h
	for _, s := range []*xfadeSide{&x.out, &x.in} {
		if s.heard {
			limit = min(limit, max(s.end(), floor))
		}
	}
	if x.carry {
		limit = min(limit, max(0, floor))
	}
	if x.final {
		// Once the pictures are the incoming item's, its PCM is the
		// stream, up to where it has any.
		switch {
		case x.in.heard:
			limit = x.in.end()
		case x.out.heard:
			limit = min(x.out.end(), x.finalKnot(start, h)+1)
		}
	}
	x.emit(c, pcmFn, limit)

	if x.final && (!x.in.heard || len(x.knots) == 0 || x.emitted > x.knots[len(x.knots)-1].pos || x.in.end() < floor) {
		// The incoming item's PCM continues the stream, or there is none.
		c.xfade = nil
		c.pcmSet, c.pcmSrcNext = x.in.heard, x.in.srcNext
	}
}

// addKnot puts the gain at the curve at e's progress on the last sample
// of e's picture.
func (x *transitionPCM) addKnot(e earlyKnot) {
	h := x.pos(e.endPTS, e.endFrac)
	k := gainKnot{h - 1, transitionFromGain(x.mode, e.progress), transitionToGain(x.mode, e.progress)}
	if e.completing {
		// The incoming item's PCM runs on at full gain from here, and fades
		// in over a picture period at least where a clip slower than the
		// channel had handed out its PCM ahead of the pictures.
		k.pos = x.finalKnot(x.pos(e.startPTS, e.startFrac), h)
	}
	if len(x.knots) == 0 {
		// What went out since the last knot held its gain.
		x.anchor.pos = max(x.anchor.pos, x.emitted-1)
	}
	// The knot of a picture whose samples went out before the transition
	// started is passed already: the gain ramps from them to the next.
	if k.pos >= x.emitted {
		x.knots = append(x.knots, k)
	}
}

// finalKnot is the position the gain reaches its end on, for a completing
// tick whose picture runs from start to h.
func (x *transitionPCM) finalKnot(start, h int64) int64 {
	return max(h, x.emitted+h-start) - 1
}

// pos is the stream position of the PTS pts.
func (x *transitionPCM) pos(pts, frac int64) int64 {
	d := float64(pts-x.originPTS) + float64(frac-x.originFrac)/ptsFracScale
	return int64(math.Round(d * float64(x.rate) / 90000))
}

// posAt returns the stream position of pts as a func, for use once the
// rate is known, which the first PCM added sets.
func (x *transitionPCM) posAt(pts, frac int64) func() int64 {
	return func() int64 { return x.pos(pts, frac) }
}

// addAll adds the PCM chunks of pcm to side s, those that do not continue
// what came before at the picture position at.
func (x *transitionPCM) addAll(s *xfadeSide, pcm []AudioFrameData, at func() int64) {
	for i := range pcm {
		af := &pcm[i]
		if af.PCM == nil || af.Channels <= 0 || af.SampleRate <= 0 {
			continue
		}
		if x.rate == 0 {
			x.rate = af.SampleRate
		}
		if af.SampleRate != x.rate {
			// pcmFn carries one rate: what does not match is not mixed.
			continue
		}
		x.add(s, af, at())
	}
}

// add places one chunk of a side's PCM: after the side's last chunk, or
// where the stream under way ends, if it continues that, else at the
// picture's position at.
func (x *transitionPCM) add(s *xfadeSide, af *AudioFrameData, at int64) {
	n := len(af.PCM) / af.Channels
	// Chunk PTS are rounded to the tick, so they continue within two.
	near := func(src int64) bool { d := af.PTS - src; return d <= 2 && d >= -2 }
	switch {
	case s.set && near(s.srcNext):
		at = s.end()
	case !s.set && x.carry && near(x.carrySrc):
		at, x.carry = 0, false
	}
	s.set, s.srcNext = true, af.PTS+int64(n)*90000/int64(af.SampleRate)
	if !s.heard {
		s.heard, s.channels = true, af.Channels
	}
	pcm := remapChannels(af.PCM, af.Channels, s.channels)
	switch end := s.end(); {
	case len(s.buf) == 0:
		s.at = at
	case at > end:
		s.buf = append(s.buf, make([]float32, int(at-end)*s.channels)...)
	case at < end:
		keep := max(at-s.at, 0)
		s.buf = s.buf[:int(keep)*s.channels]
		s.at = min(s.at, at)
	}
	s.buf = append(s.buf, pcm...)
	s.trim(x.emitted)
}

// trim drops what the side holds before position x.
func (s *xfadeSide) trim(x int64) {
	if s.channels == 0 || s.at >= x {
		return
	}
	k := min(x-s.at, int64(len(s.buf)/s.channels))
	s.buf = s.buf[int(k)*s.channels:]
	s.at += k
}

// gain returns the gains of the sample at position p, which runs past
// the knots before it.
func (x *transitionPCM) gain(p int64) (out, in float64) {
	for len(x.knots) > 0 && x.knots[0].pos < p {
		x.anchor, x.knots = x.knots[0], x.knots[1:]
	}
	a := x.anchor
	if len(x.knots) == 0 || p <= a.pos {
		return a.out, a.in
	}
	b := x.knots[0]
	t := float64(p-a.pos) / float64(b.pos-a.pos)
	return a.out + (b.out-a.out)*t, a.in + (b.in-a.in)*t
}

// emit hands out the stream from emitted up to limit. A stream with
// nothing at emitted starts afresh where a side has PCM again, as emitPCM
// starts one.
func (x *transitionPCM) emit(c *Channel, pcmFn func([]float32, int64, int, bool), limit int64) {
	first := limit
	for _, s := range []*xfadeSide{&x.out, &x.in} {
		if len(s.buf) > 0 {
			first = min(first, s.at)
		}
	}
	if first >= limit {
		return
	}
	start := false
	if !c.pcmSet || first > x.emitted {
		stepInt, stepFrac := ptsStep(float64(first) * 90000 / float64(x.rate))
		c.pcmPTS, c.pcmPTSFrac = advancePTS(x.originPTS, x.originFrac, stepInt, stepFrac)
		c.pcmSet, start = true, true
		x.emitted = first
	}

	// The wider side's layout carries the narrower one, so a 5.1 item
	// keeps its centre and surrounds under a stereo one.
	channels := max(x.out.channels, x.in.channels)
	n := limit - x.emitted
	pcm := make([]float32, int(n)*channels)
	for i := range n {
		pos := x.emitted + i
		gOut, gIn := x.gain(pos)
		for ch := range channels {
			pcm[int(i)*channels+ch] = float32(gOut)*x.out.mapped(pos, ch, channels) +
				float32(gIn)*x.in.mapped(pos, ch, channels)
		}
	}
	pcmFn(pcm, c.pcmPTS, channels, start)
	stepInt, stepFrac := ptsStep(float64(n) * 90000 / float64(x.rate))
	c.pcmPTS, c.pcmPTSFrac = advancePTS(c.pcmPTS, c.pcmPTSFrac, stepInt, stepFrac)
	x.emitted = limit
	x.out.trim(limit)
	x.in.trim(limit)
}

// mapped is the side's sample at x laid out as a stream of channels
// channels (see remapChannels).
func (s *xfadeSide) mapped(x int64, ch, channels int) float32 {
	switch {
	case s.channels == channels:
		return s.sample(x, ch)
	case s.channels == 1:
		if ch < 2 {
			return s.sample(x, 0)
		}
		return 0
	case ch < s.channels:
		return s.sample(x, ch)
	}
	return 0
}

// remapChannels lays pcm of from channels out as to channels, at least as
// many, for a side whose chunks change channel count: mono goes on the
// first two channels, any other layout channel for channel, the channels
// it lacks silent. The front pair is first in every layout the decoder
// hands out, so stereo and mono stay on it.
func remapChannels(pcm []float32, from, to int) []float32 {
	if from == to {
		return pcm
	}
	s := xfadeSide{channels: from, buf: pcm}
	n := len(pcm) / from
	out := make([]float32, n*to)
	for i := range n {
		for ch := range to {
			out[i*to+ch] = s.mapped(int64(i), ch, to)
		}
	}
	return out
}
