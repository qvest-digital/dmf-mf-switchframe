package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/caption"
)

// mockCaptionManager implements CaptionManagerAPI for testing.
type mockCaptionManager struct {
	mode         caption.Mode
	textIngested string
	newlines     int
	clears       int
}

func (m *mockCaptionManager) SetMode(mode caption.Mode) { m.mode = mode }
func (m *mockCaptionManager) Mode() caption.Mode        { return m.mode }
func (m *mockCaptionManager) IngestText(text string)     { m.textIngested += text }
func (m *mockCaptionManager) IngestNewline()             { m.newlines++ }
func (m *mockCaptionManager) Clear()                     { m.clears++ }
func (m *mockCaptionManager) State() caption.State {
	return caption.State{
		Mode:         m.mode.String(),
		AuthorBuffer: m.textIngested,
	}
}

func TestHandleCaptionMode_Valid(t *testing.T) {
	mcm := &mockCaptionManager{}
	api := NewAPI(nil, WithCaptionManager(mcm))

	body, _ := json.Marshal(captionModeRequest{Mode: "author"})
	req := httptest.NewRequest("POST", "/api/captions/mode", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, caption.ModeAuthor, mcm.mode)

	var state caption.State
	json.NewDecoder(rr.Body).Decode(&state)
	require.Equal(t, "author", state.Mode)
}

func TestHandleCaptionMode_Invalid(t *testing.T) {
	mcm := &mockCaptionManager{}
	api := NewAPI(nil, WithCaptionManager(mcm))

	body, _ := json.Marshal(captionModeRequest{Mode: "invalid"})
	req := httptest.NewRequest("POST", "/api/captions/mode", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleCaptionText_IngestText(t *testing.T) {
	mcm := &mockCaptionManager{mode: caption.ModeAuthor}
	api := NewAPI(nil, WithCaptionManager(mcm))

	body, _ := json.Marshal(captionTextRequest{Text: "Hello"})
	req := httptest.NewRequest("POST", "/api/captions/text", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "Hello", mcm.textIngested)
}

func TestHandleCaptionText_Newline(t *testing.T) {
	mcm := &mockCaptionManager{mode: caption.ModeAuthor}
	api := NewAPI(nil, WithCaptionManager(mcm))

	body, _ := json.Marshal(captionTextRequest{Newline: true})
	req := httptest.NewRequest("POST", "/api/captions/text", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, mcm.newlines)
}

func TestHandleCaptionText_Clear(t *testing.T) {
	mcm := &mockCaptionManager{mode: caption.ModeAuthor}
	api := NewAPI(nil, WithCaptionManager(mcm))

	body, _ := json.Marshal(captionTextRequest{Clear: true})
	req := httptest.NewRequest("POST", "/api/captions/text", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, mcm.clears)
}

func TestHandleCaptionText_RequiresAuthorMode(t *testing.T) {
	mcm := &mockCaptionManager{mode: caption.ModeOff}
	api := NewAPI(nil, WithCaptionManager(mcm))

	body, _ := json.Marshal(captionTextRequest{Text: "Hello"})
	req := httptest.NewRequest("POST", "/api/captions/text", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleCaptionText_EmptyBody(t *testing.T) {
	mcm := &mockCaptionManager{mode: caption.ModeAuthor}
	api := NewAPI(nil, WithCaptionManager(mcm))

	body, _ := json.Marshal(captionTextRequest{})
	req := httptest.NewRequest("POST", "/api/captions/text", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleCaptionState(t *testing.T) {
	mcm := &mockCaptionManager{mode: caption.ModePassThrough}
	api := NewAPI(nil, WithCaptionManager(mcm))

	req := httptest.NewRequest("GET", "/api/captions/state", nil)
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var state caption.State
	json.NewDecoder(rr.Body).Decode(&state)
	require.Equal(t, "passthrough", state.Mode)
}

func TestHandleCaptionMode_NotEnabled(t *testing.T) {
	api := NewAPI(nil) // no caption manager

	body, _ := json.Marshal(captionModeRequest{Mode: "author"})
	req := httptest.NewRequest("POST", "/api/captions/mode", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)

	// Route not registered → 405 method not allowed (or 404).
	require.NotEqual(t, http.StatusOK, rr.Code)
}
