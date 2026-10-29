package control

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/control/httperr"
)

// recordingDownloadPath is the one route a download ticket opens.
const recordingDownloadPath = "/api/clips/recordings/download"

// downloadTicketTTL bounds how long an unused ticket stays valid: long
// enough for the browser to start the download it was issued for.
const downloadTicketTTL = time.Minute

// downloadTickets are single-use grants to download one recording file
// without an Authorization header. A browser download is a navigation and
// cannot carry the bearer token the rest of the API takes, and fetching a
// recording into a blob instead would hold the whole file in memory.
type downloadTickets struct {
	mu      sync.Mutex
	tickets map[string]downloadTicket
	now     func() time.Time // test hook; time.Now when nil
}

type downloadTicket struct {
	path    string
	expires time.Time
}

func (t *downloadTickets) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// issue returns a new ticket for path.
func (t *downloadTickets) issue(path string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	now := t.clock()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tickets == nil {
		t.tickets = map[string]downloadTicket{}
	}
	for k, v := range t.tickets {
		if now.After(v.expires) {
			delete(t.tickets, k)
		}
	}
	t.tickets[id] = downloadTicket{path: path, expires: now.Add(downloadTicketTTL)}
	return id, nil
}

// redeem consumes the ticket id if it is unexpired and was issued for path.
func (t *downloadTickets) redeem(id, path string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	tk, ok := t.tickets[id]
	if !ok {
		return false
	}
	delete(t.tickets, id)
	return !t.clock().After(tk.expires) &&
		subtle.ConstantTimeCompare([]byte(tk.path), []byte(path)) == 1
}

// handleRecordingDownloadURL issues a download URL for one recording file,
// valid for a single request within downloadTicketTTL.
func (a *API) handleRecordingDownloadURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		httperr.Write(w, http.StatusBadRequest, "path required")
		return
	}
	absPath, err := filepath.Abs(req.Path)
	if err != nil || !a.inRecordingDirs(absPath) {
		httperr.Write(w, http.StatusForbidden, "path outside recording directory")
		return
	}
	if info, err := os.Stat(absPath); err != nil || info.IsDir() {
		httperr.Write(w, http.StatusNotFound, "recording not found")
		return
	}
	id, err := a.downloadTickets.issue(absPath)
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "ticket not issued")
		return
	}
	q := url.Values{"path": {absPath}, "ticket": {id}}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": recordingDownloadPath + "?" + q.Encode()})
}

// DownloadTicketMiddleware sends a recording download that carries a
// ticket to direct, which skips authentication, and every other request to
// authed. A ticket that is unknown, expired, used or issued for another
// file is refused outright rather than falling back to authed.
func (a *API) DownloadTicketMiddleware(authed, direct http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ticket := r.URL.Query().Get("ticket")
		if ticket == "" || r.Method != http.MethodGet || r.URL.Path != recordingDownloadPath {
			authed.ServeHTTP(w, r)
			return
		}
		setCaller(r, CallerTicket)
		absPath, err := filepath.Abs(r.URL.Query().Get("path"))
		if err != nil || !a.downloadTickets.redeem(ticket, absPath) {
			httperr.Write(w, http.StatusUnauthorized, "download link expired or already used")
			return
		}
		direct.ServeHTTP(w, r)
	})
}
