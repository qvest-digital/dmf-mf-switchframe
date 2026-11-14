package playout

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/internal/video"
)

// During a transition the blend carries the incoming item's encoded audio,
// and the outgoing item's on a tick the incoming one has none. A clip's AAC frames
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

// The playout crossfade runs on the mixer's curves for a switcher
// transition, so a playout transition sounds as the same transition taken
// on the switcher does.
func TestTransitionGainsAreTheMixers(t *testing.T) {
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	for _, mode := range []audio.TransitionMode{audio.Crossfade, audio.DipToSilence} {
		m.OnTransitionStart("a", "b", mode, 1000)
		for i := 0; i <= 1000; i++ {
			p := float64(i) / 1000
			m.OnTransitionPosition(p)
			from, to := m.TransitionGains()
			require.Equal(t, from, transitionFromGain(mode, p), "mode %d at %v: outgoing gain", mode, p)
			require.Equal(t, to, transitionToGain(mode, p), "mode %d at %v: incoming gain", mode, p)
		}
		m.OnTransitionComplete()
	}
}

// pcmIterator plays frames and reports their rate exactly, as a
// FileFrameIterator does: SliceIterator would estimate it, and clamp it.
type pcmIterator struct {
	*SliceIterator
	fps float64
}

func (it pcmIterator) EstimateFPS() float64 { return it.fps }

type pcmAssets map[string]pcmIterator

func (p pcmAssets) Open(uri string) (FrameIterator, error) {
	it, ok := p[uri]
	if !ok {
		return nil, fmt.Errorf("asset not found: %s", uri)
	}
	return pcmIterator{NewSliceIterator(it.frames), it.fps}, nil
}

// pcmClip is n pictures at fps, each with its share of rate Hz stereo PCM
// at level (l, r), as FileFrameIterator hands PCM out, up to sample
// audible.
func pcmClip(n int, fps float64, rate int, l, r float32, audible int64) pcmIterator {
	yuv := make([]byte, video.YUV420FrameSize(16, 16))
	frames := make([]DecodedFrame, n)
	at := func(i int) int64 { return int64(math.Round(float64(i) * float64(rate) / fps)) }
	for i := range frames {
		frames[i] = DecodedFrame{YUV: yuv, Width: 16, Height: 16, PTS: int64(math.Round(float64(i) * 90000 / fps)), IsKeyframe: i == 0}
		if at(i) >= audible {
			continue
		}
		pcm := make([]float32, 2*(min(at(i+1), audible)-at(i)))
		for k := 0; k < len(pcm); k += 2 {
			pcm[k], pcm[k+1] = l, r
		}
		frames[i].AudioFrames = []AudioFrameData{{PCM: pcm, PTS: at(i) * 90000 / int64(rate), SampleRate: rate, Channels: 2}}
	}
	return pcmIterator{NewSliceIterator(frames), fps}
}

// The channel and clip rates the tests run at.
var (
	channelRates = []float64{24000.0 / 1001, 24, 25, 30000.0 / 1001, 30, 50, 60000.0 / 1001, 60, 100, 120, 240}
	// A clip at another rate than the channel's: slower ones hold some
	// pictures and hand out a picture's PCM on the next, faster ones skip
	// some and hand out several pictures' PCM on one tick.
	clipOnChannelRates = [][2]float64{{25, 50}, {24000.0 / 1001, 60000.0 / 1001}, {24, 50}, {60, 50}, {25, 100}}
)

