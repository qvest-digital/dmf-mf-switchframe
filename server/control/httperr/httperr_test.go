package httperr

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrite(t *testing.T) {
	w := httptest.NewRecorder()
	Write(w, http.StatusBadRequest, "invalid input")

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	err := json.NewDecoder(w.Body).Decode(&body)
	require.NoError(t, err)
	require.Equal(t, "invalid input", body["error"])
}

func TestWriteErr(t *testing.T) {
	w := httptest.NewRecorder()
	WriteErr(w, http.StatusNotFound, errors.New("thing not found"))

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	err := json.NewDecoder(w.Body).Decode(&body)
	require.NoError(t, err)
	require.Equal(t, "thing not found", body["error"])
}

func TestWrite_500(t *testing.T) {
	w := httptest.NewRecorder()
	Write(w, http.StatusInternalServerError, "something broke")

	require.Equal(t, http.StatusInternalServerError, w.Code)
}
