package audio

import (
	"math"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/codec"
)

// aacPrimingSamples is the number of PCM samples FDK-AAC-LC silently discards
// before emitting valid output (one full MDCT frame).
const aacPrimingSamples = 1024

// IngestFrame processes an AAC audio frame from a source. Decodes to PCM,
// applies the per-channel processing chain (trim -> EQ -> compressor), and
// pushes the processed result into the channel's ring buffer. The decoded
// samples are stamped with the TAI of their arrival, as the video of the
// sources that deliver AAC (relay, clip, replay) is: frame.PTS is
// the source's own timeline, not the engine's. No immediate output is
// produced -- ProduceSamples reads the ring buffers.
func (m *Mixer) IngestFrame(sourceKey string, frame *media.AudioFrame) {
	m.ingestFrame(sourceKey, frame, false)
}

// IngestTimedFrame is IngestFrame for a source whose producer places its
// AAC on the engine timeline: frame.PTS is the engine-timeline PTS of the
// frame's first sample, and the decoded audio is stamped with it rather
// than with its arrival, so frames handed over in bunches stay continuous.
func (m *Mixer) IngestTimedFrame(sourceKey string, frame *media.AudioFrame) {
	m.ingestFrame(sourceKey, frame, true)
}

func (m *Mixer) ingestFrame(sourceKey string, frame *media.AudioFrame, timed bool) {
	// Audio forwarding: if this source is being forwarded to another channel
	// (e.g., live source → playout channel), ingest for the destination too.
	// Use ingestFrameInner to avoid infinite recursion (forwards don't chain).
	m.mu.RLock()
	fwdDst := m.audioForwards[sourceKey]
	m.mu.RUnlock()
	if fwdDst != "" {
		fwdFrame := *frame
		// Deep-copy Data to prevent aliasing — both paths decode/modify the slice.
		fwdFrame.Data = append([]byte(nil), frame.Data...)
		m.ingestFrameInner(fwdDst, &fwdFrame, timed, true)
	}

	m.ingestFrameInner(sourceKey, frame, timed, false)
}