// A playout transition fades the two items' sound with the pictures: the
// outgoing item on the left channel, the incoming one on the right, each
// at the mixer's curve for the progress the picture of every tick is
// blended at, ramped per sample from one tick's to the next, so no gain
// steps, and handed on in time as one stream that the incoming item
// continues. An item without sound is silence under the curve. The
// transition used to cut the sound: the incoming item at full level from
// the first picture and the outgoing one gone, while the pictures mixed.
func TestPlayoutTransitionCrossfadesThePCM(t *testing.T) {
	cases := []pcmCase{
		{name: "mix", typ: "mix"},
		{name: "dip", typ: "dip"},
		{name: "outgoing silent", typ: "mix", out: noAudio},
		{name: "outgoing played out", typ: "mix", out: audioToTransition},
		{name: "incoming silent", typ: "mix", inSilent: true},
	}
	rates := []int{44100, 48000, 96000}
	if raceDetector {
		rates = []int{48000}
	}
	for _, tc := range cases {
		for _, fps := range channelRates {
			for _, rate := range rates {
				t.Run(fmt.Sprintf("%s/%.3ffps/%dHz", tc.name, fps, rate), func(t *testing.T) {
					tc := tc
					tc.channelFPS, tc.clipFPS, tc.rate, tc.pre = fps, fps, rate, 6
					checkTransitionPCM(t, tc)
				})
			}
		}
	}
}

// The same with clips at another rate than the channel's, the transition
// starting on every tick of the cycle the two rates run through: on a
// picture a slower clip holds, its PCM comes a tick late and the stream
// waits for it, and on the completing tick the outgoing clip is read on
// until its PCM covers the picture period, which the picture rates alone
// do not tell.
func TestPlayoutTransitionCrossfadesThePCMAcrossClipRates(t *testing.T) {
	cases := []pcmCase{
		{name: "mix", typ: "mix"},
		{name: "dip", typ: "dip"},
		{name: "outgoing silent", typ: "mix", out: noAudio},
		{name: "outgoing played out", typ: "mix", out: audioToTransition},
		{name: "incoming silent", typ: "mix", inSilent: true},
		// Over before the stream the outgoing clip handed out ahead of its
		// picture has played.
		{name: "one picture", typ: "mix", pictures: 1},
	}
	rates := []int{44100, 48000}
	if raceDetector {
		cases, rates = cases[:2], []int{48000}
	}
	for _, r := range clipOnChannelRates {
		clip, channel := r[0], r[1]
		cycle := clipCycle(clip, channel)
		for _, tc := range cases {
			for _, rate := range rates {
				for phase := range cycle {
					t.Run(fmt.Sprintf("%.3fon%.3f/%s/%dHz/tick%d", clip, channel, tc.name, rate, phase), func(t *testing.T) {
						tc := tc
						tc.channelFPS, tc.clipFPS, tc.rate, tc.pre = channel, clip, rate, 6+phase
						checkTransitionPCM(t, tc)
					})
				}
			}
		}
	}
}

// clipCycle is how many channel ticks the pictures of a clip at clip fps
// take to fall on the channel's at channel fps the same way again.
func clipCycle(clip, channel float64) int {
	for n := 1; n <= 1000; n++ {
		if k := float64(n) * clip / channel; math.Abs(k-math.Round(k)) < 1e-9 {
			return n
		}
	}
	panic("no cycle")
}

// audioTo is how far the outgoing item's audio runs.
type audioTo int

const (
	audioThroughout   audioTo = iota
	noAudio                   // the item has none
	audioToTransition         // it ends where the transition starts
)

type pcmCase struct {
	name, typ           string
	out                 audioTo
	inSilent            bool
	channelFPS, clipFPS float64
	rate                int
	pre                 int // the tick the transition starts on
	pictures            int // the transition's length, 400 ms if 0
}

