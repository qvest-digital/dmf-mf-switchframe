package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/control/httperr"
)

type captionModeRequest struct {
	Mode string `json:"mode"`
}

type captionTextRequest struct {
	Text    string `json:"text,omitempty"`
	Newline bool   `json:"newline,omitempty"`
	Clear   bool   `json:"clear,omitempty"`
}

// handleCaptionMode sets the caption operating mode (off/passthrough/author).
func (a *API) handleCaptionMode(w http.ResponseWriter, r *http.Request) {
	if a.captionMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "captions not enabled")
		return
	}

	var req captionModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	mode, ok := caption.ParseMode(req.Mode)
	if !ok {
		httperr.Write(w, http.StatusBadRequest, "invalid mode: must be off, passthrough, or author")
		return
	}

	a.captionMgr.SetMode(mode)
	a.setLastOperator(r)

	state := a.captionMgr.State()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

// handleCaptionText ingests caption text, triggers a newline, or clears the display.
func (a *API) handleCaptionText(w http.ResponseWriter, r *http.Request) {
	if a.captionMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "captions not enabled")
		return
	}

	var req captionTextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if a.captionMgr.Mode() != caption.ModeAuthor {
		httperr.Write(w, http.StatusBadRequest, "caption text input requires author mode")
		return
	}

	if req.Clear {
		a.captionMgr.Clear()
	} else if req.Newline {
		a.captionMgr.IngestNewline()
	} else if req.Text != "" {
		a.captionMgr.IngestText(req.Text)
	} else {
		httperr.Write(w, http.StatusBadRequest, "must provide text, newline, or clear")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.captionMgr.State())
}

// handleCaptionState returns the current caption system state.
func (a *API) handleCaptionState(w http.ResponseWriter, r *http.Request) {
	if a.captionMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "captions not enabled")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.captionMgr.State())
}
