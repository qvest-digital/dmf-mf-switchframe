package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

func TestGetFormat(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	req := httptest.NewRequest("GET", "/api/format", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp formatResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)

	// Default format should be 1080p29.97
	require.Equal(t, 1920, resp.Format.Width)
	require.Equal(t, 1080, resp.Format.Height)
	require.Equal(t, 30000, resp.Format.FPSNum)
	require.Equal(t, 1001, resp.Format.FPSDen)

	// Presets should be non-empty and sorted
	require.NotEmpty(t, resp.Presets)
	for i := 1; i < len(resp.Presets); i++ {
		require.True(t, resp.Presets[i-1] < resp.Presets[i],
			"presets not sorted: %q >= %q", resp.Presets[i-1], resp.Presets[i])
	}

	// Verify all known presets are present
	require.Equal(t, len(switcher.FormatPresets), len(resp.Presets))
}

func TestSetFormatPreset(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	body := `{"format":"1080p25"}`
	req := httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// Verify the format was applied
	f := sw.PipelineFormat()
	require.Equal(t, 1920, f.Width)
	require.Equal(t, 1080, f.Height)
	require.Equal(t, 25, f.FPSNum)
	require.Equal(t, 1, f.FPSDen)
	require.Equal(t, "1080p25", f.Name)
}

func TestSetFormatCustom(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	body := `{"width":1920,"height":1080,"fpsNum":50,"fpsDen":1}`
	req := httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// Verify custom format
	f := sw.PipelineFormat()
	require.Equal(t, 1920, f.Width)
	require.Equal(t, 1080, f.Height)
	require.Equal(t, 50, f.FPSNum)
	require.Equal(t, 1, f.FPSDen)
}

func TestSetFormatInvalidPreset(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	body := `{"format":"bogus"}`
	req := httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestSetFormatOddDimensions(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	// Odd width
	body := `{"width":1921,"height":1080,"fpsNum":30,"fpsDen":1}`
	req := httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())

	// Odd height
	body = `{"width":1920,"height":1081,"fpsNum":30,"fpsDen":1}`
	req = httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestSetFormatDefaultNoChange(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	// Set to the default format (1080p29.97) should succeed
	body := `{"format":"1080p29.97"}`
	req := httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	f := sw.PipelineFormat()
	require.Equal(t, "1080p29.97", f.Name)
}

func TestSetFormatNoBody(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	body := `{}`
	req := httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestSetFormatInvalidJSON(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	req := httptest.NewRequest("PUT", "/api/format", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSetFormatDuringTransition(t *testing.T) {
	// Use transition-capable setup so we can start a transition.
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	defer sw.Close()
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

	// Start a transition.
	body := `{"source":"camera2","type":"mix","durationMs":2000}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "start transition: body: %s", rec.Body.String())

	// Attempt to change format during transition should return 409.
	body = `{"format":"720p50"}`
	req = httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())

	// Verify format is unchanged after rejection
	f := sw.PipelineFormat()
	require.Equal(t, "1080p29.97", f.Name, "format should be unchanged after rejection")
}

func TestSetFormatWidthOutOfRange(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	// Width too small
	body := `{"width":100,"height":180,"fpsNum":30,"fpsDen":1}`
	req := httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())

	// Width too large
	body = `{"width":8000,"height":1080,"fpsNum":30,"fpsDen":1}`
	req = httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestSetFormatHeightOutOfRange(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	// Height too small
	body := `{"width":320,"height":100,"fpsNum":30,"fpsDen":1}`
	req := httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())

	// Height too large
	body = `{"width":1920,"height":5000,"fpsNum":30,"fpsDen":1}`
	req = httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestSetFormatInvalidFPS(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	// Zero fpsNum
	body := `{"width":1920,"height":1080,"fpsNum":0,"fpsDen":1}`
	req := httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())

	// Zero fpsDen
	body = `{"width":1920,"height":1080,"fpsNum":30,"fpsDen":0}`
	req = httptest.NewRequest("PUT", "/api/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}
