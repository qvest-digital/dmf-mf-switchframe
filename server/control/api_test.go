package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

func setupTestAPI(t *testing.T) (*API, *switcher.Switcher) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)
	api := NewAPI(sw)
	return api, sw
}

func TestCutEndpoint(t *testing.T) {
	api, sw := setupTestAPI(t)
	_ = sw.Cut(context.Background(), "camera1")
	body := `{"source":"camera2"}`
	req := httptest.NewRequest("POST", "/api/switch/cut", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	state := sw.State()
	require.Equal(t, "camera2", state.ProgramSource)
}

func TestCutToMissingSourceReturns404(t *testing.T) {
	api, _ := setupTestAPI(t)
	body := `{"source":"nonexistent"}`
	req := httptest.NewRequest("POST", "/api/switch/cut", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPreviewEndpoint(t *testing.T) {
	api, sw := setupTestAPI(t)
	body := `{"source":"camera1"}`
	req := httptest.NewRequest("POST", "/api/switch/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	state := sw.State()
	require.Equal(t, "camera1", state.PreviewSource)
}

func TestStateEndpoint(t *testing.T) {
	api, sw := setupTestAPI(t)
	_ = sw.Cut(context.Background(), "camera1")
	req := httptest.NewRequest("GET", "/api/switch/state", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var state internal.ControlRoomState
	err := json.NewDecoder(rec.Body).Decode(&state)
	require.NoError(t, err)
	require.Equal(t, "camera1", state.ProgramSource)
}

func TestHandleSetLabel(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	// Set label
	body := strings.NewReader(`{"label":"Camera 1"}`)
	req := httptest.NewRequest("POST", "/api/sources/camera1/label", body)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// Verify in state
	req = httptest.NewRequest("GET", "/api/switch/state", nil)
	w = httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	var state internal.ControlRoomState
	_ = json.NewDecoder(w.Body).Decode(&state)
	require.Equal(t, "Camera 1", state.Sources["camera1"].Label)

	// Unknown source
	body = strings.NewReader(`{"label":"Nope"}`)
	req = httptest.NewRequest("POST", "/api/sources/nonexistent/label", body)
	w = httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestCutInvalidJSON(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("POST", "/api/switch/cut", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCutEmptySource(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("POST", "/api/switch/cut", strings.NewReader(`{"source":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPreviewInvalidJSON(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("POST", "/api/switch/preview", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPreviewEmptySource(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("POST", "/api/switch/preview", strings.NewReader(`{"source":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSetLabelInvalidJSON(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("POST", "/api/sources/camera1/label", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// setupTransitionTestAPI creates a test API with transition support configured.
// Sources "camera1" and "camera2" are registered, and "camera1" is set on program.
func setupTransitionTestAPI(t *testing.T) (*API, *switcher.Switcher) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)
	sw.SetTransitionConfig(switcher.TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(320, 240), nil
		},
	})
	_ = sw.Cut(context.Background(), "camera1")
	api := NewAPI(sw)
	return api, sw
}

func TestHandleTransition(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	body := `{"source":"camera2","type":"mix","durationMs":500}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var state internal.ControlRoomState
	err := json.NewDecoder(rec.Body).Decode(&state)
	require.NoError(t, err)
	require.True(t, state.InTransition, "expected InTransition=true")
}

func TestHandleTransitionBadType(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	body := `{"source":"camera2","type":"wipe","durationMs":500}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestHandleTransitionBadDuration(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	// Too short
	body := `{"source":"camera2","type":"mix","durationMs":50}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())

	// Too long
	body = `{"source":"camera2","type":"mix","durationMs":6000}`
	req = httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "too long: body: %s", rec.Body.String())
}

func TestHandleTransitionAlreadyOnProgram(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	// camera1 is already on program — transition to it should return 400
	body := `{"source":"camera1","type":"mix","durationMs":500}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestHandleTransitionPosition(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	// Start a transition first
	body := `{"source":"camera2","type":"mix","durationMs":2000}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "start transition: body: %s", rec.Body.String())

	// Set position
	body = `{"position":0.5}`
	req = httptest.NewRequest("POST", "/api/switch/transition/position", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

func TestHandleTransitionPositionNoTransition(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	// No transition active — set position should fail
	body := `{"position":0.5}`
	req := httptest.NewRequest("POST", "/api/switch/transition/position", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
}

func TestHandleFTB(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	req := httptest.NewRequest("POST", "/api/switch/ftb", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var state internal.ControlRoomState
	err := json.NewDecoder(rec.Body).Decode(&state)
	require.NoError(t, err)
	require.True(t, state.FTBActive, "expected FTBActive=true")
}

func TestHandleFTBDuringMix(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	// Start a mix transition first
	body := `{"source":"camera2","type":"mix","durationMs":2000}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "start transition: body: %s", rec.Body.String())

	// FTB during active mix should fail with 409
	req = httptest.NewRequest("POST", "/api/switch/ftb", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
}

func TestSourcesEndpoint(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var sources map[string]internal.SourceInfo
	err := json.NewDecoder(rec.Body).Decode(&sources)
	require.NoError(t, err)
	// Built-in virtual sources (source:black, source:colorbars) are always
	// auto-registered, on top of the 2 user sources setupTestAPI installs.
	require.Len(t, sources, 4)
}

// --- Audio API tests ---

// mockMixer implements AudioMixerAPI for testing.
type mockMixer struct {
	levelCalls  []mockLevelCall
	trimCalls   []mockLevelCall
	muteCalls   []mockMuteCall
	afvCalls    []mockAFVCall
	masterCalls []float64
	knownKeys   map[string]bool
}

type mockLevelCall struct {
	source string
	level  float64
}

type mockMuteCall struct {
	source string
	muted  bool
}

type mockAFVCall struct {
	source string
	afv    bool
}

func newMockMixer(keys ...string) *mockMixer {
	m := &mockMixer{knownKeys: make(map[string]bool)}
	for _, k := range keys {
		m.knownKeys[k] = true
	}
	return m
}

func (m *mockMixer) SetTrim(sourceKey string, trimDB float64) error {
	if trimDB < -20 || trimDB > 20 {
		return audio.ErrInvalidTrim
	}
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("channel %q: %w", sourceKey, audio.ErrChannelNotFound)
	}
	m.trimCalls = append(m.trimCalls, mockLevelCall{sourceKey, trimDB})
	return nil
}

func (m *mockMixer) SetLevel(sourceKey string, levelDB float64) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("channel %q: %w", sourceKey, audio.ErrChannelNotFound)
	}
	m.levelCalls = append(m.levelCalls, mockLevelCall{sourceKey, levelDB})
	return nil
}

func (m *mockMixer) SetMuted(sourceKey string, muted bool) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("channel %q: %w", sourceKey, audio.ErrChannelNotFound)
	}
	m.muteCalls = append(m.muteCalls, mockMuteCall{sourceKey, muted})
	return nil
}

func (m *mockMixer) SetAFV(sourceKey string, afv bool) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("channel %q: %w", sourceKey, audio.ErrChannelNotFound)
	}
	m.afvCalls = append(m.afvCalls, mockAFVCall{sourceKey, afv})
	return nil
}

func (m *mockMixer) SetPhaseInvert(sourceKey string, invert bool) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("channel %q: %w", sourceKey, audio.ErrChannelNotFound)
	}
	return nil
}

func (m *mockMixer) SetBalance(sourceKey string, balance float64) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("audio: unknown source %q", sourceKey)
	}
	if balance < -1.0 || balance > 1.0 {
		return fmt.Errorf("audio: balance must be between -1.0 and 1.0")
	}
	return nil
}

func (m *mockMixer) SetMasterLevel(level float64) error {
	m.masterCalls = append(m.masterCalls, level)
	return nil
}

func (m *mockMixer) SetEQ(sourceKey string, band int, frequency, gain, q float64, enabled bool) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("%w: %s", audio.ErrChannelNotFound, sourceKey)
	}
	return nil
}