// ingestFrameInner ingests frame for sourceKey; forwarded is set for a
// copy forwarded to another channel, whose video does not arrive with it
// and is not held (arrivalHold).
func (m *Mixer) ingestFrameInner(sourceKey string, frame *media.AudioFrame, timed, forwarded bool) {
	m.mu.Lock()

	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return
	}

	// Always decode AAC -> PCM (no passthrough in clock-driven mixer)
	m.initChannelDecoder(ch)
	if ch.decoder == nil {
		m.mu.Unlock()
		return
	}

	// Ensure ADTS header is present -- FDK decoder requires ADTS framing.
	adtsFrame := codec.EnsureADTS(frame.Data, frame.SampleRate, frame.Channels)

	// Decode AAC -> float32 PCM
	pcm, err := ch.decoder.Decode(adtsFrame)
	if err != nil {
		m.decodeErrors.Add(1)
		m.mu.Unlock()
		m.log.Warn("decode error", "source", sourceKey, "err", err)
		return
	}

	// FDK-AAC-LC silently discards 1–2 priming frames while the MDCT overlap
	// buffer fills. Without compensation, valid PCM lands in the ring buffer
	// earlier than the source timeline expects — audio plays ahead of video.
	// On the first valid emission, prepend one silence frame per observed
	// priming skip, scaled to mixer channels and (when the source rate differs
	// from the mixer rate) the mixer's sample rate. Silence pushed before the
	// real PCM, so the ring-buffer timeline matches the source timeline.
	if len(pcm) == 0 {
		if !ch.primingOffsetApplied {
			ch.primingSkipCount++
		}
		m.mu.Unlock()
		return
	}
	if ch.primingSkipCount > 0 && !ch.primingOffsetApplied {
		ch.primingOffsetApplied = true
		silencePerFrame := int(m.primingSamples(frame.SampleRate))
		ch.ringBuf.Push(make([]float32, silencePerFrame*m.numChannels*ch.primingSkipCount))
	}

	// Surround → mixer-channels downmix: if the bitstream decoded to more
	// channels than the mixer supports, fold before the resampler and ring
	// buffer, which are both sized for m.numChannels. A bitstream with
	// fewer channels, mono, is spread over all of them.
	srcChannels := ch.decoder.Channels()
	if srcChannels > 0 {
		ch.sourceChannels = srcChannels
	}
	pcm, srcChannels = m.selectPair(ch, pcm, srcChannels)
	if srcChannels > 0 && srcChannels < m.numChannels {
		pcm = m.upmixMono(&ch.upmixBuf, pcm, srcChannels)
	}
	if srcChannels > m.numChannels {
		var result downmixResult
		pcm, result = m.downmixAAC(&ch.downmixBuf, pcm, srcChannels)
		if !ch.surroundWarned {
			ch.surroundWarned = true
			m.logDownmixWarn(sourceKey, "AAC", srcChannels, result)
		}
	}

	// Resample if source rate doesn't match mixer rate.
	pcm = m.resampleIfNeeded(ch, pcm, frame.SampleRate)

	// Muted channels skip ring buffer entirely.
	if ch.muted {
		ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)
		m.mu.Unlock()
		return
	}

	// Update per-channel peaks (pre-fader, pre-gain)
	ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)

	// Pipeline order: Trim -> Phase -> HPF -> Gate -> EQ -> Compressor -> Balance -> [push to ring buffer]
	// Fader gain and transition gain are applied by ProduceSamples at read time.

	// Apply trim (input gain)
	ch.trimBuf = growBuf(ch.trimBuf, len(pcm))
	trimmedPCM := ch.trimBuf
	for i, s := range pcm {
		trimmedPCM[i] = s * ch.trimLinear
	}

	// Apply phase invert
	if ch.phaseInvert {
		for i := range trimmedPCM {
			trimmedPCM[i] = -trimmedPCM[i]
		}
	}

	// Apply high-pass filter
	if !ch.hpf.IsBypassed() {
		ch.hpf.Process(trimmedPCM, m.numChannels)
	}

	// Apply noise gate
	if !ch.gate.IsBypassed() {
		ch.gate.Process(trimmedPCM)
	}

	// Apply EQ (3-band parametric)
	if !ch.eq.IsBypassed() {
		ch.eq.Process(trimmedPCM, m.numChannels)
	}

	// Apply compressor
	if !ch.compressor.IsBypassed() {
		ch.compressor.Process(trimmedPCM)
	}

	// Apply stereo balance (after compressor, before ring buffer push).
	// At center (0.0) both gains are 1.0, so this is a no-op.
	// Only apply when numChannels >= 2 — balance is a stereo concept.
	if m.numChannels >= 2 && (ch.balL != 1.0 || ch.balR != 1.0) {
		stride := m.numChannels
		for i := 0; i+1 < len(trimmedPCM); i += stride {
			trimmedPCM[i] *= ch.balL
			trimmedPCM[i+1] *= ch.balR
		}
	}

	// Push processed PCM to ring buffer for clock-driven output.
	n := len(trimmedPCM) / m.numChannels
	ch.ringBuf.Push(trimmedPCM)
	if timed {
		// The decoder returns each frame's audio primingSkipCount frames
		// after the frame that carried it.
		delay := int64(ch.primingSkipCount) * m.primingSamples(frame.SampleRate)
		m.stampRing(ch, frame.PTS-delay*90000/int64(m.sampleRate), n)
	} else {
		m.stampArrival(ch, n, frame.SampleRate, !forwarded)
	}
	m.mu.Unlock()
}

// IngestPCM processes raw interleaved float32 PCM from a source (e.g. MXL).
// Unlike IngestFrame, this skips ADTS parsing and AAC decoding -- the PCM is
// already in float32 format. Applies the per-channel processing chain
// (trim -> EQ -> compressor) and pushes into the channel's ring buffer.
//
// PCM input is interleaved float32 (e.g. 1024 samples * 2 channels = 2048 values for stereo).
// The pts parameter is the presentation timestamp in 90 kHz clock units.
// The channels parameter is the source's actual channel count (1=mono, 2=stereo).
// If channels < mixer's numChannels, mono samples are upmixed to stereo.
func (m *Mixer) IngestPCM(sourceKey string, pcm []float32, pts int64, channels int) {
	m.ingestPCM(sourceKey, pcm, pts, channels, false)
}

