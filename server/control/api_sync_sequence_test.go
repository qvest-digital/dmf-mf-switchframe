package control

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/switcher"
)

func sequenceZIP(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.SetNRGBA(1, 1, color.NRGBA{R: 255, A: 255})
	var p bytes.Buffer
	require.NoError(t, png.Encode(&p, img))
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("loop_0001.png")
	require.NoError(t, err)
	_, err = fw.Write(p.Bytes())
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// The peer engine reads a layer's sequence as it was uploaded, and only
// while the layer still holds the one it asks for.
func TestGetGraphicsSequence(t *testing.T) {
	comp := graphics.NewCompositor()
	defer comp.Close()
	se := graphics.NewSequenceEngine(comp)
	defer se.Close()
	require.NoError(t, comp.AddLayerWithID(4))
	data := sequenceZIP(t)
	require.NoError(t, se.UploadAsync(4, data, 25))

	api := NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()), WithCompositor(comp), WithSequenceEngine(se))
	api.SetSnapshotProvider(&mockSnapshotProvider{})

	sum := sha256.Sum256(data)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("GET", "/api/sync/graphics-sequence?layer=4&hash="+hex.EncodeToString(sum[:]), nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, data, rec.Body.Bytes())

	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("GET", "/api/sync/graphics-sequence?layer=4&hash=other", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("GET", "/api/sync/graphics-sequence?layer=5", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}