func (m *mockMixer) GetEQ(sourceKey string) ([3]audio.EQBandSettings, error) {
	if !m.knownKeys[sourceKey] {
		return [3]audio.EQBandSettings{}, fmt.Errorf("%w: %s", audio.ErrChannelNotFound, sourceKey)
	}
	return [3]audio.EQBandSettings{
		{Frequency: 250, Gain: 0, Q: 1.0, Enabled: false},
		{Frequency: 1000, Gain: 0, Q: 1.0, Enabled: false},
		{Frequency: 4000, Gain: 0, Q: 1.0, Enabled: false},
	}, nil
}

func (m *mockMixer) SetCompressor(sourceKey string, threshold, ratio, attack, release, makeupGain float64) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("%w: %s", audio.ErrChannelNotFound, sourceKey)
	}
	return nil
}

func (m *mockMixer) GetCompressor(sourceKey string) (audio.CompressorState, error) {
	if !m.knownKeys[sourceKey] {
		return audio.CompressorState{}, fmt.Errorf("%w: %s", audio.ErrChannelNotFound, sourceKey)
	}
	return audio.CompressorState{
		Ratio:   1.0,
		Attack:  5.0,
		Release: 100.0,
	}, nil
}

func (m *mockMixer) SetGate(sourceKey string, threshold, rangeDB, attack, release, hold float64, enabled bool) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("audio: unknown source %q", sourceKey)
	}
	return nil
}