// IngestPCMSlewed is IngestPCM for a source whose stamps move as a
// clock.TrackingAnchor slews its offset. The audio continues the source's
// audio before it, and when its stamps are 2 samples or more off that, it
// is resampled towards them by at most one sample in slewDivisor (0.5%)
// until it is on them again, so a move of the stamps is neither a splice
// nor silence. Stamps half a program period or more off, as a step of the
// anchor moves them, place the audio again on them, as the first push
// does, so it never airs that far from its video.
func (m *Mixer) IngestPCMSlewed(sourceKey string, pcm []float32, pts int64, channels int) {
	m.ingestPCM(sourceKey, pcm, pts, channels, true)
}

func (m *Mixer) ingestPCM(sourceKey string, pcm []float32, pts int64, channels int, slewed bool) {
	// Audio forwarding (same pattern as IngestFrame).
	m.mu.RLock()
	fwdDst := m.audioForwards[sourceKey]
	m.mu.RUnlock()
	if fwdDst != "" {
		// Deep-copy PCM to prevent aliasing.
		fwdPCM := make([]float32, len(pcm))
		copy(fwdPCM, pcm)
		m.ingestPCMInner(fwdDst, fwdPCM, pts, channels, slewed)
	}
	m.ingestPCMInner(sourceKey, pcm, pts, channels, slewed)
}

func (m *Mixer) ingestPCMInner(sourceKey string, pcm []float32, pts int64, channels int, slewed bool) {
	m.mu.Lock()

	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return
	}

	if channels > 0 {
		ch.sourceChannels = channels
	}
	pcm, channels = m.selectPair(ch, pcm, channels)

	// Mono->stereo upmix: if source delivers fewer channels than the mixer
	// expects, duplicate each sample to fill all channels.
	if channels > 0 && channels < m.numChannels {
		pcm = m.upmixMono(&ch.upmixBuf, pcm, channels)
	}

	// Surround → mixer-channels downmix: symmetric to upmix above. A 5.1 PCM
	// source pushed into a stereo mixer would otherwise be read with the
	// wrong stride by the per-channel DSP chain and the ring buffer.
	if channels > m.numChannels {
		var result downmixResult
		pcm, result = m.downmixChannels(&ch.downmixBuf, pcm, channels)
		if !ch.surroundWarned {
			ch.surroundWarned = true
			m.logDownmixWarn(sourceKey, "PCM", channels, result)
		}
	}

	// Always compute peak levels for input metering, even for inactive/muted channels.
	ch.peakL, ch.peakR = PeakLevel(pcm, m.numChannels)

	// Muted channels skip ring buffer entirely — there's no unmute crossfade.
	if ch.muted {
		m.mu.Unlock()
		return
	}

	// Mark channel as PCM source (no passthrough recovery needed in clock-driven mixer).
	if !ch.isPCM {
		ch.isPCM = true
	}

	// Resample if source rate doesn't match mixer rate.
	pcm = m.resampleIfNeeded(ch, pcm, 0) // PCM sources don't carry sample rate in-band

	// Pipeline order: Trim -> Phase -> HPF -> Gate -> EQ -> Compressor -> Balance -> [push to ring buffer]
	// Fader gain and transition gain are applied by ProduceSamples at read time.

	// Apply trim (input gain)
	ch.trimBuf = growBuf(ch.trimBuf, len(pcm))
	trimmedPCM := ch.trimBuf
	for i, s := range pcm {
		trimmedPCM[i] = s * ch.trimLinear
	}

	// Apply phase invert
	if ch.phaseInvert {
		for i := range trimmedPCM {
			trimmedPCM[i] = -trimmedPCM[i]
		}
	}

	// Apply high-pass filter
	if !ch.hpf.IsBypassed() {
		ch.hpf.Process(trimmedPCM, m.numChannels)
	}

	// Apply noise gate
	if !ch.gate.IsBypassed() {
		ch.gate.Process(trimmedPCM)
	}

	// Apply EQ (3-band parametric)
	if !ch.eq.IsBypassed() {
		ch.eq.Process(trimmedPCM, m.numChannels)
	}

	// Apply compressor
	if !ch.compressor.IsBypassed() {
		ch.compressor.Process(trimmedPCM)
	}

	// Apply stereo balance (after compressor, before ring buffer push).
	// At center (0.0) both gains are 1.0, so this is a no-op.
	// Only apply when numChannels >= 2 — balance is a stereo concept.
	if m.numChannels >= 2 && (ch.balL != 1.0 || ch.balR != 1.0) {
		stride := m.numChannels
		for i := 0; i+1 < len(trimmedPCM); i += stride {
			trimmedPCM[i] *= ch.balL
			trimmedPCM[i+1] *= ch.balR
		}
	}

	// Always push to ring buffer, even for inactive channels. This keeps the
	// buffer primed so that when a channel activates via AFV, there is no
	// silence gap from an empty ring buffer. ProduceSamples only reads from
	// active channels, so inactive data won't be mixed into output.
	if slewed {
		m.pushSlewed(ch, trimmedPCM, pts)
	} else {
		ch.ringBuf.Push(trimmedPCM)
		m.stampRing(ch, pts, len(trimmedPCM)/m.numChannels)
	}
	m.mu.Unlock()
}

