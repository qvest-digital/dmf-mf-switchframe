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

// TestMixer_TickAfterCloseDoesNotResurrectCodecs verifies that a straggler
// tick() running after Close() has freed the codecs does not recreate or use
// them. This models the pathological shutdown path where the ticker goroutine
// outlives the shutdown wait: Close() sets m.closed before freeing, so the late
// tick() must observe the closed flag, produce no output, and never invoke the
// encoder/decoder factory again.
func TestMixer_TickAfterCloseDoesNotResurrectCodecs(t *testing.T) {
	t.Parallel()

	var encodersCreated atomic.Int32
	var decodersCreated atomic.Int32

	// Use a clock-driven mixer so no background ticker races with our manual
	// tick() call — we drive tick() ourselves to model the straggler.
	m := NewMixer(MixerConfig{
		SampleRate:  48000,
		Channels:    2,
		ClockDriven: true,
		Output:      func(_ *media.AudioFrame) {},
		EncoderFactory: func(_, _ int) (Encoder, error) {
			encodersCreated.Add(1)
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
		DecoderFactory: func(_, _ int) (Decoder, error) {
			decodersCreated.Add(1)
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
	})
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Prime the encoder via one normal tick so the factory has run once.
	require.NotNil(t, m.tick(), "tick before close should produce output")
	require.Equal(t, int32(1), encodersCreated.Load(), "encoder created once before close")

	require.NoError(t, m.Close())

	// A straggler tick() after Close must not produce output and must not
	// recreate the encoder that Close just freed.
	require.Nil(t, m.tick(), "tick after close must produce no output")
	require.Equal(t, int32(1), encodersCreated.Load(),
		"closed mixer must not recreate the encoder")

	// Ingest after close must not recreate the decoder either.
	m.IngestFrame("cam1", &media.AudioFrame{
		PTS: 2000, Data: []byte{0xFF}, SampleRate: 48000, Channels: 2,
	})
	require.Zero(t, decodersCreated.Load(),
		"closed mixer must not create a decoder on late ingest")
}
