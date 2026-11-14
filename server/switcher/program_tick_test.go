package switcher

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/clock"
)

// orderLog records the program tick calls and the mixer reads in their
// order.
type orderLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *orderLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, s)
}

func (l *orderLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.calls
	l.calls = nil
	return c
}

type orderedMixer struct{ log *orderLog }

func (m orderedMixer) ProduceSamples(uint64, int, int64) []float32 {
	m.log.add("audio")
	return nil
}
func (m orderedMixer) AdvanceCrossfade() {}

// The program tick call names each source a tick shows as program that
// the tick before did not, once, with the tick's read point, and before
// the tick's audio is read: a source started on it, as a cued clip player
// is, has its first audio read by that tick. A transition shows its
// incoming source from its first tick; a cut to the source already shown
// names nothing.
func TestProgramTickNamesEachSourceTheTickPutsOnAir(t *testing.T) {
	s := tickSwitcher(t)
	recordPipeline(t, s)
	s.SetTransitionConfig(TransitionConfig{})
	log := &orderLog{}
	s.SetSyncMixer(orderedMixer{log})
	s.SetAACBuffer(clock.NewAACFrameBuffer(2, 48000, mockAACEncode))
	var reads []int64
	s.SetOnProgramTick(func(key string, readPTS int64) {
		log.add("on air " + key)
		reads = append(reads, readPTS)
	})
	s.RegisterMXLSource("cam1")
	s.RegisterMXLSource("cam2")

	g := grainNow(s)
	tick := func() []string {
		tk := tickAt(s, g)
		g++
		s.processTick(tk)
		return log.take()
	}
	require.Equal(t, []string{"audio"}, tick(), "nothing on program")

	require.NoError(t, s.Cut(context.Background(), "cam1"))
	require.Empty(t, log.take(), "the cut itself names nothing")
	tk := tickAt(s, g)
	require.Equal(t, []string{"on air cam1", "audio"}, tick())
	require.Equal(t, []int64{tk.ReadPTS}, reads)
	require.Equal(t, []string{"audio"}, tick(), "a source is named once")

	require.NoError(t, s.Cut(context.Background(), "cam1"))
	require.Equal(t, []string{"audio"}, tick())

	require.NoError(t, s.StartTransition(context.Background(), "cam2", "mix", 60000, ""))
	require.Equal(t, []string{"on air cam2", "audio"}, tick(), "a mix shows its incoming source from its first tick")
	require.Equal(t, []string{"audio"}, tick())

	// The mix cut short to its incoming source: shown already.
	require.NoError(t, s.Cut(context.Background(), "cam2"))
	require.Equal(t, []string{"audio"}, tick())
	require.NoError(t, s.Cut(context.Background(), "cam1"))
	require.Equal(t, []string{"on air cam1", "audio"}, tick())
}
