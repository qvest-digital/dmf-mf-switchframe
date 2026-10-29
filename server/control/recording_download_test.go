package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/switcher"
)

// downloadTestServer wires the API like the app does: a ticket bypasses
// AuthMiddleware for the download route only.
func downloadTestServer(t *testing.T) (*API, http.Handler, string) {
	t.Helper()
	recDir := t.TempDir()
	path := filepath.Join(recDir, "program_x_001.ts")
	require.NoError(t, os.WriteFile(path, []byte("recorded bytes"), 0o644))
	store, err := clip.NewStore(t.TempDir(), 100*1024*1024)
	require.NoError(t, err)
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	api := NewAPI(sw, WithClipStore(store), WithClipManager(clip.NewManager(store, clip.ManagerConfig{})), WithRecordingDir(recDir))
	mux := http.Handler(api.Mux())
	authed := AuthMiddleware("secret")(mux)
	return api, api.DownloadTicketMiddleware(authed, mux), path
}

func issueDownloadURL(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"path": path})
	req := httptest.NewRequest("POST", "/api/clips/recordings/download-url", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp.URL
}

// A browser download carries no Authorization header. Without a ticket it
// is refused, with one it gets the file once, and the URL is then dead.
func TestRecordingDownloadTicket(t *testing.T) {
	_, h, path := downloadTestServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", recordingDownloadPath+"?path="+path, nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code, "the download route stays authenticated")

	url := issueDownloadURL(t, h, path)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "recorded bytes", rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Disposition"), "program_x_001.ts")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code, "a ticket is single-use")
}

// A ticket opens only the file it was issued for, only on the download
// route, and only until it expires.
func TestRecordingDownloadTicketScope(t *testing.T) {
	api, h, path := downloadTestServer(t)
	other := filepath.Join(filepath.Dir(path), "program_y_001.ts")
	require.NoError(t, os.WriteFile(other, []byte("other"), 0o644))

	id, err := api.downloadTickets.issue(path)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", recordingDownloadPath+"?path="+other+"&ticket="+id, nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code, "another file")

	id, err = api.downloadTickets.issue(path)
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/clips/recordings?path="+path+"&ticket="+id, nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code, "another route still needs the bearer token, ticket and path or not")

	now := time.Now()
	api.downloadTickets.now = func() time.Time { return now }
	id, err = api.downloadTickets.issue(path)
	require.NoError(t, err)
	now = now.Add(downloadTicketTTL + time.Second)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", recordingDownloadPath+"?path="+path+"&ticket="+id, nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code, "expired")
}

// Tickets are issued only to an authenticated caller and only for files
// inside a recording directory.
func TestRecordingDownloadURLRequiresAuthAndRecordingDir(t *testing.T) {
	_, h, path := downloadTestServer(t)

	body, _ := json.Marshal(map[string]string{"path": path})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/clips/recordings/download-url", bytes.NewReader(body)))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	outside := filepath.Join(t.TempDir(), "secret.ts")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))
	body, _ = json.Marshal(map[string]string{"path": outside})
	req := httptest.NewRequest("POST", "/api/clips/recordings/download-url", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// A ticketed download is counted as such: it carries no bearer token, and a
// refused one is not an anonymous client.
func TestRecordingDownloadTicketCaller(t *testing.T) {
	api, h, path := downloadTestServer(t)
	h = RouteMetricsMiddleware(api.Mux())(h)
	route := "GET " + recordingDownloadPath
	ok := func() float64 { return requestsCount("GET", route, "200", CallerTicket) }
	refused := func() float64 { return requestsCount("GET", route, "401", CallerTicket) }
	okBefore, refusedBefore := ok(), refused()

	url := issueDownloadURL(t, h, path)
	for range 2 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", url, nil))
	}
	require.Equal(t, okBefore+1, ok())
	require.Equal(t, refusedBefore+1, refused(), "the second use of a ticket is refused")
}
