package control

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/dve"
)

func squeezeAPI(t *testing.T) (*API, *dve.Squeeze) {
	t.Helper()
	_, sw := setupTestAPI(t)
	sq := dve.NewSqueeze(1920, 1080)
	return NewAPI(sw, WithSqueeze(sq)), sq
}

const lFrame = `{"rect":{"x":480,"y":0,"w":1440,"h":810},"background":"camera2","durationMs":500,"easing":"ease-in-out"}`

func TestSqueezeRoutesAbsentWithoutSqueeze(t *testing.T) {
	api, _ := setupTestAPI(t)
	require.Equal(t, http.StatusNotFound, serve(t, api, "GET", "/api/squeezeback", "").Code)
}

func TestSqueezeGet(t *testing.T) {
	api, _ := squeezeAPI(t)
	rec := serve(t, api, "GET", "/api/squeezeback", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var st dve.SqueezeState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&st))
	require.False(t, st.Configured)
	require.False(t, st.On)
}

func TestSqueezeSet(t *testing.T) {
	api, sq := squeezeAPI(t)
	rec := serve(t, api, "PUT", "/api/squeezeback", lFrame)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var st dve.SqueezeState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&st))
	require.True(t, st.Configured)
	require.Equal(t, "camera2", st.Background)
	require.Equal(t, dve.Rect{X: 480, Y: 0, W: 1440, H: 810}, sq.Snapshot().Rect)
}

func TestSqueezeSetRejects(t *testing.T) {
	api, sq := squeezeAPI(t)
	for name, body := range map[string]string{
		"unknown background": `{"rect":{"x":0,"y":0,"w":960,"h":540},"background":"nope"}`,
		"odd rect":           `{"rect":{"x":1,"y":0,"w":960,"h":540}}`,
		"outside the frame":  `{"rect":{"x":1000,"y":0,"w":960,"h":540}}`,
		"empty rect":         `{"rect":{"x":0,"y":0,"w":0,"h":540}}`,
		"bad easing":         `{"rect":{"x":0,"y":0,"w":960,"h":540},"easing":"wobble"}`,
		"invalid json":       `{`,
	} {
		rec := serve(t, api, "PUT", "/api/squeezeback", body)
		require.Equal(t, http.StatusBadRequest, rec.Code, name)
	}
	require.False(t, sq.Snapshot().Configured, "nothing stored")
}

func TestSqueezeSetBlackBackground(t *testing.T) {
	api, _ := squeezeAPI(t)
	rec := serve(t, api, "PUT", "/api/squeezeback", `{"rect":{"x":0,"y":0,"w":960,"h":540}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestSqueezeOnOff(t *testing.T) {
	api, sq := squeezeAPI(t)
	rec := serve(t, api, "POST", "/api/squeezeback/on", "")
	require.Equal(t, http.StatusBadRequest, rec.Code, "on before configured")

	require.Equal(t, http.StatusOK, serve(t, api, "PUT", "/api/squeezeback", lFrame).Code)

	rec = serve(t, api, "POST", "/api/squeezeback/on", "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.True(t, sq.Snapshot().On)
	require.True(t, sq.Snapshot().Animating, "empty body takes the configured duration")
	require.Equal(t, "camera2", sq.BackgroundOnAir())

	rec = serve(t, api, "POST", "/api/squeezeback/off", `{"durationMs":0}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.False(t, sq.Snapshot().On)
	require.False(t, sq.Active(), "durationMs 0 cuts")

	rec = serve(t, api, "POST", "/api/squeezeback/on", `{"easing":"wobble"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.False(t, sq.Snapshot().On)
}