func checkTransitionPCM(t *testing.T, tc pcmCase) {
	const post = 8
	fps, rate, pre := tc.channelFPS, tc.rate, tc.pre
	durMs := 400
	if tc.pictures > 0 {
		durMs = int(math.Ceil(float64(tc.pictures) * 1000 / fps))
	}
	ticks := pre + int(math.Ceil(float64(durMs)*fps/1000)) + post
	a, b := clipItem("a"), clipItem("b")
	b.Transition = &TransitionConfig{Type: tc.typ, DurationMs: durMs}
	pictures := int(float64(ticks+4)*tc.clipFPS/fps) + 4
	// The channel's tick i shows the picture of sample tickAt(i) on.
	tickAt := func(i int) int64 { return int64(math.Round(float64(i) * float64(rate) / fps)) }
	outAudible := map[audioTo]int64{audioThroughout: math.MaxInt64, noAudio: 0, audioToTransition: tickAt(pre)}[tc.out]
	inAudible := int64(math.MaxInt64)
	if tc.inSilent {
		inAudible = 0
	}
	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"},
		AssetProvider: pcmAssets{
			a.AssetURI: pcmClip(pictures, tc.clipFPS, rate, 1, 0, outAudible),
			b.AssetURI: pcmClip(pictures, tc.clipFPS, rate, 0, 1, inAudible),
		},
		Width: 16, Height: 16, FPS: fps,
	})
	progress := math.NaN()
	ch.compositor = NewCompositor(16, 16, func(_, _, _ []byte, _, _ int, p float32, _ string) { progress = float64(p) })
	ch.SetRawVideoOutput(func([]byte, int, int, int64, bool) {})

	// The stream as the mixer reads it: a chunk that continues the one
	// before follows it, a start is placed by its PTS.
	var l, r []float32
	starts, tick, firstTick := 0, 0, -1
	var firstPTS int64
	var next float64
	ch.SetPCMOutput(func(pcm []float32, pts int64, channels int, start bool) {
		require.Equal(t, 2, channels)
		at := len(l)
		if firstTick < 0 {
			firstTick, firstPTS, next = tick, pts, float64(pts)
		}
		if start || math.Abs(float64(pts)-next) > 2 {
			at = int(math.Round(float64(pts-firstPTS) * float64(rate) / 90000))
		}
		if start {
			starts++
		}
		for len(l) < at+len(pcm)/2 {
			l, r = append(l, 0), append(r, 0)
		}
		for k := 0; k < len(pcm)/2; k++ {
			l[at+k], r[at+k] = pcm[2*k], pcm[2*k+1]
		}
		next = float64(pts) + float64(len(pcm)/2)*90000/float64(rate)
	})

	require.NoError(t, ch.LoadPlaylist(testPlaylist(a, b)))
	ch.mu.Lock()
	ch.state = StateOnAir
	ch.scheduler.Start()
	ch.mu.Unlock()
	t0 := time.Now()
	var prog []float64 // per tick, NaN where the pictures are not blended
	var streamed []int // per tick, the samples of the stream out after it
	before := 0        // the samples out before the transition
	for tick = 0; tick < ticks; tick++ {
		if tick == pre {
			require.NoError(t, ch.Next())
			before = len(l)
		}
		progress = math.NaN()
		ch.tick(t0.Add(time.Duration(math.Round(float64(tick) * 1e9 / fps))))
		prog = append(prog, progress)
		streamed = append(streamed, len(l))
	}
	first, last := -1, -1
	for i, p := range prog {
		if !math.IsNaN(p) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	require.Equal(t, pre, first, "the transition starts on the Next")
	require.Less(t, last+1, ticks, "the transition completes")

	// knot is the last sample of tick i's picture, the tick's gains, as
	// stream positions run from the picture of firstTick.
	origin := tickAt(firstTick)
	knot := func(i int) int { return int(tickAt(i+1)-origin) - 1 }
	mode := transitionAudioMode(tc.typ)
	type gains struct{ out, in float64 }
	want := map[int]gains{}
	for i := first; i <= last; i++ {
		want[i] = gains{transitionFromGain(mode, prog[i]), transitionToGain(mode, prog[i])}
	}
	want[last+1] = gains{0, 1}
	// The incoming item fades in over a picture period at least past what
	// went out before the transition.
	finalKnot := max(knot(last+1), before-1+knot(last+1)-knot(last))

	// The gain runs through each tick's knot in straight lines, from the
	// samples out before the transition, which a knot that falls among
	// them does not move. No sample steps further than the steepest line.
	// An item's PCM runs on its own picture grid, rounded to the sample,
	// so a knot may land a sample off.
	type point struct {
		pos int
		gains
	}
	path := []point{{before - 1, gains{1, 0}}}
	for i := first; i <= last; i++ {
		if knot(i) >= before && knot(i) < finalKnot {
			path = append(path, point{knot(i), want[i]})
		}
	}
	path = append(path, point{finalKnot, want[last+1]})
	maxStep := 0.0
	for k := 1; k < len(path); k++ {
		n := float64(path[k].pos - path[k-1].pos - 1)
		maxStep = max(maxStep, math.Abs(path[k].out-path[k-1].out)/n, math.Abs(path[k].in-path[k-1].in)/n)
	}
	outSilent := tc.out != audioThroughout
	for _, p := range path[1:] {
		require.Greater(t, len(l), p.pos, "the stream ends before sample %d", p.pos)
		wantOut, wantIn := p.out, p.in
		if outSilent {
			wantOut = 0
		}
		if tc.inSilent {
			wantIn = 0
		}
		require.InDelta(t, wantOut, l[p.pos], maxStep+1e-5, "sample %d, a tick's last: outgoing gain", p.pos)
		require.InDelta(t, wantIn, r[p.pos], maxStep+1e-5, "sample %d, a tick's last: incoming gain", p.pos)
	}
	for k := 1; k < len(l); k++ {
		dl, dr := math.Abs(float64(l[k]-l[k-1])), math.Abs(float64(r[k]-r[k-1]))
		if tc.out == audioToTransition && k == int(outAudible-origin) {
			dl = 0 // the item's sound ends here, not its gain
		}
		if max(dl, dr) > maxStep+1e-5 {
			t.Fatalf("the gain steps at sample %d of %d: outgoing %v -> %v, incoming %v -> %v, at most %v a sample",
				k, len(l), l[k-1], l[k], r[k-1], r[k], maxStep)
		}
	}
	// Each tick hands out the stream to at least half a picture before
	// its own, ahead of the mixer, which reads two pictures behind.
	lastAudible := ticks - 1
	if tc.inSilent {
		lastAudible = last
	}
	for i := max(firstTick, first); i <= lastAudible; i++ {
		due := int(tickAt(i)-origin) - int(tickAt(i+1)-tickAt(i))/2 - 1
		require.GreaterOrEqual(t, streamed[i], due, "tick %d: the stream is behind its picture", i)
	}
	require.Equal(t, 1, starts, "the sound is one stream")
	if tc.out == noAudio {
		require.Equal(t, first, firstTick, "the stream starts with the transition")
	} else {
		require.Equal(t, 0, firstTick, "the stream starts with the outgoing item")
	}
	end := finalKnot
	if tc.inSilent {
		require.InDelta(t, end+1, len(l), 1, "the outgoing item's sound stops where its gain reaches 0")
		require.InDelta(t, 0, l[len(l)-1], 1e-6, "the outgoing item's last sample")
		return
	}
	clipPicture := int(math.Ceil(float64(rate) / tc.clipFPS))
	require.LessOrEqual(t, len(l), knot(ticks-1)+1+clipPicture+1, "the incoming item's sound runs ahead of the last tick")
	for k := end; k < len(l); k++ {
		if math.Abs(float64(l[k])) > maxStep+1e-6 || math.Abs(float64(r[k])-1) > maxStep+1e-6 {
			t.Fatalf("sample %d after the transition: outgoing %v, incoming %v", k, l[k], r[k])
		}
	}
}

