package control

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/internal"
)

// buildRealisticState creates a ControlRoomState representative of a production
// switcher with n sources. Includes audio channels with varied settings, tally
// state, recording/SRT status, presets, and graphics state.
func buildRealisticState(n int) internal.ControlRoomState {
	audioChannels := make(map[string]internal.AudioChannel, n)
	tallyState := make(map[string]string, n)
	sources := make(map[string]internal.SourceInfo, n)

	for i := 0; i < n; i++ {
		key := fmt.Sprintf("cam%d", i+1)
		audioChannels[key] = internal.AudioChannel{
			Level: float64(-6 * (i % 4)),
			Muted: i == 3,
			AFV:   i != 0,
		}

		status := "idle"
		switch i {
		case 0:
			status = "program"
		case 1:
			status = "preview"
		}
		tallyState[key] = status

		sources[key] = internal.SourceInfo{
			Key:     key,
			Label:   fmt.Sprintf("Camera %d", i+1),
			Status:  "healthy",
			DelayMs: i * 5,
		}
	}

	presets := make([]internal.PresetInfo, 4)
	for i := range presets {
		presets[i] = internal.PresetInfo{
			ID:   fmt.Sprintf("preset-%d", i+1),
			Name: fmt.Sprintf("Scene %d", i+1),
		}
	}

	return internal.ControlRoomState{
		ProgramSource:        "cam1",
		PreviewSource:        "cam2",
		TransitionType:       "mix",
		TransitionDurationMs: 1000,
		TransitionPosition:   0.0,
		InTransition:         false,
		FTBActive:            false,
		AudioChannels:        audioChannels,
		MasterLevel:          0.0,
		ProgramPeak:          [2]float64{-12.5, -14.2},
		GainReduction:        0.0,
		TallyState:           tallyState,
		Recording: &internal.RecordingStatus{
			Active:       true,
			Filename:     "program_20260305_140000_001.ts",
			BytesWritten: 524288000,
			DurationSecs: 3600.5,
		},
		SRTOutput: &internal.SRTOutputStatus{
			Active:       true,
			Mode:         "caller",
			Address:      "srt.example.com",
			Port:         9000,
			State:        "connected",
			Connections:  1,
			BytesWritten: 1048576000,
		},
		Sources: sources,
		Presets: presets,
		Graphics: &internal.GraphicsState{
			Active:       true,
			Template:     "lower-third",
			FadePosition: 1.0,
		},
		Seq:       42,
		Timestamp: time.Now().UnixMilli(),
	}
}

// BenchmarkStateMarshal benchmarks JSON serialization of a realistic
// ControlRoomState with 8 sources. This runs on every state broadcast
// to connected browsers via the MoQ control track.
func BenchmarkStateMarshal_8Sources(b *testing.B) {
	state := buildRealisticState(8)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		data, err := json.Marshal(state)
		if err != nil {
			b.Fatal(err)
		}
		_ = data
	}
}

// BenchmarkStateUnmarshal benchmarks JSON deserialization of a realistic
// ControlRoomState. This runs on the browser side (via TypeScript), but
// is benchmarked here to establish a baseline for the Go REST polling path.
func BenchmarkStateUnmarshal_8Sources(b *testing.B) {
	state := buildRealisticState(8)
	data, err := json.Marshal(state)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var s internal.ControlRoomState
		if err := json.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStateMarshal_4Sources benchmarks state serialization with 4 sources
// (the default demo configuration).
func BenchmarkStateMarshal_4Sources(b *testing.B) {
	state := buildRealisticState(4)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		data, err := json.Marshal(state)
		if err != nil {
			b.Fatal(err)
		}
		_ = data
	}
}

// BenchmarkStatePublish benchmarks the full StatePublisher.Publish path
// including JSON serialization and the publish callback.
func BenchmarkStatePublish(b *testing.B) {
	var lastData []byte
	pub := NewStatePublisher(func(data []byte) {
		lastData = data
	})
	state := buildRealisticState(8)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		state.Seq = uint64(i)
		pub.Publish(state)
	}
	_ = lastData
}

// BenchmarkChannelPublish benchmarks the ChannelPublisher.Publish path
// including JSON serialization and channel send.
func BenchmarkChannelPublish(b *testing.B) {
	cp := NewChannelPublisher(1024)
	state := buildRealisticState(8)

	// Drain the channel in a goroutine to prevent blocking and log spam
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-cp.Ch():
			case <-stop:
				return
			}
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		state.Seq = uint64(i)
		cp.Publish(state)
	}

	b.StopTimer()
	close(stop)
}
