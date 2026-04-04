package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/layout"
)

func TestDVEGetReturnsNullWhenNoDVELayout(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	req := httptest.NewRequest("GET", "/api/dve", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Nil(t, resp["layout"])
}

func TestDVESetWithValidLayout(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	body := `{
		"name": "test",
		"slots": [
			{
				"sourceKey": "cam1",
				"transform": {
					"positionX": 100, "positionY": 50,
					"width": 480, "height": 270,
					"opacity": 1.0
				},
				"zOrder": 1,
				"enabled": true
			}
		]
	}`
	req := httptest.NewRequest("PUT", "/api/dve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// Verify the layout was stored.
	dl := lc.GetDVELayout()
	require.NotNil(t, dl)
	require.Len(t, dl.Slots, 1)
	require.Equal(t, "cam1", dl.Slots[0].SourceKey)
	require.InDelta(t, 480.0, dl.Slots[0].Transform.Width, 0.01)
}

func TestDVESetWithInvalidLayout(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	// Width <= 0 should fail validation.
	body := `{
		"slots": [
			{
				"sourceKey": "cam1",
				"transform": {
					"positionX": 100, "positionY": 50,
					"width": 0, "height": 270,
					"opacity": 1.0
				},
				"zOrder": 0,
				"enabled": true
			}
		]
	}`
	req := httptest.NewRequest("PUT", "/api/dve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDVESetWithInvalidJSON(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	req := httptest.NewRequest("PUT", "/api/dve", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDVESetWithNoSlotsOrPreset(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	req := httptest.NewRequest("PUT", "/api/dve", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDVESetWithBuiltinPreset(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	body := `{"preset":"pip-top-right"}`
	req := httptest.NewRequest("PUT", "/api/dve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	dl := lc.GetDVELayout()
	require.NotNil(t, dl)
	require.Len(t, dl.Slots, 1)
	require.True(t, dl.Slots[0].Enabled)
}

func TestDVESetWithUnknownPreset(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	body := `{"preset":"nonexistent"}`
	req := httptest.NewRequest("PUT", "/api/dve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDVEDelete(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	// Set a DVE layout first.
	lc.SetDVELayout(&dve.Layout{
		Name:  "test",
		Slots: []dve.Slot{{Transform: dve.DefaultTransform(0, 0, 480, 270), Enabled: true}},
	})

	req := httptest.NewRequest("DELETE", "/api/dve", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Nil(t, lc.GetDVELayout())
}

func TestDVESlotUpdatePartial(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	// Set a DVE layout.
	lc.SetDVELayout(&dve.Layout{
		Name: "test",
		Slots: []dve.Slot{
			{
				SourceKey: "cam1",
				Transform: dve.DefaultTransform(0, 0, 480, 270),
				ZOrder:    0,
				Enabled:   true,
			},
		},
	})

	// Partial update: only change sourceKey.
	body := `{"sourceKey":"cam2"}`
	req := httptest.NewRequest("PUT", "/api/dve/slots/0", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)

	dl := lc.GetDVELayout()
	require.Equal(t, "cam2", dl.Slots[0].SourceKey)
	// Transform should remain unchanged.
	require.InDelta(t, 480.0, dl.Slots[0].Transform.Width, 0.01)
}

func TestDVESlotUpdateInvalidSlotID(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	lc.SetDVELayout(&dve.Layout{
		Name:  "test",
		Slots: []dve.Slot{{Transform: dve.DefaultTransform(0, 0, 480, 270), Enabled: true}},
	})

	// Slot 99 out of range.
	body := `{"sourceKey":"cam1"}`
	req := httptest.NewRequest("PUT", "/api/dve/slots/99", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDVESlotOn(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	lc.SetDVELayout(&dve.Layout{
		Name:  "test",
		Slots: []dve.Slot{{Transform: dve.DefaultTransform(0, 0, 480, 270), Enabled: false}},
	})

	req := httptest.NewRequest("POST", "/api/dve/slots/0/on", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.True(t, lc.GetDVELayout().Slots[0].Enabled)
}

func TestDVESlotOff(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	lc.SetDVELayout(&dve.Layout{
		Name:  "test",
		Slots: []dve.Slot{{Transform: dve.DefaultTransform(0, 0, 480, 270), Enabled: true}},
	})

	req := httptest.NewRequest("POST", "/api/dve/slots/0/off", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.False(t, lc.GetDVELayout().Slots[0].Enabled)
}

func TestDVESlotOnOutOfRange(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	lc.SetDVELayout(&dve.Layout{
		Name:  "test",
		Slots: []dve.Slot{{Transform: dve.DefaultTransform(0, 0, 480, 270), Enabled: true}},
	})

	req := httptest.NewRequest("POST", "/api/dve/slots/5/on", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDVESlotOffNoDVELayout(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	// No DVE layout set.
	req := httptest.NewRequest("POST", "/api/dve/slots/0/off", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDVEPresetStoreNilDoesNotPanic(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	// No dvePresetStore — preset routes should not be registered.
	api := NewAPI(sw, WithLayoutCompositor(lc))

	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/dve/presets", ""},
		{"POST", "/api/dve/presets", `{"name":"test"}`},
		{"PUT", "/api/dve/presets/test/recall", `{"durationMs":300}`},
		{"DELETE", "/api/dve/presets/test", ""},
	}

	for _, ep := range endpoints {
		var req *http.Request
		if ep.body != "" {
			req = httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(ep.method, ep.path, nil)
		}
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)

		require.True(t, rec.Code >= 400 && rec.Code < 500,
			"%s %s: expected 4xx, got %d: %s", ep.method, ep.path, rec.Code, rec.Body.String())
	}
}

func TestDVEPresetsCRUD(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	store, err := dve.NewPresetStore(t.TempDir())
	require.NoError(t, err)
	api := NewAPI(sw, WithLayoutCompositor(lc), WithDVEPresetStore(store))

	// Set a DVE layout to save as a preset.
	lc.SetDVELayout(&dve.Layout{
		Name: "test",
		Slots: []dve.Slot{
			{SourceKey: "cam1", Transform: dve.DefaultTransform(0, 0, 480, 270), Enabled: true},
		},
	})

	// Save preset.
	req := httptest.NewRequest("POST", "/api/dve/presets", strings.NewReader(`{"name":"My Preset"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	// List presets — should have built-in + saved.
	req = httptest.NewRequest("GET", "/api/dve/presets", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var presets []dve.PresetInfo
	err = json.NewDecoder(rec.Body).Decode(&presets)
	require.NoError(t, err)
	// Should have 8 built-in presets + 1 user preset.
	require.Len(t, presets, 9)

	// Find our custom preset.
	var found bool
	for _, p := range presets {
		if p.Name == "My Preset" {
			found = true
			break
		}
	}
	require.True(t, found, "saved preset not found in list")

	// Recall preset.
	req = httptest.NewRequest("PUT", "/api/dve/presets/my-preset/recall", strings.NewReader(`{"durationMs":500}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// Delete preset.
	req = httptest.NewRequest("DELETE", "/api/dve/presets/my-preset", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	// Verify deleted.
	req = httptest.NewRequest("GET", "/api/dve/presets", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var remaining []dve.PresetInfo
	err = json.NewDecoder(rec.Body).Decode(&remaining)
	require.NoError(t, err)
	require.Len(t, remaining, 8, "should only have built-in presets after delete")
}

func TestDVEGetAfterSet(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	// Set via API.
	body := `{
		"name": "roundtrip",
		"slots": [
			{
				"sourceKey": "cam1",
				"transform": {
					"positionX": 100, "positionY": 50,
					"width": 480, "height": 270,
					"opacity": 0.8,
					"rotationDeg": 15,
					"border": {"width": 3, "colorY": 235, "colorCb": 128, "colorCr": 128}
				},
				"zOrder": 1,
				"enabled": true
			}
		]
	}`
	req := httptest.NewRequest("PUT", "/api/dve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// GET should return the layout.
	req = httptest.NewRequest("GET", "/api/dve", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var dl dve.Layout
	err := json.NewDecoder(rec.Body).Decode(&dl)
	require.NoError(t, err)
	require.Equal(t, "roundtrip", dl.Name)
	require.Len(t, dl.Slots, 1)
	require.InDelta(t, 15.0, dl.Slots[0].Transform.RotationDeg, 0.01)
	require.InDelta(t, 0.8, dl.Slots[0].Transform.Opacity, 0.01)
}

func TestDVENoLayoutCompositor(t *testing.T) {
	_, sw := setupTestAPI(t)
	// No layout compositor — DVE routes should not be registered.
	api := NewAPI(sw)

	req := httptest.NewRequest("GET", "/api/dve", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// Should be 404 or 405 because routes weren't registered.
	require.True(t, rec.Code >= 400 && rec.Code < 500)
}

func TestDVESlotUpdateWithTransform(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	lc.SetDVELayout(&dve.Layout{
		Name: "test",
		Slots: []dve.Slot{
			{SourceKey: "cam1", Transform: dve.DefaultTransform(0, 0, 480, 270), Enabled: true},
		},
	})

	// Update with a full transform.
	body := `{
		"transform": {
			"positionX": 200, "positionY": 100,
			"width": 640, "height": 360,
			"opacity": 0.5,
			"rotationDeg": 45
		}
	}`
	req := httptest.NewRequest("PUT", "/api/dve/slots/0", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)

	dl := lc.GetDVELayout()
	require.InDelta(t, 200.0, dl.Slots[0].Transform.PositionX, 0.01)
	require.InDelta(t, 640.0, dl.Slots[0].Transform.Width, 0.01)
	require.InDelta(t, 45.0, dl.Slots[0].Transform.RotationDeg, 0.01)
	require.InDelta(t, 0.5, dl.Slots[0].Transform.Opacity, 0.01)
}

func TestDVESlotUpdateInvalidTransform(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)
	api := NewAPI(sw, WithLayoutCompositor(lc))

	lc.SetDVELayout(&dve.Layout{
		Name: "test",
		Slots: []dve.Slot{
			{SourceKey: "cam1", Transform: dve.DefaultTransform(0, 0, 480, 270), Enabled: true},
		},
	})

	// Width 0 is invalid.
	body := `{
		"transform": {
			"positionX": 200, "positionY": 100,
			"width": 0, "height": 360,
			"opacity": 1
		}
	}`
	req := httptest.NewRequest("PUT", "/api/dve/slots/0", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSanitizePresetID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My Preset", "my-preset"},
		{"PIP Top/Right", "pip-topright"},
		{"test_123", "test_123"},
		{"!@#$%", "preset"},
		{"Hello World!", "hello-world"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizePresetID(tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}
