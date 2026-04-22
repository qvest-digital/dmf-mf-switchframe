package switcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/audio"
)

func TestSwitcher_FTBDurationDefaultsTo1000(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	require.Equal(t, 1000, sw.FTBDurationMs())
	require.Equal(t, 1000, sw.State().FTBDurationMs)
}

func TestSwitcher_SetFTBDurationValidatesRange(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	require.True(t, errors.Is(sw.SetFTBDuration(99), ErrInvalidFTBDuration))
	require.True(t, errors.Is(sw.SetFTBDuration(5001), ErrInvalidFTBDuration))
	require.True(t, errors.Is(sw.SetFTBDuration(0), ErrInvalidFTBDuration))
	require.True(t, errors.Is(sw.SetFTBDuration(-100), ErrInvalidFTBDuration))

	require.NoError(t, sw.SetFTBDuration(100))
	require.Equal(t, 100, sw.FTBDurationMs())
	require.Equal(t, 100, sw.State().FTBDurationMs)

	require.NoError(t, sw.SetFTBDuration(5000))
	require.Equal(t, 5000, sw.FTBDurationMs())

	require.NoError(t, sw.SetFTBDuration(1500))
	require.Equal(t, 1500, sw.FTBDurationMs())
}

func TestSwitcherFTB_RespectsConfiguredDuration(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	audioTrans := &mockAudioTransHandler{}
	sw.SetAudioTransition(audioTrans)

	require.NoError(t, sw.SetFTBDuration(2000))
	require.NoError(t, sw.FadeToBlack(context.Background()))

	audioTrans.mu.Lock()
	require.Len(t, audioTrans.startCalls, 1)
	require.Equal(t, 2000, audioTrans.startCalls[0].durationMs)
	require.Equal(t, audio.FadeOut, audioTrans.startCalls[0].mode)
	audioTrans.mu.Unlock()
}

func TestSwitcherFTB_ReverseDurationSymmetric(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	audioTrans := &mockAudioTransHandler{}
	sw.SetAudioTransition(audioTrans)

	require.NoError(t, sw.SetFTBDuration(1500))

	require.NoError(t, sw.FadeToBlack(context.Background()))
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 1.0))
	time.Sleep(20 * time.Millisecond)

	require.NoError(t, sw.FadeToBlack(context.Background()))

	audioTrans.mu.Lock()
	require.Len(t, audioTrans.startCalls, 2, "forward + reverse legs")
	require.Equal(t, 1500, audioTrans.startCalls[0].durationMs, "forward leg duration")
	require.Equal(t, audio.FadeOut, audioTrans.startCalls[0].mode)
	require.Equal(t, 1500, audioTrans.startCalls[1].durationMs, "reverse leg duration")
	require.Equal(t, audio.FadeIn, audioTrans.startCalls[1].mode)
	audioTrans.mu.Unlock()
}

func TestSwitcherFTB_MidFlightDurationChangeDoesNotRetimeLeg(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	audioTrans := &mockAudioTransHandler{}
	sw.SetAudioTransition(audioTrans)

	require.NoError(t, sw.SetFTBDuration(2000))
	require.NoError(t, sw.FadeToBlack(context.Background()))

	require.NoError(t, sw.SetFTBDuration(500))

	audioTrans.mu.Lock()
	require.Equal(t, 2000, audioTrans.startCalls[0].durationMs)
	audioTrans.mu.Unlock()
}
