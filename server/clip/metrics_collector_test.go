package clip

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/internal/metricstest"
)

// Every player reports a one-hot state, and position only once a clip is
// in it. Clip names stay out: they are operator text.
func TestMetricsCollector_Players(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))

	f := metricstest.Series(t, NewMetricsCollector(mgr))
	state := f["switchframe_clip_player_state"]
	require.Equal(t, float64(1), state["1/loaded"])
	require.Equal(t, float64(0), state["1/empty"])
	require.Equal(t, float64(1), state["2/empty"])
	require.Len(t, state, MaxPlayers*len(playerStates))
	require.Contains(t, f["switchframe_clip_player_position_ratio"], "1")
	require.NotContains(t, f["switchframe_clip_player_position_ratio"], "2")
	require.NotContains(t, f, "switchframe_clip_player_speed", "nothing plays")
}

// A playing clip reports its speed; one that stops playing drops it.
func TestMetricsCollector_PlayingSpeed(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	c := addTestClip(t, store, dir)
	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.Play(1, 0.25, true)) // slow + loop: stays playing

	col := NewMetricsCollector(mgr)
	require.Eventually(t, func() bool {
		return metricstest.Series(t, col)["switchframe_clip_player_state"]["1/playing"] == 1
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, map[string]float64{"1": 0.25}, metricstest.Series(t, col)["switchframe_clip_player_speed"])

	require.NoError(t, mgr.Pause(1))
	require.Eventually(t, func() bool {
		_, ok := metricstest.Series(t, col)["switchframe_clip_player_speed"]
		return !ok
	}, 2*time.Second, 10*time.Millisecond)
}

// A player logs a late start against its own output period: the
// pipeline's, or without an output rate its clip's. A start just past
// the period (two for audio) is late, one just short of it is not.
func TestManagerStartIsLateAgainstThePlayerPeriod(t *testing.T) {
	type tc struct {
		name     string
		clipRate clock.Rate
		output   func() (int, int)
		want     clock.Rate
	}
	var cases []tc
	for _, r := range programRates {
		cases = append(cases, tc{
			name:     fmt.Sprintf("output_%d_%d", r.Num, r.Den),
			clipRate: clock.Rate{Num: 50, Den: 1},
			output:   func() (int, int) { return int(r.Num), int(r.Den) },
			want:     r,
		})
		cases = append(cases, tc{name: fmt.Sprintf("clip_%d_%d", r.Num, r.Den), clipRate: r, want: r})
	}

	var mu sync.Mutex
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(lockedWriter{&mu, &buf}, nil)))
	defer slog.SetDefault(prev)
	logged := func() string {
		mu.Lock()
		defer mu.Unlock()
		s := buf.String()
		buf.Reset()
		return s
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			frames := make([]BufferedFrame, 30)
			for i := range frames {
				pts := (int64(i)*90000*c.clipRate.Den + c.clipRate.Num/2) / c.clipRate.Num
				frames[i] = BufferedFrame{wireData: []byte{0}, pts: pts, isKeyframe: i == 0}
			}
			m := NewManager(nil, ManagerConfig{OutputRate: c.output})
			m.mu.Lock()
			p, closer, err := m.newPlayer(0, &playerSlot{clip: &Clip{}, frames: frames}, 1, false, false)
			m.mu.Unlock()
			require.NoError(t, err)
			if closer != nil {
				defer closer()
			}
			period := time.Second * time.Duration(c.want.Den) / time.Duration(c.want.Num)
			for _, s := range []struct {
				media string
				delay time.Duration
				want  string
			}{
				{"video", period * 102 / 100, `msg="clip: late start"`},
				{"video", period * 98 / 100, `msg="clip: started"`},
				{"audio", 2 * period * 102 / 100, `msg="clip: late start audio"`},
				{"audio", 2 * period * 98 / 100, ""},
			} {
				logged()
				p.config.OnStart(s.media, s.delay, "ready")
				got := logged()
				if s.want == "" {
					require.NotContains(t, got, "clip:", "%s start %v after a %v period", s.media, s.delay, period)
					continue
				}
				require.Contains(t, got, s.want, "%s start %v after a %v period", s.media, s.delay, period)
			}
		})
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}