// A side whose PCM changes channel count is laid out in the count it
// started with: mono on the front pair, others channel for channel.
func TestRemapChannels(t *testing.T) {
	require.Equal(t, []float32{1, 1, 0, 0, 2, 2, 0, 0}, remapChannels([]float32{1, 2}, 1, 4))
	require.Equal(t, []float32{1, 2, 0, 0, 0, 0}, remapChannels([]float32{1, 2}, 2, 6))
	require.Equal(t, []float32{1, 2, 3, 4}, remapChannels([]float32{1, 2, 3, 4}, 2, 2))
}

// withLayout gives every chunk of it PCM of len(levels) channels at those
// levels.
func withLayout(it pcmIterator, levels ...float32) pcmIterator {
	frames := make([]DecodedFrame, len(it.frames))
	copy(frames, it.frames)
	for i := range frames {
		var afs []AudioFrameData
		for _, af := range frames[i].AudioFrames {
			n := len(af.PCM) / af.Channels
			pcm := make([]float32, n*len(levels))
			for k := range pcm {
				pcm[k] = levels[k%len(levels)]
			}
			af.PCM, af.Channels = pcm, len(levels)
			afs = append(afs, af)
		}
		frames[i].AudioFrames = afs
	}
	return pcmIterator{NewSliceIterator(frames), it.fps}
}

