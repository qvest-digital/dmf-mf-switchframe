package control

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/transition"
)

// graphicsFrameRequest is the JSON body for the graphics frame upload endpoint.
type graphicsFrameRequest struct {
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Template string `json:"template"`
	RGBA     []byte `json:"rgba"`
}

// handleGraphicsOn activates the overlay immediately (CUT ON).
func (a *API) handleGraphicsOn(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if err := a.compositor.On(); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsOff deactivates the overlay immediately (CUT OFF).
func (a *API) handleGraphicsOff(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if err := a.compositor.Off(); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsAutoOn starts a 500ms fade-in transition (AUTO ON).
func (a *API) handleGraphicsAutoOn(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if err := a.compositor.AutoOn(500 * time.Millisecond); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsAutoOff starts a 500ms fade-out transition (AUTO OFF).
func (a *API) handleGraphicsAutoOff(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if err := a.compositor.AutoOff(500 * time.Millisecond); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsStatus returns the current graphics overlay state.
func (a *API) handleGraphicsStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsFrame receives an RGBA overlay frame from the browser.
// The body is a JSON object with width, height, template name, and base64-encoded RGBA data.
func (a *API) handleGraphicsFrame(w http.ResponseWriter, r *http.Request) {
	body := io.LimitReader(r.Body, 16*1024*1024)

	var req graphicsFrameRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Width <= 0 || req.Height <= 0 {
		httperr.Write(w, http.StatusBadRequest, "width and height must be positive")
		return
	}
	if req.Width > 3840 || req.Height > 2160 {
		httperr.Write(w, http.StatusBadRequest, "resolution exceeds 4K limit")
		return
	}
	expected := req.Width * req.Height * 4
	if len(req.RGBA) != expected {
		httperr.Write(w, http.StatusBadRequest, "rgba data size mismatch")
		return
	}

	if err := a.compositor.SetOverlay(req.RGBA, req.Width, req.Height, req.Template); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleStingerList returns all loaded stinger clip names.
func (a *API) handleStingerList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.stingerStore.List())
}

// handleStingerDelete removes a stinger clip by name.
func (a *API) handleStingerDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := a.stingerStore.Delete(name); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stingerCutPointRequest is the JSON body for updating a stinger's cut point.
type stingerCutPointRequest struct {
	CutPoint float64 `json:"cutPoint"`
}

// handleStingerCutPoint updates the cut point for a stinger clip.
func (a *API) handleStingerCutPoint(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req stingerCutPointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.stingerStore.SetCutPoint(name, req.CutPoint); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleStingerUpload accepts a zip file upload containing PNG frames for a stinger.
func (a *API) handleStingerUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)

	data, err := io.ReadAll(r.Body)
	if err != nil {
		httperr.Write(w, http.StatusRequestEntityTooLarge, "upload too large (max 256MB)")
		return
	}

	if err := a.stingerStore.Upload(name, data); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// clipToStingerData converts a stinger.StingerClip to transition.StingerData.
func clipToStingerData(clip *stinger.StingerClip) *transition.StingerData {
	frames := make([]transition.StingerFrameData, len(clip.Frames))
	for i, f := range clip.Frames {
		frames[i] = transition.StingerFrameData{
			YUV:   f.YUV,
			Alpha: f.Alpha,
		}
	}
	return &transition.StingerData{
		Frames:   frames,
		Width:    clip.Width,
		Height:   clip.Height,
		CutPoint: clip.CutPoint,
	}
}
