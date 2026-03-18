package control

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

func setupStingerTestAPI(t *testing.T) (*API, *stinger.Store) {
	t.Helper()

	// Create temp dir with a test stinger
	dir := t.TempDir()
	stingerDir := filepath.Join(dir, "test-wipe")
	require.NoError(t, os.MkdirAll(stingerDir, 0755))

	// Create 3 small test PNG frames (4x4 pixels, even dimensions for YUV420)
	for i := 0; i < 3; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				img.Set(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 128})
			}
		}
		fname := fmt.Sprintf("frame_%03d.png", i)
		f, err := os.Create(filepath.Join(stingerDir, fname))
		require.NoError(t, err)
		err = png.Encode(f, img)
		_ = f.Close()
		require.NoError(t, err)
	}

	store, err := stinger.NewStore(dir, 0)
	require.NoError(t, err)

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
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

	api := NewAPI(sw, WithStingerStore(store))
	t.Cleanup(func() { sw.Close() })

	return api, store
}

func TestStingerListEndpoint(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	req := httptest.NewRequest("GET", "/api/stinger/list", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var names []string
	err := json.NewDecoder(rec.Body).Decode(&names)
	require.NoError(t, err)
	require.Equal(t, []string{"test-wipe"}, names)
}

func TestStingerDeleteEndpoint(t *testing.T) {
	api, store := setupStingerTestAPI(t)

	req := httptest.NewRequest("DELETE", "/api/stinger/test-wipe", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
	require.Empty(t, store.List(), "expected empty list after delete")
}

func TestStingerDeleteNotFound(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	req := httptest.NewRequest("DELETE", "/api/stinger/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestStingerCutPointEndpoint(t *testing.T) {
	api, store := setupStingerTestAPI(t)

	body := `{"cutPoint":0.3}`
	req := httptest.NewRequest("POST", "/api/stinger/test-wipe/cut-point", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	clip, ok := store.Get("test-wipe")
	require.True(t, ok, "stinger not found after cut point update")
	require.Equal(t, 0.3, clip.CutPoint)
}

func TestStingerCutPointInvalid(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	body := `{"cutPoint":1.5}`
	req := httptest.NewRequest("POST", "/api/stinger/test-wipe/cut-point", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestStingerCutPointNotFound(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	body := `{"cutPoint":0.3}`
	req := httptest.NewRequest("POST", "/api/stinger/nonexistent/cut-point", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestTransitionStingerTypeAccepted(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	body := `{"source":"camera2","type":"stinger","durationMs":500,"stingerName":"test-wipe"}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

func TestTransitionStingerMissingName(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	body := `{"source":"camera2","type":"stinger","durationMs":500}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestTransitionStingerClipNotFound(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	body := `{"source":"camera2","type":"stinger","durationMs":500,"stingerName":"nonexistent"}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestTransitionStingerNoStore(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	body := `{"source":"camera2","type":"stinger","durationMs":500,"stingerName":"test-wipe"}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code, "body: %s", rec.Body.String())
}
