package control

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func serve(t *testing.T, api *API, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	return rec
}

// The UI adds a graphics layer on both engines of a pair and addresses it
// by one ID afterwards. Each engine numbers layers itself, so once one has
// handed out IDs the other has not, as after a restart, the layer the UI
// adds has to take the ID the request names on both.
func TestGraphicsAddLayer_TakesRequestID(t *testing.T) {
	a, _ := setupGraphicsTestAPI(t)
	b, _ := setupGraphicsTestAPI(t)
	for range 3 {
		addLayerViaAPI(t, a)
	}
	require.Equal(t, http.StatusNoContent, serve(t, a, "DELETE", "/api/graphics/2", "").Code)

	for _, api := range []*API{a, b} {
		rec := serve(t, api, "POST", "/api/graphics", `{"id":3}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var got map[string]int
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
		require.Equal(t, 3, got["id"])
	}
	for _, api := range []*API{a, b} {
		require.Equal(t, http.StatusNoContent, serve(t, api, "DELETE", "/api/graphics/3", "").Code)
	}

	// A layer added without an ID never takes one handed out before.
	require.Equal(t, 4, addLayerViaAPI(t, b))
}

func TestGraphicsAddLayer_RefusesTakenOrNegativeID(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)
	require.Equal(t, http.StatusCreated, serve(t, api, "POST", "/api/graphics", `{"id":1}`).Code)
	require.Equal(t, http.StatusConflict, serve(t, api, "POST", "/api/graphics", `{"id":1}`).Code)
	require.Equal(t, http.StatusBadRequest, serve(t, api, "POST", "/api/graphics", `{"id":-1}`).Code)

	// An unbounded ID would carry the next automatic ID past the int range
	// into negative numbers, which the peer engine then refuses. The
	// bound keeps the next ID within a 32-bit int.
	require.Equal(t, http.StatusBadRequest, serve(t, api, "POST", "/api/graphics", `{"id":2147483647}`).Code)
	require.Equal(t, http.StatusBadRequest, serve(t, api, "POST", "/api/graphics", `{"id":9223372036854775807}`).Code)
	require.Equal(t, 2, addLayerViaAPI(t, api))

	// The highest ID taken leaves the next automatic one at MaxInt32.
	require.Equal(t, http.StatusCreated, serve(t, api, "POST", "/api/graphics", `{"id":2147483646}`).Code)
	require.Equal(t, math.MaxInt32, addLayerViaAPI(t, api))
}

// A pod created on both engines of a pair is armed, fired and deleted by
// one ID, so it has to take the ID the create request names on both.
func TestPodCreate_TakesRequestID(t *testing.T) {
	const body = `{"id":"pod-0a1b2c3d","config":{"type":"quarter","duration_ms":30000}}`
	for _, api := range []*API{setupTestPlayoutAPI(t), setupTestPlayoutAPI(t)} {
		rec := serve(t, api, "POST", "/api/playout/channels/1/pods", body)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var pod struct{ ID string }
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&pod))
		require.Equal(t, "pod-0a1b2c3d", pod.ID)
		require.Equal(t, http.StatusNoContent, serve(t, api, "POST", "/api/playout/channels/1/pods/pod-0a1b2c3d/arm", "{}").Code)

		require.Equal(t, http.StatusConflict, serve(t, api, "POST", "/api/playout/channels/1/pods", body).Code)
	}
	api := setupTestPlayoutAPI(t)
	require.Equal(t, http.StatusBadRequest,
		serve(t, api, "POST", "/api/playout/channels/1/pods", `{"id":"../x","config":{"type":"quarter"}}`).Code)
}