// slewDivisor bounds how fast IngestPCMSlewed resamples a source onto its
// stamps: one sample in 200, 0.5%, a pitch change under 9 cents.
const slewDivisor = 200

// slewTaps is the length of the windowed-sinc kernel IngestPCMSlewed
// resamples with, and slewPhases how many fractional positions its table
// holds, interpolated between: the error is under -60 dB up to 15 kHz at
// 48 kHz. A source is held back slewTaps/2 frames so the frames after the
// one resampled are on hand: that costs 167 us of the audio budget at
// 48 kHz, and the held frames are released by the next push, so the last
// of them before a source stops never air.
const (
	slewTaps   = 16
	slewPhases = 512
	slewBeta   = 7.0
)

// slewKernel holds slewPhases+1 kernels of slewTaps taps: kernel p
// interpolates the position p/slewPhases after a frame from the frames
// slewTaps/2-1 before it to slewTaps/2 after, normalised to unity gain.
var slewKernel = func() []float32 {
	const h = slewTaps / 2
	k := make([]float32, (slewPhases+1)*slewTaps)
	for p := 0; p <= slewPhases; p++ {
		frac := float64(p) / slewPhases
		row := k[p*slewTaps : (p+1)*slewTaps]
		var sum float64
		w := make([]float64, slewTaps)
		for t := range slewTaps {
			x := float64(t-h+1) - frac
			v := 1.0
			if x != 0 {
				v = math.Sin(math.Pi*x) / (math.Pi * x)
			}
			if r := x / h; r > -1 && r < 1 {
				v *= besselI0(slewBeta*math.Sqrt(1-r*r)) / besselI0(slewBeta)
			} else {
				v = 0
			}
			w[t], sum = v, sum+v
		}
		for t := range slewTaps {
			row[t] = float32(w[t] / sum)
		}
	}
	return k
}()

// slewAt writes to dst the frame of hist, interleaved frames of c
// channels, at position pos, which has slewTaps/2-1 frames before it and
// slewTaps/2 after.
func slewAt(dst, hist []float32, c int, pos float64) {
	i := int(pos)
	frac := pos - float64(i)
	if frac == 0 {
		copy(dst, hist[i*c:(i+1)*c])
		return
	}
	f := frac * slewPhases
	p := int(f)
	a := float32(f - float64(p))
	k0 := slewKernel[p*slewTaps : (p+1)*slewTaps]
	k1 := slewKernel[(p+1)*slewTaps : (p+2)*slewTaps]
	base := (i - slewTaps/2 + 1) * c
	for ch := range c {
		var v float32
		for t := range slewTaps {
			v += hist[base+t*c+ch] * (k0[t] + a*(k1[t]-k0[t]))
		}
		dst[ch] = v
	}
}

