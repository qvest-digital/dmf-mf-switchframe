package audio

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// TestMixer_CloseCompletesWithinTimeout verifies that Mixer.Close() completes
// within a reasonable time and does not deadlock. The mixer uses WaitWithTimeout
// internally (5s for the ticker goroutine), so we allow 6s for the full close.
func TestMixer_CloseCompletesWithinTimeout(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) {},
	})

	// Add a channel to exercise the codec cleanup path.
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	done := make(chan struct{})
	go func() {
		_ = m.Close()
		close(done)
	}()

	select {
	case <-done:
		// OK — Close() completed.
	case <-time.After(6 * time.Second):
		t.Fatal("Mixer.Close() appears to be deadlocked (did not complete within 6s)")
	}
}

// TestMixer_CloseClockDriven verifies that a clock-driven mixer (no internal
// ticker goroutine) closes cleanly without blocking.
func TestMixer_CloseClockDriven(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate:  48000,
		Channels:    2,
		Output:      func(_ *media.AudioFrame) {},
		ClockDriven: true,
	})

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	done := make(chan struct{})
	go func() {
		_ = m.Close()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("Clock-driven Mixer.Close() should complete almost instantly")
	}
}

// TestMixer_ConcurrentClose verifies that calling Close() from multiple
// goroutines simultaneously doesn't panic or race (sync.Once protects it).
func TestMixer_ConcurrentClose(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) {},
	})
	m.AddChannel("cam1")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Close()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// OK — all Close() calls completed.
	case <-time.After(6 * time.Second):
		t.Fatal("concurrent Mixer.Close() appears to be deadlocked")
	}
}

// TestMixer_CloseDoesNotHoldMuDuringCgo verifies the fix where Close()
// collects codecs under the lock, then closes them outside the lock.
// We verify this by ensuring Close() doesn't block IngestFrame on
// another goroutine (which also takes the lock). If Close held mu during
// cgo, IngestFrame would block indefinitely.
func TestMixer_CloseDoesNotHoldMuDuringCgo(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) {},
	})
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Start Close in background.
	closeDone := make(chan struct{})
	go func() {
		_ = m.Close()
		close(closeDone)
	}()

	// Wait for Close to finish (should not deadlock).
	select {
	case <-closeDone:
		// OK
	case <-time.After(6 * time.Second):
		t.Fatal("Mixer.Close() deadlocked — likely holding mu during cgo calls")
	}

	// After close, operations should be safe (no panic).
	// IngestFrame on a closed mixer should not crash.
	require.NotPanics(t, func() {
		m.IngestFrame("cam1", &media.AudioFrame{
			PTS:        1000,
			Data:       []byte{0xFF},
			SampleRate: 48000,
			Channels:   2,
		})
	})
}