func (m *mockMixer) GetGate(sourceKey string) (audio.GateState, error) {
	if !m.knownKeys[sourceKey] {
		return audio.GateState{}, fmt.Errorf("audio: unknown source %q", sourceKey)
	}
	return audio.GateState{
		Threshold: -40,
		Range:     -60,
		Attack:    0.5,
		Release:   100,
		Hold:      50,
		Enabled:   false,
	}, nil
}

func (m *mockMixer) SetHPF(sourceKey string, frequency float64, enabled bool) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("audio: unknown source %q", sourceKey)
	}
	return nil
}

func (m *mockMixer) GetHPF(sourceKey string) (float64, bool) {
	if !m.knownKeys[sourceKey] {
		return 80, false
	}
	return 80, false
}

func (m *mockMixer) SetAudioDelay(sourceKey string, delayMs int) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("channel %q: %w", sourceKey, audio.ErrChannelNotFound)
	}
	return nil
}

func (m *mockMixer) AudioDelayMs(sourceKey string) int {
	return 0
}

func (m *mockMixer) SetLimiterThreshold(thresholdDB float64) error {
	if thresholdDB < -12 || thresholdDB > 0 {
		return fmt.Errorf("audio: limiter threshold must be between -12 and 0 dBFS")
	}
	return nil
}

func (m *mockMixer) SetLimiterCeiling(ceilingDB float64) error {
	if ceilingDB < -3 || ceilingDB > 0 {
		return fmt.Errorf("audio: limiter ceiling must be between -3 and 0 dBFS")
	}
	return nil
}

func (m *mockMixer) LimiterSettings() (float64, float64, float64) {
	return -1.0, 0.0, 0.0
}

func setupAudioTestAPI(t *testing.T) (*API, *mockMixer) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	mock := newMockMixer("camera1", "camera2")
	api := NewAPI(sw, WithMixer(mock))
	return api, mock
}

