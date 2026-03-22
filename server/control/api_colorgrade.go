package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
)

// registerColorGradeRoutes registers color grading API routes on the given mux.
func (a *API) registerColorGradeRoutes(mux *http.ServeMux) {
	if a.colorGradeMgr == nil {
		return
	}

	mux.HandleFunc("GET /api/colorgrade", a.handleColorGradeGet)
	mux.HandleFunc("PUT /api/colorgrade", a.handleColorGradeSet)
	mux.HandleFunc("POST /api/colorgrade/upload", a.handleColorGradeUpload)
	mux.HandleFunc("DELETE /api/colorgrade/{name}", a.handleColorGradeDelete)
}

// handleColorGradeGet returns the current color grade state.
func (a *API) handleColorGradeGet(w http.ResponseWriter, r *http.Request) {
	state := a.colorGradeMgr.GetState()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

type colorGradeRequest struct {
	Enabled   *bool    `json:"enabled"`
	LookName  string   `json:"lookName"`
	Intensity *float32 `json:"intensity"`
}

// handleColorGradeSet enables/disables color grading or changes the look.
func (a *API) handleColorGradeSet(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	var req colorGradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	// If explicitly disabled, turn off.
	if req.Enabled != nil && !*req.Enabled {
		a.colorGradeMgr.DisableColorGrade()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a.enrichedState())
		return
	}

	// Determine intensity — default to 1.0 if not specified.
	intensity := float32(1.0)
	if req.Intensity != nil {
		intensity = *req.Intensity
		if intensity < 0 {
			intensity = 0
		}
		if intensity > 1 {
			intensity = 1
		}
	}

	if req.LookName == "" && req.Intensity != nil {
		// Intensity-only update: get current state and reapply with new intensity.
		state := a.colorGradeMgr.GetState()
		if !state.Enabled || state.LookName == "" {
			httperr.Write(w, http.StatusBadRequest, "no look active to adjust intensity")
			return
		}
		req.LookName = state.LookName
	}

	if req.LookName == "" {
		httperr.Write(w, http.StatusBadRequest, "lookName required")
		return
	}

	if err := a.colorGradeMgr.SetColorGrade(req.LookName, intensity); err != nil {
		httperr.WriteErr(w, http.StatusBadRequest, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleColorGradeUpload handles multipart .cube file upload.
func (a *API) handleColorGradeUpload(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	r.Body = http.MaxBytesReader(w, r.Body, 16<<20) // 16MB limit

	if err := r.ParseMultipartForm(16 << 20); err != nil {
		httperr.Write(w, http.StatusRequestEntityTooLarge, "upload too large")
		return
	}

	name := r.FormValue("name")
	if name == "" {
		httperr.Write(w, http.StatusBadRequest, "name required")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "file required")
		return
	}
	defer file.Close()

	if err := a.colorGradeMgr.UploadLUT(name, file); err != nil {
		httperr.WriteErr(w, http.StatusBadRequest, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(a.colorGradeMgr.GetState())
}

// handleColorGradeDelete removes an uploaded look.
func (a *API) handleColorGradeDelete(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	name := r.PathValue("name")
	if name == "" {
		httperr.Write(w, http.StatusBadRequest, "name required")
		return
	}

	if err := a.colorGradeMgr.DeleteLUT(name); err != nil {
		// Distinguish between "cannot delete builtin" (403) and "not found" (404).
		if contains(err.Error(), "built-in") {
			httperr.Write(w, http.StatusForbidden, err.Error())
		} else if contains(err.Error(), "not found") {
			httperr.Write(w, http.StatusNotFound, err.Error())
		} else {
			httperr.WriteErr(w, http.StatusBadRequest, err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