// A transition between a 5.1 item and a stereo one carries 5.1, so the
// outgoing item's centre, where dialogue is, fades out with the rest of
// it rather than going at the first transition picture. After the
// transition the stream is the stereo item's own.
func TestPlayoutTransitionCarriesTheWiderLayout(t *testing.T) {
	a, b := clipItem("a"), clipItem("b")
	b.Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
	ch, tick, chunks := pcmChannel(t, pcmAssets{
		a.AssetURI: withLayout(pcmClip(100, 50, 48000, 0, 0, math.MaxInt64), 0, 0, 1, 0, 0, 0),
		b.AssetURI: pcmClip(100, 50, 48000, 1, 1, math.MaxInt64),
	}, a, b)
	var during []pcmChunk
	completed := -1
	for i := 0; i < 40; i++ {
		if i == 5 {
			require.NoError(t, ch.Next())
		}
		n := len(*chunks)
		tick(i)
		if ch.scheduler.State() == SchedTransitioning {
			during = append(during, (*chunks)[n:]...)
		} else if completed < 0 && i > 5 {
			completed = i
		}
	}
	require.NotEmpty(t, during, "the transition never ran")
	prev := float32(1)
	for _, k := range during {
		require.Equal(t, 6, k.channels, "tick %d: the stream's layout", k.tick)
		for s := 0; s < len(k.pcm); s += 6 {
			c, l := k.pcm[s+2], k.pcm[s]
			require.InDelta(t, prev, c, 1e-3, "tick %d, sample %d: the outgoing centre steps", k.tick, s/6)
			prev = c
			// Straight between the knots of a 20-picture curve, the gains
			// lose up to 0.2 % of power mid-picture.
			require.InDelta(t, 1, c*c+l*l, 5e-3, "tick %d, sample %d: centre %v and front %v are not at equal power", k.tick, s/6, c, l)
		}
	}
	require.Less(t, prev, float32(0.2), "the outgoing centre fades out")
	for _, k := range *chunks {
		if k.tick > completed {
			require.Equal(t, 2, k.channels, "tick %d: the stereo item's own layout after the transition", k.tick)
		}
	}
}

// pcmChunk is one call of a channel's PCM output, on tick tick.
type pcmChunk struct {
	tick     int
	pcm      []float32
	pts      int64
	channels int
	start    bool
}

// pcmChannel puts a 50 fps channel playing items from assets on air,
// recording its PCM output into chunks with the tick it went out on.
func pcmChannel(t *testing.T, assets AssetProvider, items ...PlaylistItem) (ch *Channel, tick func(i int), chunks *[]pcmChunk) {
	t.Helper()
	ch = NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: assets, Width: 16, Height: 16, FPS: 50})
	ch.compositor = NewCompositor(16, 16, func(_, _, _ []byte, _, _ int, _ float32, _ string) {})
	ch.SetRawVideoOutput(func([]byte, int, int, int64, bool) {})
	var out []pcmChunk
	at := 0
	ch.SetPCMOutput(func(pcm []float32, pts int64, channels int, start bool) {
		out = append(out, pcmChunk{at, append([]float32(nil), pcm...), pts, channels, start})
	})
	require.NoError(t, ch.LoadPlaylist(testPlaylist(items...)))
	ch.mu.Lock()
	ch.state = StateOnAir
	ch.scheduler.Start()
	ch.mu.Unlock()
	t0 := time.Now()
	return ch, func(i int) {
		at = i
		ch.tick(t0.Add(time.Duration(i) * 20 * time.Millisecond))
	}, &out
}

