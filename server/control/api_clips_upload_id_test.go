package control

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/output"
)

// uploadTestTS returns a small valid H.264+AAC MPEG-TS stream.
func uploadTestTS(t *testing.T) []byte {
	t.Helper()
	sps := []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88}
	pps := []byte{0x68, 0xCE, 0x38, 0x80}
	avc1 := func(nalu []byte) []byte {
		n := len(nalu)
		return append([]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}, nalu...)
	}
	idr := make([]byte, 64)
	idr[0] = 0x65
	slice := make([]byte, 32)
	slice[0] = 0x41

	muxer := output.NewTSMuxer()
	var ts []byte
	muxer.SetOutput(func(b []byte) { ts = append(ts, b...) })
	for i := range 30 {
		pts := int64(90000 + i*3000)
		f := &media.VideoFrame{PTS: pts, DTS: pts, WireData: avc1(slice)}
		if i == 0 {
			f = &media.VideoFrame{PTS: pts, DTS: pts, IsKeyframe: true, SPS: sps, PPS: pps, WireData: avc1(idr), Codec: "h264"}
		}
		require.NoError(t, muxer.WriteVideo(f))
		aac := make([]byte, 128)
		aac[0] = 0x01
		require.NoError(t, muxer.WriteAudio(&media.AudioFrame{
			PTS: pts, Data: codec.EnsureADTS(aac, 48000, 2), SampleRate: 48000, Channels: 2,
		}))
	}
	return ts
}

func clipUploadRequest(t *testing.T, id string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if id != "" {
		require.NoError(t, mw.WriteField("id", id))
	}
	fw, err := mw.CreateFormFile("file", "shared.ts")
	require.NoError(t, err)
	_, err = fw.Write(data)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	req := httptest.NewRequest("POST", "/api/clips/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// Two engines given the same ID by the uploading client store the clip under
// that ID, so later ID-based requests resolve on both.
func TestHandleClipUploadWithClientID(t *testing.T) {
	api, store, _ := setupTestAPIWithClips(t)
	const id = "0123456789abcdef0123456789abcdef"

	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, clipUploadRequest(t, id, uploadTestTS(t)))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var got clip.Clip
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	require.Equal(t, id, got.ID)
	stored, err := store.Get(id)
	require.NoError(t, err)
	require.Equal(t, "shared", stored.Name)
	_, err = os.Stat(filepath.Join(store.Dir(), stored.Filename))
	require.NoError(t, err)

	// A second upload under the same ID is rejected and leaves no file behind.
	before, err := os.ReadDir(store.Dir())
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, clipUploadRequest(t, id, uploadTestTS(t)))
	require.Equal(t, http.StatusConflict, rec.Code)
	after, err := os.ReadDir(store.Dir())
	require.NoError(t, err)
	require.Len(t, after, len(before))
}

func TestHandleClipUploadWithoutClientIDGeneratesOne(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, clipUploadRequest(t, "", uploadTestTS(t)))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var got clip.Clip
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	require.True(t, clip.ValidID(got.ID), "generated id %q", got.ID)
}

func TestHandleClipUploadRejectsMalformedID(t *testing.T) {
	api, store, _ := setupTestAPIWithClips(t)

	for _, id := range []string{"../../etc/passwd", "ABCDEF0123456789ABCDEF0123456789", "abc"} {
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, clipUploadRequest(t, id, uploadTestTS(t)))
		require.Equal(t, http.StatusBadRequest, rec.Code, "id %q", id)
	}
	require.Empty(t, store.List())
}
