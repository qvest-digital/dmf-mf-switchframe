package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

func setupEncoderTestAPI(t *testing.T) (*API, *switcher.Switcher) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	_ = sw.BuildPipeline()
	api := NewAPI(sw)
	return api, sw
}

func TestHandleGetEncoder(t *testing.T) {
	api, sw := setupEncoderTestAPI(t)
	defer sw.Close()

	avail := []codec.EncoderInfo{
		{Name: "libx264", DisplayName: "x264 (Software)", IsDefault: true},
	}
	sw.SetAvailableEncoders(avail)
	sw.SetCodecInfo("libx264", "h264", false)

	req := httptest.NewRequest("GET", "/api/encoder", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp encoderResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "libx264", resp.Current)
	require.Len(t, resp.Available, 1)
	require.Equal(t, "libx264", resp.Available[0].Name)
}

func TestHandleSetEncoder(t *testing.T) {
	api, sw := setupEncoderTestAPI(t)
	defer sw.Close()

	avail := []codec.EncoderInfo{
		{Name: "libx264", DisplayName: "x264 (Software)", IsDefault: true},
		{Name: "h264_nvenc", DisplayName: "NVENC (CUDA)", IsDefault: false},
	}
	sw.SetAvailableEncoders(avail)
	sw.SetCodecInfo("libx264", "h264", false)

	body, _ := json.Marshal(encoderRequest{Encoder: "h264_nvenc"})
	req := httptest.NewRequest("PUT", "/api/encoder", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "h264_nvenc", sw.EncoderName())
}

func TestHandleSetEncoder_InvalidName(t *testing.T) {
	api, sw := setupEncoderTestAPI(t)
	defer sw.Close()

	avail := []codec.EncoderInfo{
		{Name: "libx264", DisplayName: "x264 (Software)", IsDefault: true},
	}
	sw.SetAvailableEncoders(avail)
	sw.SetCodecInfo("libx264", "h264", false)

	body, _ := json.Marshal(encoderRequest{Encoder: "nonexistent"})
	req := httptest.NewRequest("PUT", "/api/encoder", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// Should be 400 Bad Request, not 500.
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSetEncoder_EmptyName(t *testing.T) {
	api, sw := setupEncoderTestAPI(t)
	defer sw.Close()

	body, _ := json.Marshal(encoderRequest{Encoder: ""})
	req := httptest.NewRequest("PUT", "/api/encoder", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}
