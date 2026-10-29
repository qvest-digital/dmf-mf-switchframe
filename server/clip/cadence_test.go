package clip

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slotCounts returns how many output slots each of the first n pictures
// occupies.
func slotCounts(c cadence, n int) []int {
	out := make([]int, n)
	for k := range out {
		out[k] = c.slots(k)
	}
	return out
}

// A clip at a rate other than the output's repeats or skips pictures in a
// pattern fixed by the two rates, not by when each picture happened to be
// ready: an output sampling a clip paced on a clock of its own shows some
// pictures for three slots and others for one.
func TestCadence_BroadcastRates(t *testing.T) {
	for _, tc := range []struct {
		name           string
		outNum, outDen int
		srcFPS         float64
		speed          float64
		cycle          []int // repeats exactly, from the first picture
		allowed        []int // slots any picture may take, the rarer first, when no short cycle exists
		pictures       int
		slots          int // slots of the first pictures pictures
	}{
		{name: "25 on 50", outNum: 50, outDen: 1, srcFPS: 25, speed: 1, cycle: []int{2}},
		{name: "30 on 50", outNum: 50, outDen: 1, srcFPS: 30, speed: 1, cycle: []int{2, 2, 1}},
		{name: "50 on 50", outNum: 50, outDen: 1, srcFPS: 50, speed: 1, cycle: []int{1}},
		{name: "60 on 50", outNum: 50, outDen: 1, srcFPS: 60, speed: 1, cycle: []int{1, 1, 1, 1, 1, 0}},
		{name: "29.97 on 50", outNum: 50, outDen: 1, srcFPS: 29.97, speed: 1, allowed: []int{1, 2}, pictures: 30000, slots: 50050},
		{name: "59.94 on 50", outNum: 50, outDen: 1, srcFPS: 59.94, speed: 1, allowed: []int{0, 1}, pictures: 60000, slots: 50050},
		{name: "29.97 on 59.94", outNum: 60000, outDen: 1001, srcFPS: 29.97, speed: 1, cycle: []int{2}},
		{name: "30 on 50 at half speed", outNum: 50, outDen: 1, srcFPS: 30, speed: 0.5, cycle: []int{4, 3, 3}},
		{name: "30 on 50 at double speed", outNum: 50, outDen: 1, srcFPS: 30, speed: 2, cycle: []int{1, 1, 1, 1, 1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			num, den := clipRational(tc.srcFPS)
			c := newCadence(tc.outNum, tc.outDen, num, den, tc.speed)
			if tc.cycle != nil {
				got := slotCounts(c, 10*len(tc.cycle))
				for k, n := range got {
					require.Equal(t, tc.cycle[k%len(tc.cycle)], n, "picture %d of %v", k, got)
				}
				return
			}
			got := slotCounts(c, tc.pictures)
			for k, n := range got {
				require.Contains(t, tc.allowed, n, "picture %d", k)
				// Repeats or skips are spread out: the rarer count, the
				// first allowed, never comes twice in a row.
				if k > 0 && n == tc.allowed[0] {
					require.NotEqual(t, n, got[k-1], "pictures %d and %d", k-1, k)
				}
			}
			require.Equal(t, tc.slots, c.start(tc.pictures), "slots for %d pictures", tc.pictures)
		})
	}
}