func TestAudioLevelEndpoint(t *testing.T) {
	api, mock := setupAudioTestAPI(t)

	body := `{"source":"camera1","level":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/level", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mock.levelCalls, 1)
	require.Equal(t, "camera1", mock.levelCalls[0].source)
	require.Equal(t, -6.0, mock.levelCalls[0].level)
}

func TestAudioLevelUnknownSource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"nonexistent","level":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/level", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestAudioLevelInvalidJSON(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	req := httptest.NewRequest("POST", "/api/audio/level", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAudioLevelEmptySource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"","level":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/level", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAudioLevelNoMixer(t *testing.T) {
	api, _ := setupTestAPI(t) // no mixer attached

	body := `{"source":"camera1","level":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/level", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestAudioMuteEndpoint(t *testing.T) {
	api, mock := setupAudioTestAPI(t)

	body := `{"source":"camera1","muted":true}`
	req := httptest.NewRequest("POST", "/api/audio/mute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mock.muteCalls, 1)
	require.Equal(t, "camera1", mock.muteCalls[0].source)
	require.True(t, mock.muteCalls[0].muted)
}

func TestAudioMuteUnknownSource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"nonexistent","muted":true}`
	req := httptest.NewRequest("POST", "/api/audio/mute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAudioMuteInvalidJSON(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	req := httptest.NewRequest("POST", "/api/audio/mute", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAudioMuteEmptySource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"","muted":true}`
	req := httptest.NewRequest("POST", "/api/audio/mute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAudioAFVEndpoint(t *testing.T) {
	api, mock := setupAudioTestAPI(t)

	body := `{"source":"camera2","afv":true}`
	req := httptest.NewRequest("POST", "/api/audio/afv", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mock.afvCalls, 1)
	require.Equal(t, "camera2", mock.afvCalls[0].source)
	require.True(t, mock.afvCalls[0].afv)
}

func TestAudioAFVUnknownSource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"nonexistent","afv":true}`
	req := httptest.NewRequest("POST", "/api/audio/afv", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAudioAFVInvalidJSON(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	req := httptest.NewRequest("POST", "/api/audio/afv", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAudioAFVEmptySource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"","afv":true}`
	req := httptest.NewRequest("POST", "/api/audio/afv", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAudioMasterEndpoint(t *testing.T) {
	api, mock := setupAudioTestAPI(t)

	body := `{"level":-3.5}`
	req := httptest.NewRequest("POST", "/api/audio/master", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mock.masterCalls, 1)
	require.Equal(t, -3.5, mock.masterCalls[0])
}

func TestAudioMasterInvalidJSON(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	req := httptest.NewRequest("POST", "/api/audio/master", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAudioMasterNoMixer(t *testing.T) {
	api, _ := setupTestAPI(t) // no mixer attached

	body := `{"level":-3.5}`
	req := httptest.NewRequest("POST", "/api/audio/master", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

// --- Audio Trim Endpoint ---

func TestAudioTrimEndpoint(t *testing.T) {
	api, mock := setupAudioTestAPI(t)

	body := `{"source":"camera1","trim":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/trim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mock.trimCalls, 1)
	require.Equal(t, "camera1", mock.trimCalls[0].source)
	require.Equal(t, -6.0, mock.trimCalls[0].level)
}

func TestAudioTrimUnknownSource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"nonexistent","trim":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/trim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestAudioTrimOutOfRange(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"camera1","trim":-25.0}`
	req := httptest.NewRequest("POST", "/api/audio/trim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestAudioTrimInvalidJSON(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	req := httptest.NewRequest("POST", "/api/audio/trim", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAudioTrimEmptySource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"","trim":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/trim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAudioTrimNoMixer(t *testing.T) {
	api, _ := setupTestAPI(t) // no mixer attached

	body := `{"source":"camera1","trim":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/trim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

// --- Limiter API tests ---

func TestSetLimiter(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"threshold":-6,"ceiling":-1}`
	req := httptest.NewRequest("PUT", "/api/audio/limiter", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

func TestSetLimiter_InvalidThreshold(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"threshold":-15,"ceiling":-1}`
	req := httptest.NewRequest("PUT", "/api/audio/limiter", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestSetLimiter_InvalidCeiling(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"threshold":-6,"ceiling":-5}`
	req := httptest.NewRequest("PUT", "/api/audio/limiter", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestSetLimiter_InvalidJSON(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	req := httptest.NewRequest("PUT", "/api/audio/limiter", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSetLimiter_NoMixer(t *testing.T) {
	api, _ := setupTestAPI(t) // no mixer

	body := `{"threshold":-6,"ceiling":-1}`
	req := httptest.NewRequest("PUT", "/api/audio/limiter", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestGetLimiter(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	req := httptest.NewRequest("GET", "/api/audio/limiter", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var resp limiterResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.InDelta(t, -1.0, resp.Threshold, 0.01)
	require.InDelta(t, 0.0, resp.Ceiling, 0.01)
	require.InDelta(t, 0.0, resp.GainReduction, 0.01)
}

func TestGetLimiter_NoMixer(t *testing.T) {
	api, _ := setupTestAPI(t) // no mixer

	req := httptest.NewRequest("GET", "/api/audio/limiter", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestSetEQ_ChannelNotFound(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"band":0,"frequency":250,"gain":3.0,"q":1.0,"enabled":true}`
	req := httptest.NewRequest("PUT", "/api/audio/unknown/eq", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestSetCompressor_ChannelNotFound(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"threshold":-10,"ratio":4.0,"attack":5.0,"release":100.0,"makeupGain":0}`
	req := httptest.NewRequest("PUT", "/api/audio/unknown/compressor", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

// --- API v1 alias tests ---

func TestAPIv1AliasState(t *testing.T) {
	api, sw := setupTestAPI(t)
	_ = sw.Cut(context.Background(), "camera1")

	// GET /api/v1/switch/state should return the same response as /api/switch/state.
	req := httptest.NewRequest("GET", "/api/v1/switch/state", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "v1 state endpoint should return 200")
	var state internal.ControlRoomState
	err := json.NewDecoder(rec.Body).Decode(&state)
	require.NoError(t, err)
	require.Equal(t, "camera1", state.ProgramSource)
}

func TestAPIv1AliasCut(t *testing.T) {
	api, sw := setupTestAPI(t)
	_ = sw.Cut(context.Background(), "camera1")

	body := `{"source":"camera2"}`
	req := httptest.NewRequest("POST", "/api/v1/switch/cut", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "v1 cut endpoint should return 200")
	state := sw.State()
	require.Equal(t, "camera2", state.ProgramSource)
}

func TestAPIv1AliasSources(t *testing.T) {
	api, _ := setupTestAPI(t)

	req := httptest.NewRequest("GET", "/api/v1/sources", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "v1 sources endpoint should return 200")
	var sources map[string]internal.SourceInfo
	err := json.NewDecoder(rec.Body).Decode(&sources)
	require.NoError(t, err)
	// 2 user sources installed by setupTestAPI + 2 built-in virtual sources.
	require.Len(t, sources, 4)
}

// --- SCTE-35 Rules API tests ---

func TestCreateRuleReturns201(t *testing.T) {
	api, _, _ := setupSCTE35TestAPI(t)

	body := `{"name":"test rule","enabled":true,"action":"pass","conditions":[{"field":"commandType","operator":"=","value":"splice_insert"}]}`
	req := httptest.NewRequest("POST", "/api/scte35/rules", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
}

func TestInternalConnections_NoCounter(t *testing.T) {
	api, _ := setupTestAPI(t) // no WithConnectionCounter
	req := httptest.NewRequest("GET", "/api/internal/connections", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		WebTransport int `json:"webtransport"`
		SRT          int `json:"srt"`
		MoQ          int `json:"moq"`
		Total        int `json:"total"`
	}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Equal(t, 0, resp.WebTransport)
	require.Equal(t, 0, resp.SRT)
	require.Equal(t, 0, resp.MoQ)
	require.Equal(t, 0, resp.Total)
}

func TestInternalConnections_WithCounter(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)

	api := NewAPI(sw, WithConnectionCounter(func() (int, int, int) {
		return 3, 1, 5
	}))

	req := httptest.NewRequest("GET", "/api/internal/connections", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp struct {
		WebTransport int `json:"webtransport"`
		SRT          int `json:"srt"`
		MoQ          int `json:"moq"`
		Total        int `json:"total"`
	}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Equal(t, 3, resp.WebTransport)
	require.Equal(t, 1, resp.SRT)
	require.Equal(t, 5, resp.MoQ)
	require.Equal(t, 9, resp.Total)
}
