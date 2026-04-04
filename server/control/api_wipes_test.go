package control

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

func setupWipeAPI(t *testing.T) (*API, *wipemap.WipeMapStore) {
	t.Helper()
	store, err := wipemap.NewWipeMapStore(t.TempDir())
	require.NoError(t, err)
	store.RegisterBuiltins()
	_, sw := setupTestAPI(t)
	api := NewAPI(sw, WithWipeMapStore(store))
	return api, store
}

func TestWipeAPI_ListPatterns(t *testing.T) {
	api, _ := setupWipeAPI(t)

	req := httptest.NewRequest("GET", "/api/wipes/patterns", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Categories []struct {
			Category string                `json:"category"`
			Patterns []wipemap.WipePattern `json:"patterns"`
		} `json:"categories"`
		Total int `json:"total"`
	}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.GreaterOrEqual(t, resp.Total, 96, "expected at least 96 builtin patterns")
	require.NotEmpty(t, resp.Categories, "expected at least one category")

	// Verify categories are present.
	catNames := make(map[string]bool)
	for _, c := range resp.Categories {
		catNames[c.Category] = true
	}
	require.True(t, catNames["basic"], "expected 'basic' category")
	require.True(t, catNames["iris"], "expected 'iris' category")
	require.True(t, catNames["shape"], "expected 'shape' category")
}

func TestWipeAPI_GetPattern(t *testing.T) {
	api, _ := setupWipeAPI(t)

	req := httptest.NewRequest("GET", "/api/wipes/patterns/star-5", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var p wipemap.WipePattern
	err := json.NewDecoder(rec.Body).Decode(&p)
	require.NoError(t, err)
	require.Equal(t, "star-5", p.ID)
	require.Equal(t, "shape", p.Category)
	require.True(t, p.Builtin)
}

func TestWipeAPI_GetPatternNotFound(t *testing.T) {
	api, _ := setupWipeAPI(t)

	req := httptest.NewRequest("GET", "/api/wipes/patterns/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestWipeAPI_GetPreview(t *testing.T) {
	api, _ := setupWipeAPI(t)

	req := httptest.NewRequest("GET", "/api/wipes/patterns/star-5/preview", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "image/png", rec.Header().Get("Content-Type"))

	// Decode as PNG to verify it's valid.
	img, err := png.Decode(rec.Body)
	require.NoError(t, err)
	bounds := img.Bounds()
	require.Equal(t, 320, bounds.Dx())
	require.Equal(t, 180, bounds.Dy())
}

func TestWipeAPI_GetPreviewNotFound(t *testing.T) {
	api, _ := setupWipeAPI(t)

	req := httptest.NewRequest("GET", "/api/wipes/patterns/nonexistent/preview", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestWipeAPI_CustomUpload(t *testing.T) {
	api, store := setupWipeAPI(t)

	// Create a small grayscale gradient PNG.
	img := image.NewGray(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetGray(x, y, color.Gray{Y: byte(x * 4)})
		}
	}
	var pngBuf bytes.Buffer
	require.NoError(t, png.Encode(&pngBuf, img))

	// Create multipart form.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "my-gradient.png")
	require.NoError(t, err)
	_, err = io.Copy(part, &pngBuf)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", "/api/wipes/custom", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var p wipemap.WipePattern
	err = json.NewDecoder(rec.Body).Decode(&p)
	require.NoError(t, err)
	require.Equal(t, "custom-my-gradient", p.ID)
	require.False(t, p.Builtin)

	// Verify pattern is listed.
	patterns := store.List()
	found := false
	for _, pp := range patterns {
		if pp.ID == "custom-my-gradient" {
			found = true
			break
		}
	}
	require.True(t, found, "custom pattern should appear in list")

	// Verify preview returns a valid image.
	req2 := httptest.NewRequest("GET", "/api/wipes/patterns/custom-my-gradient/preview", nil)
	rec2 := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)
	require.Equal(t, "image/png", rec2.Header().Get("Content-Type"))
}

func TestWipeAPI_DeleteCustom(t *testing.T) {
	api, store := setupWipeAPI(t)

	// Upload a custom pattern first.
	img := image.NewGray(image.Rect(0, 0, 32, 32))
	var pngBuf bytes.Buffer
	require.NoError(t, png.Encode(&pngBuf, img))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "deleteme.png")
	require.NoError(t, err)
	_, _ = io.Copy(part, &pngBuf)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", "/api/wipes/custom", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	// Delete it.
	req2 := httptest.NewRequest("DELETE", "/api/wipes/custom/custom-deleteme", nil)
	rec2 := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusNoContent, rec2.Code)

	// Verify it's gone.
	_, err = store.GetPattern("custom-deleteme")
	require.Error(t, err)
}

func TestWipeAPI_DeleteBuiltinForbidden(t *testing.T) {
	api, _ := setupWipeAPI(t)

	req := httptest.NewRequest("DELETE", "/api/wipes/custom/star-5", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestWipeAPI_NotConfigured(t *testing.T) {
	_, sw := setupTestAPI(t)
	api := NewAPI(sw) // no wipe store

	req := httptest.NewRequest("GET", "/api/wipes/patterns", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// Routes should not be registered at all, so 405 or 404.
	require.NotEqual(t, http.StatusOK, rec.Code)
}