// pushSlewed pushes pcm, stamped pts, for IngestPCMSlewed. Its frames
// follow the ones before; while the next frame airs 2 samples or more off
// its stamp, they are resampled towards the stamps, a sample in
// slewDivisor, until it airs on it, and half a program period or more off
// they are placed again on the stamps. A 44.1 kHz stream's stamps step a
// sample either way of its own audio, which needs no resampling, and a
// slewing anchor moves the stamps under a millisecond between an MPEG-TS
// stream's bursts of audio, under half the 4.2 ms period at 240 fps.
// Caller holds m.mu.
func (m *Mixer) pushSlewed(ch *Channel, pcm []float32, pts int64) {
	const h = slewTaps / 2
	c := m.numChannels
	n := len(pcm) / c
	if n == 0 {
		return
	}
	sr := clock.Rate{Num: int64(m.sampleRate), Den: 1}
	start := int64(clock.TimestampToIndex(sr, clock.TimestampOf(pts)))
	var end int64 // mixer index of the next frame pushed to the ring
	var off float64
	held := len(ch.slewHist) / c
	if ch.ringEnd != 0 && held > 0 {
		end = int64(clock.TimestampToIndex(sr, ch.ringEnd))
		// >0: the next frame airs after its stamp. The indices are
		// subtracted first: as floats they are a sixty-fourth apart.
		off = float64(end-start+int64(held)) - ch.slewPos
		period := clock.ProgramPeriod()
		if period == 0 {
			period = 90000 / 240 // no program clock yet: the shortest period read
		}
		if most := float64(period * sr.Num / 90000 / 2); off >= most || -off >= most {
			// Placed again: the frames held back air first, as they are.
			rest := ch.slewHist[int(math.Round(ch.slewPos))*c:]
			ch.ringBuf.Push(rest)
			end += int64(len(rest) / c)
			held = 0
			ch.placed = false
		}
	}
	if held == 0 {
		ch.slewHist = append(ch.slewHist[:0], make([]float32, h*c)...)
		ch.slewPos, ch.slewDir, off, end = h, 0, 0, start
	}
	ch.slewHist = append(ch.slewHist, pcm...)
	frames := len(ch.slewHist) / c
	out := ch.slewBuf[:0]
	for int(ch.slewPos)+h < frames {
		if ch.slewDir == 0 && (off >= 2 || off <= -2) {
			ch.slewDir = 1
			if off < 0 {
				ch.slewDir = -1
			}
		}
		k := len(out)
		out = append(out, make([]float32, c)...)
		slewAt(out[k:], ch.slewHist, c, ch.slewPos)
		step := 1.0
		switch {
		case ch.slewDir == 0:
		case off <= 1.0/slewDivisor+1e-9 && off >= -1.0/slewDivisor-1e-9:
			// Lands on the stamps: the next frame is a whole one.
			step, ch.slewDir = 1+off, 0
		case off > 0:
			// Towards the stamps, which may have stepped past it.
			step, ch.slewDir = 1+1.0/slewDivisor, 1
		default:
			step, ch.slewDir = 1-1.0/slewDivisor, -1
		}
		ch.slewPos += step
		off += 1 - step
		if ch.slewDir == 0 {
			ch.slewPos, off = math.Round(ch.slewPos*1e6)/1e6, math.Round(off*1e6)/1e6
		}
	}
	ch.slewBuf = out
	ch.ringBuf.Push(out)
	ch.ringEnd = clock.IndexToTimestamp(sr, uint64(end)+uint64(len(out)/c))
	if drop := int(ch.slewPos) - h + 1; drop > 0 {
		ch.slewHist = append(ch.slewHist[:0], ch.slewHist[drop*c:]...)
		ch.slewPos -= float64(drop)
	}
}

// arrivalSlack is how far ahead of its arrival a source's audio may be
// placed to continue the audio it delivered before, in AAC frames of the
// source. A source that hands over its AAC frames with its video frames,
// as the replay player does, delivers one AAC frame a frame period, none
// in some periods; placed by each arrival, its audio steps back and forth
// by about an AAC frame, and a step past a period, which scheduling jitter
// makes likely, is spliced.
const arrivalSlack = 2