// A transition that cuts part-way, as a GPU-aliased live side does, cuts
// the audio with the pictures: the outgoing item's sound stops on the cut
// and does not come back to fade out when the transition completes.
func TestPlayoutTransitionCutStopsTheOutgoingSound(t *testing.T) {
	const cutAt = 10
	a, b := clipItem("a"), liveItem("b", "cam")
	b.Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
	ch, tick, chunks := pcmChannel(t, pcmAssets{a.AssetURI: pcmClip(100, 50, 48000, 1, 0, math.MaxInt64)}, a, b)
	aliased := false
	ch.SetGPUAlias(func(string) bool { return aliased })
	live := &DecodedFrame{YUV: make([]byte, video.YUV420FrameSize(16, 16)), Width: 16, Height: 16}
	ch.SetGPUSourceTap(func(string) *DecodedFrame { return live })

	completed := -1
	for i := 0; i < 40; i++ {
		if i == 5 {
			require.NoError(t, ch.Next())
		}
		aliased = i >= cutAt
		tick(i)
		if completed < 0 && i > 5 && ch.scheduler.State() != SchedTransitioning {
			completed = i
		}
	}
	require.Greater(t, completed, cutAt, "the transition completes after the cut")
	heard := false
	for _, c := range *chunks {
		if c.tick < cutAt {
			heard = heard || c.pcm[0] > 0
			continue
		}
		t.Fatalf("PCM went out on tick %d, after the cut on tick %d (completed on %d): %d samples from %v",
			c.tick, cutAt, completed, len(c.pcm)/c.channels, c.pcm[0])
	}
	require.True(t, heard, "the outgoing item's sound went out before the cut")
}

// shiftPCM moves the PCM timestamps of it by d.
func shiftPCM(it pcmIterator, d int64) pcmIterator {
	frames := make([]DecodedFrame, len(it.frames))
	copy(frames, it.frames)
	for i := range frames {
		afs := append([]AudioFrameData(nil), frames[i].AudioFrames...)
		for k := range afs {
			afs[k].PTS += d
		}
		frames[i].AudioFrames = afs
	}
	return pcmIterator{NewSliceIterator(frames), it.fps}
}

// A transition left for another item, by a jump, hands that item's sound
// out as it is, as a stream of its own placed on its first picture, and
// leaves nothing behind that fades the incoming item's sound when it
// plays later. The jumped-to item's PCM timestamps here continue those
// the outgoing item had reached when the transition started, which on
// their own would make it carry on that stream.
func TestPlayoutTransitionLeftForAnotherItem(t *testing.T) {
	a, b, c := clipItem("a"), clipItem("b"), clipItem("c")
	b.Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
	const next = 5
	ch, tick, chunks := pcmChannel(t, pcmAssets{
		a.AssetURI: pcmClip(100, 50, 48000, 1, 0, math.MaxInt64),
		b.AssetURI: pcmClip(100, 50, 48000, 0, 1, math.MaxInt64),
		c.AssetURI: shiftPCM(pcmClip(100, 50, 48000, 0.25, 0.25, math.MaxInt64), next*90000/50),
	}, a, b, c)
	const jumpC, jumpB = 10, 15
	for i := 0; i < 25; i++ {
		switch i {
		case next:
			require.NoError(t, ch.Next())
		case jumpC:
			require.Equal(t, SchedTransitioning, ch.scheduler.State(), "the jump is mid-transition")
			require.NoError(t, ch.JumpTo("c"))
		case jumpB:
			require.NoError(t, ch.JumpTo("b"))
		}
		tick(i)
	}
	seen := map[int]bool{}
	for _, k := range *chunks {
		if k.tick < jumpC {
			continue
		}
		if k.tick == jumpC || k.tick == jumpB {
			if !seen[k.tick] {
				require.True(t, k.start, "tick %d: the item's sound starts a stream of its own", k.tick)
				require.Equal(t, int64(k.tick)*90000/50, k.pts, "tick %d: the item's sound starts on its first picture", k.tick)
			}
			seen[k.tick] = true
		}
		want := [2]float32{0.25, 0.25}
		if k.tick >= jumpB {
			want = [2]float32{0, 1}
		}
		for s := 0; s < len(k.pcm); s += 2 {
			if k.pcm[s] != want[0] || k.pcm[s+1] != want[1] {
				t.Fatalf("tick %d, sample %d: %v %v, want the item's own level %v", k.tick, s/2, k.pcm[s], k.pcm[s+1], want)
			}
		}
	}
}

