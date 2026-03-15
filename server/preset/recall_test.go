package preset

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockTarget records calls to RecallTarget methods for testing.
type mockTarget struct {
	cutCalls      []string
	previewCalls  []string
	levelCalls    []levelCall
	muteCalls     []muteCall
	afvCalls      []afvCall
	masterCalls   []float64
	knownSources  map[string]bool
	knownChannels map[string]bool
}

type levelCall struct {
	key   string
	level float64
}

type muteCall struct {
	key   string
	muted bool
}

type afvCall struct {
	key string
	afv bool
}

func newMockTarget(sources []string, channels []string) *mockTarget {
	m := &mockTarget{
		knownSources:  make(map[string]bool),
		knownChannels: make(map[string]bool),
	}
	for _, s := range sources {
		m.knownSources[s] = true
	}
	for _, c := range channels {
		m.knownChannels[c] = true
	}
	return m
}

func (m *mockTarget) Cut(_ context.Context, source string) error {
	if !m.knownSources[source] {
		return fmt.Errorf("source %q not found", source)
	}
	m.cutCalls = append(m.cutCalls, source)
	return nil
}

func (m *mockTarget) SetPreview(_ context.Context, source string) error {
	if !m.knownSources[source] {
		return fmt.Errorf("source %q not found", source)
	}
	m.previewCalls = append(m.previewCalls, source)
	return nil
}

func (m *mockTarget) SetLevel(sourceKey string, levelDB float64) error {
	if !m.knownChannels[sourceKey] {
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	m.levelCalls = append(m.levelCalls, levelCall{sourceKey, levelDB})
	return nil
}

func (m *mockTarget) SetMuted(sourceKey string, muted bool) error {
	if !m.knownChannels[sourceKey] {
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	m.muteCalls = append(m.muteCalls, muteCall{sourceKey, muted})
	return nil
}

func (m *mockTarget) SetAFV(sourceKey string, afv bool) error {
	if !m.knownChannels[sourceKey] {
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	m.afvCalls = append(m.afvCalls, afvCall{sourceKey, afv})
	return nil
}

func (m *mockTarget) SetMasterLevel(level float64) error {
	m.masterCalls = append(m.masterCalls, level)
	return nil
}

func TestRecallSetsProgramSource(t *testing.T) {
	target := newMockTarget([]string{"cam1", "cam2"}, []string{"cam1", "cam2"})

	p := Preset{
		ID:            "test-id",
		Name:          "Test",
		ProgramSource: "cam1",
		PreviewSource: "cam2",
		MasterLevel:   -3.0,
		CreatedAt:     time.Now(),
	}

	warnings := Recall(context.Background(), p, target)

	require.Empty(t, warnings)
	require.Equal(t, []string{"cam1"}, target.cutCalls)
}

func TestRecallSetsPreviewSource(t *testing.T) {
	target := newMockTarget([]string{"cam1", "cam2"}, []string{"cam1", "cam2"})

	p := Preset{
		ID:            "test-id",
		Name:          "Test",
		ProgramSource: "cam1",
		PreviewSource: "cam2",
		MasterLevel:   0,
		CreatedAt:     time.Now(),
	}

	Recall(context.Background(), p, target)

	require.Equal(t, []string{"cam2"}, target.previewCalls)
}

func TestRecallAppliesAudioChannels(t *testing.T) {
	target := newMockTarget([]string{"cam1"}, []string{"cam1", "cam2"})

	p := Preset{
		ID:            "test-id",
		Name:          "Test",
		ProgramSource: "cam1",
		AudioChannels: map[string]AudioChannelPreset{
			"cam1": {Level: -6, Muted: true, AFV: false},
			"cam2": {Level: 0, Muted: false, AFV: true},
		},
		MasterLevel: -3.0,
		CreatedAt:   time.Now(),
	}

	warnings := Recall(context.Background(), p, target)

	require.Empty(t, warnings)
	require.Len(t, target.levelCalls, 2)

	// Check that both channels got level calls (order may vary due to map iteration)
	levelsByKey := make(map[string]float64)
	for _, c := range target.levelCalls {
		levelsByKey[c.key] = c.level
	}
	require.Equal(t, float64(-6), levelsByKey["cam1"])
	require.Equal(t, float64(0), levelsByKey["cam2"])
}

func TestRecallSetsMasterLevel(t *testing.T) {
	target := newMockTarget([]string{"cam1"}, []string{})

	p := Preset{
		ID:            "test-id",
		Name:          "Test",
		ProgramSource: "cam1",
		MasterLevel:   -12.5,
		CreatedAt:     time.Now(),
	}

	Recall(context.Background(), p, target)

	require.Equal(t, []float64{-12.5}, target.masterCalls)
}

func TestRecallMissingSourceWarning(t *testing.T) {
	// Target has cam1 but NOT cam2
	target := newMockTarget([]string{"cam1"}, []string{"cam1"})

	p := Preset{
		ID:            "test-id",
		Name:          "Test",
		ProgramSource: "cam1",
		PreviewSource: "cam2", // not connected
		MasterLevel:   0,
		CreatedAt:     time.Now(),
	}

	warnings := Recall(context.Background(), p, target)

	require.Len(t, warnings, 1)
	require.Empty(t, target.previewCalls)
	// Program should still have been set
	require.Equal(t, []string{"cam1"}, target.cutCalls)
}

func TestRecallMissingAudioChannelWarning(t *testing.T) {
	// Target has cam1 source but no audio channel for cam2
	target := newMockTarget([]string{"cam1"}, []string{"cam1"})

	p := Preset{
		ID:            "test-id",
		Name:          "Test",
		ProgramSource: "cam1",
		AudioChannels: map[string]AudioChannelPreset{
			"cam1": {Level: 0, Muted: false, AFV: true},
			"cam2": {Level: -6, Muted: true, AFV: false}, // channel not connected
		},
		MasterLevel: 0,
		CreatedAt:   time.Now(),
	}

	warnings := Recall(context.Background(), p, target)

	require.Len(t, warnings, 1)

	// cam1 should still be applied
	levelsByKey := make(map[string]float64)
	for _, c := range target.levelCalls {
		levelsByKey[c.key] = c.level
	}
	require.Contains(t, levelsByKey, "cam1", "cam1 level should have been set despite cam2 missing")
}