// arrivalLate is how far behind its arrival a source's audio may be placed
// to continue the audio before it once that audio has all been read: 5.3
// ms, and no more than half a program period, so audio placed by its
// arrival stays within half a picture of the video that arrived with it
// at any program rate. Both are well inside the audio budget, the read
// delay less the period a tick reads (clock.ReadDelay), past which audio
// placed behind its arrival would be missing when read. While an active
// channel holds audio before it that is due and unread, a push continues
// it up to arrivalSlack late.
const arrivalLate = 16 * time.Millisecond / 3

// lateArrival returns arrivalLate at the running program rate in
// nanoseconds, or arrivalLate before a program clock runs.
func lateArrival() int64 {
	late := int64(arrivalLate)
	if p := clock.ProgramPeriod(); p > 0 {
		late = min(late, p*1_000_000_000/(2*90000))
	}
	return late
}

// stampArrival places the n samples just pushed for a source without a
// media clock right after the source's audio before them, when they
// arrive between arrivalSlack before and arrivalLate after that ends, or
// arrivalSlack after while some of it is due and unread, and otherwise at
// their arrival. A source whose rate is off the mixer's by more than those
// bounds allow is placed again at its arrival each time it leaves them.
// Arrival is the source's arrival plus its hold (arrivalHold) when held
// is set. sampleRate is the source's (0: the mixer rate).
func (m *Mixer) stampArrival(ch *Channel, n, sampleRate int, held bool) {
	ns := func(samples int64) int64 { return samples * 1_000_000_000 / int64(m.sampleRate) }
	now := clock.TAINow()
	var hold int64
	if held {
		var readEnd int64
		if m.readEnd != 0 {
			readEnd = clock.IndexToTimestamp(clock.Rate{Num: int64(m.sampleRate), Den: 1}, m.readEnd-uint64(m.delaySamples(ch)))
		}
		if readEnd != 0 && ch.ringEnd != 0 && readEnd > ch.ringEnd {
			// The read point passed the end of the audio before this
			// push: the source ran dry, or would have, had it been read.
			// An off-program source is not read, and is held all the same,
			// so it does not run dry once it is cut to program.
			ch.hold.dryPushes++
		}
		ch.hold.note(now, ns(int64(n)), readEnd, ch.hold.dryPushes, ch.ringEnd != 0)
		hold = ch.hold.ticks.Load()
	}
	moved := held && ch.hold.moved != 0
	if moved {
		// The audio before this push moves with the hold, onto the
		// arrival of the pushes on the source's cadence plus the hold, so
		// the push continues it, late or not, and the next read places it
		// again: the source steps once, and a placement a late push left
		// behind is undone with it.
		ch.ringEnd += ticksNs(ch.hold.moved) - ch.hold.lastOff
		ch.hold.moved, ch.placed = 0, false
	}
	pts := clock.ArrivalPTS(n, m.sampleRate) + hold
	if ch.ringEnd != 0 {
		slack := ns(arrivalSlack * m.primingSamples(sampleRate))
		late := lateArrival()
		if moved || ch.active && ch.ringBuf.FrameCount()-m.delaySamples(ch) > n {
			// Audio before this push is due and unread, so no read has
			// passed the place that continues it: a push that came late
			// is jitter, and placed at its arrival it would move all of
			// that audio later. A channel's delay is held unread and an
			// inactive channel is never read, so neither shows that. A
			// lost AAC frame continued over plays the audio after it at
			// most one frame early, and only from a source placed more
			// than a frame ahead of its arrival: one on its own cadence
			// has no audio left unread a lost frame later.
			late = slack
		}
		if d := clock.TimestampOf(pts) - ch.ringEnd; d <= late && d >= -slack {
			ch.ringEnd += ns(int64(n))
			ch.hold.placed(now, ch.ringEnd)
			return
		}
	}
	m.stampRing(ch, pts, n)
	ch.hold.placed(now, ch.ringEnd)
}

// primingSamples is one AAC frame at the mixer rate, for a source at
// sampleRate (0: the mixer rate).
func (m *Mixer) primingSamples(sampleRate int) int64 {
	if sampleRate != 0 && sampleRate != m.sampleRate {
		return int64(aacPrimingSamples * m.sampleRate / sampleRate)
	}
	return aacPrimingSamples
}