// PCM at a rate other than the stream's is not mixed into it: the stream
// carries one rate, and samples of another placed on it would play at the
// wrong speed. FileFrameIterator hands all its PCM out at audioRate, so
// only another iterator can mix rates.
func TestPlayoutTransitionSkipsPCMAtAnotherRate(t *testing.T) {
	a, b := clipItem("a"), clipItem("b")
	b.Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
	ch, tick, chunks := pcmChannel(t, pcmAssets{
		a.AssetURI: pcmClip(100, 50, 48000, 1, 0, math.MaxInt64),
		b.AssetURI: pcmClip(100, 50, 96000, 0, 1, math.MaxInt64),
	}, a, b)
	completed := -1
	for i := 0; i < 40; i++ {
		if i == 5 {
			require.NoError(t, ch.Next())
		}
		tick(i)
		if completed < 0 && i > 5 && ch.scheduler.State() != SchedTransitioning {
			completed = i
		}
	}
	require.Greater(t, completed, 5, "the transition completes")
	for _, k := range *chunks {
		if k.tick > completed {
			break
		}
		for s := 1; s < len(k.pcm); s += 2 {
			if k.pcm[s] != 0 {
				t.Fatalf("tick %d, sample %d: the 96 kHz item's sound %v is in the 48 kHz stream", k.tick, s/2, k.pcm[s])
			}
		}
	}
}

// The transition's picture types: a dip goes through silence, as on the
// switcher, which dips its audio only for a dip; every other blend, and
// a type the compositor cuts, crossfades.
func TestTransitionAudioModeIsTheSwitchers(t *testing.T) {
	for _, typ := range []string{"dip", "dip_black", "dip_white"} {
		require.Equal(t, audio.DipToSilence, transitionAudioMode(typ), typ)
	}
	for _, typ := range []string{"mix", "dissolve", "wipe", "dve-push", "", "unknown"} {
		require.Equal(t, audio.Crossfade, transitionAudioMode(typ), typ)
	}
}

// A transition type the compositor does not know is a cut, on the CPU
// compositor as anywhere: its audio cuts with the pictures, the incoming
// item's at full level from the transition's first picture, as a stream
// of its own, with nothing of the outgoing item's after it.
func TestPlayoutUnknownTransitionCutsTheSound(t *testing.T) {
	a, b := clipItem("a"), clipItem("b")
	b.Transition = &TransitionConfig{Type: "no-such-type", DurationMs: 400}
	ch, tick, chunks := pcmChannel(t, pcmAssets{
		a.AssetURI: pcmClip(100, 50, 48000, 1, 0, math.MaxInt64),
		b.AssetURI: pcmClip(100, 50, 48000, 0, 1, math.MaxInt64),
	}, a, b)
	ch.compositor = NewCompositor(16, 16, nil)
	const next = 5
	for i := 0; i < 30; i++ {
		if i == next {
			require.NoError(t, ch.Next())
		}
		tick(i)
	}
	cut := false
	for _, k := range *chunks {
		if k.tick < next {
			continue
		}
		if !cut {
			require.True(t, k.start, "tick %d: the incoming item's sound starts a stream of its own", k.tick)
			require.Equal(t, next, k.tick, "the sound cuts on the transition's first picture")
			cut = true
		}
		for s := 0; s < len(k.pcm); s += 2 {
			if k.pcm[s] != 0 || k.pcm[s+1] != 1 {
				t.Fatalf("tick %d, sample %d: %v %v, want the incoming item alone at full level", k.tick, s/2, k.pcm[s], k.pcm[s+1])
			}
		}
	}
	require.True(t, cut, "the incoming item's sound never went out")
}