// The player outputs at the output rate: a 30 fps clip on a 50 fps output
// shows its pictures for 2, 2 and 1 slots in turn, one slot per output
// period, sends each picture to the browser relay once, and emits all of
// its audio.
func TestPlayer_OutputsClipAtOutputRate(t *testing.T) {
	const pictures = 12
	clip := make([]BufferedFrame, pictures)
	audio := make([]BufferedAudioFrame, pictures)
	for i := range clip {
		clip[i] = BufferedFrame{wireData: []byte{byte(i)}, pts: int64(i) * 3000, isKeyframe: i == 0}
		audio[i] = BufferedAudioFrame{data: []byte{byte(i)}, sampleRate: 48000, channels: 2}
	}

	var mu sync.Mutex
	var shown []byte
	var at []time.Time
	relayed := map[byte]int{}
	audioOut := 0
	done := make(chan struct{})
	p := NewPlayer(PlayerConfig{
		Clip:         clip,
		AudioClip:    audio,
		Speed:        1,
		OutputFPSNum: 50,
		OutputFPSDen: 1,
		RawVideoOutput: func(yuv []byte, _, _ int, _ int64, _ bool) {
			mu.Lock()
			shown = append(shown, yuv[0])
			at = append(at, time.Now())
			mu.Unlock()
		},
		VideoOutput: func(wire []byte, _ int64, _ bool, _, _ []byte) {
			mu.Lock()
			relayed[wire[0]]++
			mu.Unlock()
		},
		AudioOutput: func([]byte, int64, int, int) {
			mu.Lock()
			audioOut++
			mu.Unlock()
		},
		OnDone: func() { close(done) },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for playback")
	}
	cancel()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()
	var runs []int
	for i, v := range shown {
		if i > 0 && v == shown[i-1] {
			runs[len(runs)-1]++
			continue
		}
		require.Equal(t, byte(len(runs)), v, "pictures out of order: %v", shown)
		runs = append(runs, 1)
	}
	assert.Equal(t, []int{2, 2, 1, 2, 2, 1, 2, 2, 1, 2, 2, 1}, runs)
	for i := 1; i < len(at); i++ {
		assert.GreaterOrEqual(t, at[i].Sub(at[i-1]), 10*time.Millisecond, "slot %d", i)
	}
	for k := 0; k < pictures; k++ {
		assert.Equal(t, 1, relayed[byte(k)], "picture %d sent to the relay", k)
	}
	assert.Equal(t, pictures, audioOut, "audio frames emitted")
}

// One output period is 1501.5 ticks at 59.94 fps. A whole-tick step loses
// half a tick a slot, about 20 ms a minute against the audio, which runs on
// its own exact 90 kHz count; over an hour the PTS stays within a tick.
func TestPtsStep_NoDriftOverAnHour(t *testing.T) {
	for _, r := range [][2]int64{{60000, 1001}, {30000, 1001}, {24000, 1001}, {50, 1}, {60, 1}} {
		s := ptsStep{num: r[0], den: r[1]}
		slots := 3600 * r[0] / r[1]
		var pts int64
		for k := int64(0); k < slots; k++ {
			pts += s.next()
		}
		want := slots * 90000 * r[1] / r[0]
		assert.InDelta(t, want, pts, 1, "%d/%d fps after %d slots", r[0], r[1], slots)
	}
}

// The player stamps slot k of a 59.94 fps output k*1501.5 ticks after the
// first, to the tick.
func TestPlayer_OutputPTSFollowsOutputRateExactly(t *testing.T) {
	const pictures = 30
	clip := make([]BufferedFrame, pictures)
	for i := range clip {
		clip[i] = BufferedFrame{wireData: []byte{byte(i)}, pts: int64(i) * 1501, isKeyframe: i == 0}
	}
	var mu sync.Mutex
	var pts []int64
	done := make(chan struct{})
	p := NewPlayer(PlayerConfig{
		Clip: clip, Speed: 1, OutputFPSNum: 60000, OutputFPSDen: 1001, InitialPTS: 1000,
		RawVideoOutput: func(_ []byte, _, _ int, v int64, _ bool) { mu.Lock(); pts = append(pts, v); mu.Unlock() },
		OnDone:         func() { close(done) },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	<-done
	cancel()
	p.Wait()
	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(pts), pictures)
	for k := 1; k < pictures; k++ {
		assert.Equal(t, int64(k)*90000*1001/60000, pts[k]-pts[0], "slot %d", k)
	}
}
