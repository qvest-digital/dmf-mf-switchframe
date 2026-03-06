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

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

func setupStingerTestAPI(t *testing.T) (*API, *stinger.StingerStore) {
	t.Helper()

	// Create temp dir with a test stinger
	dir := t.TempDir()
	stingerDir := filepath.Join(dir, "test-wipe")
	if err := os.MkdirAll(stingerDir, 0755); err != nil {
		t.Fatal(err)
	}

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
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			t.Fatal(err)
		}
		f.Close()
	}

	store, err := stinger.NewStingerStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)
	sw.SetTransitionConfig(switcher.TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(320, 240), nil
		},
		EncoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	})
	sw.Cut(context.Background(), "camera1")

	api := NewAPI(sw, WithStingerStore(store))
	t.Cleanup(func() { sw.Close() })

	return api, store
}

func TestStingerListEndpoint(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	req := httptest.NewRequest("GET", "/api/stinger/list", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var names []string
	if err := json.NewDecoder(rec.Body).Decode(&names); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(names) != 1 || names[0] != "test-wipe" {
		t.Errorf("got names %v, want [test-wipe]", names)
	}
}

func TestStingerDeleteEndpoint(t *testing.T) {
	api, store := setupStingerTestAPI(t)

	req := httptest.NewRequest("DELETE", "/api/stinger/test-wipe", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	if names := store.List(); len(names) != 0 {
		t.Errorf("expected empty list after delete, got %v", names)
	}
}

func TestStingerDeleteNotFound(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	req := httptest.NewRequest("DELETE", "/api/stinger/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestStingerCutPointEndpoint(t *testing.T) {
	api, store := setupStingerTestAPI(t)

	body := `{"cutPoint":0.3}`
	req := httptest.NewRequest("POST", "/api/stinger/test-wipe/cut-point", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	clip, ok := store.Get("test-wipe")
	if !ok {
		t.Fatal("stinger not found after cut point update")
	}
	if clip.CutPoint != 0.3 {
		t.Errorf("CutPoint = %f, want 0.3", clip.CutPoint)
	}
}

func TestStingerCutPointInvalid(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	body := `{"cutPoint":1.5}`
	req := httptest.NewRequest("POST", "/api/stinger/test-wipe/cut-point", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestStingerCutPointNotFound(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	body := `{"cutPoint":0.3}`
	req := httptest.NewRequest("POST", "/api/stinger/nonexistent/cut-point", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestTransitionStingerTypeAccepted(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	body := `{"source":"camera2","type":"stinger","durationMs":500,"stingerName":"test-wipe"}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestTransitionStingerMissingName(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	body := `{"source":"camera2","type":"stinger","durationMs":500}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestTransitionStingerClipNotFound(t *testing.T) {
	api, _ := setupStingerTestAPI(t)

	body := `{"source":"camera2","type":"stinger","durationMs":500,"stingerName":"nonexistent"}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestTransitionStingerNoStore(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	body := `{"source":"camera2","type":"stinger","durationMs":500,"stingerName":"test-wipe"}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
}