// A transition into an item without sound leaves no stream behind for
// the next item's sound to run on: that starts a stream of its own on its
// first picture, even where its PCM timestamps happen to continue those
// of the silent item's nothing.
func TestPlayoutSoundAfterASilentIncomingItemStartsAfresh(t *testing.T) {
	a, b, c := clipItem("a"), clipItem("b"), clipItem("c")
	b.Transition = &TransitionConfig{Type: "mix", DurationMs: 200}
	ch, tick, chunks := pcmChannel(t, pcmAssets{
		a.AssetURI: pcmClip(100, 50, 48000, 1, 0, math.MaxInt64),
		b.AssetURI: pcmClip(20, 50, 48000, 0, 1, 0),
		c.AssetURI: pcmClip(100, 50, 48000, 0.5, 0.5, math.MaxInt64),
	}, a, b, c)
	cAt := -1
	for i := 0; i < 50; i++ {
		if i == 5 {
			require.NoError(t, ch.Next())
		}
		tick(i)
		if cAt < 0 && ch.Status().CurrentItemID == "c" {
			cAt = i
		}
	}
	require.Positive(t, cAt, "item c never played")
	for _, k := range *chunks {
		if k.pcm[0] == 0.5 {
			require.True(t, k.start, "tick %d: item c's sound starts a stream of its own", k.tick)
			return
		}
	}
	t.Fatal("item c's sound never went out")
}

// A channel stopped part-way through a transition and started again on
// the transition's incoming item plays that item's sound as a stream of
// its own from its first picture, at full level, with nothing of the
// crossfade the stop cut short.
func TestPlayoutRestartMidTransition(t *testing.T) {
	a, b := clipItem("a"), clipItem("b")
	b.Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
	ch, tick, chunks := pcmChannel(t, pcmAssets{
		a.AssetURI: pcmClip(100, 50, 48000, 1, 0, math.MaxInt64),
		b.AssetURI: pcmClip(100, 50, 48000, 0, 1, math.MaxInt64),
	}, a, b)
	const next, stop = 5, 10
	for i := 0; i < stop; i++ {
		if i == next {
			require.NoError(t, ch.Next())
		}
		tick(i)
	}
	require.Equal(t, SchedTransitioning, ch.scheduler.State(), "the stop is mid-transition")
	ch.Stop()
	ch.CueAt(1)
	// The run loop's ticker never fires: the test ticks the channel.
	ch.frameDuration = time.Hour
	require.NoError(t, ch.Start())
	t.Cleanup(ch.Stop)
	n := len(*chunks)
	for i := stop; i < stop+10; i++ {
		tick(i)
	}
	restarted := (*chunks)[n:]
	require.NotEmpty(t, restarted, "no sound after the restart")
	require.True(t, restarted[0].start, "the sound after the restart starts a stream of its own")
	require.Equal(t, stop, restarted[0].tick, "the sound starts on the first picture after the restart")
	require.Equal(t, int64(0), restarted[0].pts, "the sound starts on the restarted channel's first picture")
	for _, k := range restarted {
		for s := 0; s < len(k.pcm); s += 2 {
			if k.pcm[s] != 0 || k.pcm[s+1] != 1 {
				t.Fatalf("tick %d, sample %d: %v %v, want the item alone at full level", k.tick, s/2, k.pcm[s], k.pcm[s+1])
			}
		}
	}
}
