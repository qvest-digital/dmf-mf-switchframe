package switcher

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/audio"
)

// orderLog records the mixer calls and the on-air calls in their order.
type orderLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *orderLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, s)
}

func (l *orderLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

type orderedAudioCut struct{ log *orderLog }

func (c orderedAudioCut) OnCut(oldSource, newSource string) { c.log.add("cut " + newSource) }
func (c orderedAudioCut) OnProgramChange(newProgramSource string) {
	c.log.add("program " + newProgramSource)
}

type orderedAudioTrans struct {
	mockAudioTransHandler
	log *orderLog
}

func (t *orderedAudioTrans) OnTransitionStart(oldSource, newSource string, mode audio.TransitionMode, durationMs int) uint64 {
	t.log.add("transition " + newSource)
	return t.mockAudioTransHandler.OnTransitionStart(oldSource, newSource, mode, durationMs)
}

// A source started by the on-air call, as a cued clip player is, delivers
// its first audio then: the mixer flushes the audio of the source a cut or
// a transition takes to program, so the call has to follow that flush or
// the first audio is lost.
func TestOnAirFollowsTheMixerOnCutAndTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	log := &orderLog{}
	sw.configMu.Lock()
	sw.audioCut = orderedAudioCut{log: log}
	sw.audioTransition = &orderedAudioTrans{log: log}
	sw.configMu.Unlock()
	sw.SetOnAir(func(key string) { log.add("on air " + key) })

	require.NoError(t, sw.Cut(context.Background(), "cam2"))
	require.Equal(t, []string{"cut cam2", "program cam2", "on air cam2"}, log.get())

	// A cut to the source already on program takes nothing to air.
	require.NoError(t, sw.Cut(context.Background(), "cam2"))
	require.Len(t, log.get(), 3)

	require.NoError(t, sw.StartTransition(context.Background(), "cam1", "mix", 60000, ""))
	require.Equal(t, []string{"cut cam2", "program cam2", "on air cam2", "transition cam1", "on air cam1"}, log.get())
}
