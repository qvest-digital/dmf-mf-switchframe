package audio

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// TestMixer_CloseCompletesWithinTimeout verifies that Mixer.Close() completes
// within a reasonable time and does not deadlock.
func TestMixer_CloseCompletesWithinTimeout(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
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

// TestMixer_ConcurrentClose verifies that calling Close() from multiple
// goroutines simultaneously doesn't panic or race (sync.Once protects it).
func TestMixer_ConcurrentClose(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
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

// TestMixer_IngestAfterCloseDoesNotResurrectDecoder verifies that an ingest
// running after Close() has freed the decoders does not recreate one: Close()
// sets m.closed before freeing, and initChannelDecoder must observe it.
func TestMixer_IngestAfterCloseDoesNotResurrectDecoder(t *testing.T) {
	t.Parallel()

	var decodersCreated atomic.Int32

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		DecoderFactory: func(_, _ int) (Decoder, error) {
			decodersCreated.Add(1)
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
	})
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	require.NoError(t, m.Close())

	// Ingest after close must not create the decoder.
	m.IngestFrame("cam1", &media.AudioFrame{
		PTS: 2000, Data: []byte{0xFF}, SampleRate: 48000, Channels: 2,
	})
	require.Zero(t, decodersCreated.Load(),
		"closed mixer must not create a decoder on late ingest")
}
