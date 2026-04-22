package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/switcher"
)

func setupKeyTestAPI(t *testing.T) *API {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("cam1", r1)
	kp := graphics.NewKeyProcessor()
	api := NewAPI(sw, WithKeyer(kp))
	return api
}

func TestSetSourceKey_Chroma(t *testing.T) {
	api := setupKeyTestAPI(t)
	body := `{"type":"chroma","enabled":true,"keyColorY":120,"keyColorCb":100,"keyColorCr":80,"similarity":0.4,"smoothness":0.1}`
	req := httptest.NewRequest("PUT", "/api/sources/cam1/key", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var cfg graphics.KeyConfig
	err := json.NewDecoder(rec.Body).Decode(&cfg)
	require.NoError(t, err)
	require.Equal(t, graphics.KeyTypeChroma, cfg.Type)
	require.True(t, cfg.Enabled)
}

func TestSetSourceKey_Luma(t *testing.T) {
	api := setupKeyTestAPI(t)
	body := `{"type":"luma","enabled":true,"lowClip":0.1,"highClip":0.9,"softness":0.05}`
	req := httptest.NewRequest("PUT", "/api/sources/cam1/key", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

func TestSetSourceKey_Neural(t *testing.T) {
	api := setupKeyTestAPI(t)
	body := `{"type":"neural","enabled":true,"keyColorY":173,"keyColorCb":42,"keyColorCr":26,"similarity":0.4,"smoothness":0.1}`
	req := httptest.NewRequest("PUT", "/api/sources/cam1/key", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var cfg graphics.KeyConfig
	err := json.NewDecoder(rec.Body).Decode(&cfg)
	require.NoError(t, err)
	require.Equal(t, graphics.KeyTypeNeural, cfg.Type)
	require.True(t, cfg.Enabled)
}

func TestSetSourceKey_InvalidType(t *testing.T) {
	api := setupKeyTestAPI(t)
	body := `{"type":"invalid","enabled":true}`
	req := httptest.NewRequest("PUT", "/api/sources/cam1/key", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "type must be 'chroma', 'luma', or 'neural'")
}

func TestSetSourceKey_InvalidJSON(t *testing.T) {
	api := setupKeyTestAPI(t)
	req := httptest.NewRequest("PUT", "/api/sources/cam1/key", strings.NewReader("{bad"))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid json")
}

func TestGetSourceKey(t *testing.T) {
	api := setupKeyTestAPI(t)

	// Set a key first.
	body := `{"type":"neural","enabled":true}`
	req := httptest.NewRequest("PUT", "/api/sources/cam1/key", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// GET it back.
	req = httptest.NewRequest("GET", "/api/sources/cam1/key", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var cfg graphics.KeyConfig
	err := json.NewDecoder(rec.Body).Decode(&cfg)
	require.NoError(t, err)
	require.Equal(t, graphics.KeyTypeNeural, cfg.Type)
}

func TestGetSourceKey_NotFound(t *testing.T) {
	api := setupKeyTestAPI(t)
	req := httptest.NewRequest("GET", "/api/sources/cam1/key", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteSourceKey(t *testing.T) {
	api := setupKeyTestAPI(t)

	// Set a key first.
	body := `{"type":"chroma","enabled":true,"keyColorY":120,"keyColorCb":100,"keyColorCr":80,"similarity":0.4,"smoothness":0.1}`
	req := httptest.NewRequest("PUT", "/api/sources/cam1/key", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Delete it.
	req = httptest.NewRequest("DELETE", "/api/sources/cam1/key", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	// Confirm it's gone.
	req = httptest.NewRequest("GET", "/api/sources/cam1/key", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
