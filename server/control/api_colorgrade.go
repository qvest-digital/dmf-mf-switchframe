package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/zsiec/switchframe/server/colorgrade"
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

	// Corrector and save-look routes registered BEFORE the DELETE {name} route
	// to prevent "corrector" or "save" from matching as a {name} parameter.
	mux.HandleFunc("GET /api/colorgrade/corrector", a.handleCorrectorGet)
	mux.HandleFunc("PUT /api/colorgrade/corrector", a.handleCorrectorSet)
	mux.HandleFunc("DELETE /api/colorgrade/corrector", a.handleCorrectorReset)
	mux.HandleFunc("POST /api/colorgrade/save", a.handleSaveLook)

	// Look settings (corrector recall) — registered BEFORE {name} catch-all.
	mux.HandleFunc("GET /api/colorgrade/looks/{name}/settings", a.handleLookSettings)

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
	defer func() { _ = file.Close() }()

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
		if errors.Is(err, colorgrade.ErrBuiltinLook) {
			httperr.Write(w, http.StatusForbidden, err.Error())
		} else if errors.Is(err, colorgrade.ErrLookNotFound) {
			httperr.Write(w, http.StatusNotFound, err.Error())
		} else {
			httperr.WriteErr(w, http.StatusBadRequest, err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ─── Corrector Endpoints ────────────────────────────────────────────────────

// handleCorrectorGet returns the current color corrector state.
func (a *API) handleCorrectorGet(w http.ResponseWriter, r *http.Request) {
	state := a.colorGradeMgr.GetCorrector()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

// handleCorrectorSet updates the color corrector state. The client sends
// the full CorrectorState; partial updates are not supported.
func (a *API) handleCorrectorSet(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	var state colorgrade.CorrectorState
	if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	// Validate ranges.
	if err := validateCorrectorState(state); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	a.colorGradeMgr.SetCorrector(state)
	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleCorrectorReset resets the color corrector to the neutral default.
func (a *API) handleCorrectorReset(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	a.colorGradeMgr.ResetCorrector()
	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleSaveLook bakes the current corrector + look into a new custom .cube file.
func (a *API) handleSaveLook(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if req.Name == "" {
		httperr.Write(w, http.StatusBadRequest, "name required")
		return
	}

	if err := validateStringLen("name", req.Name, MaxNameLen); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := a.colorGradeMgr.SaveCustomLook(req.Name); err != nil {
		httperr.WriteErr(w, http.StatusInternalServerError, err)
		return
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleLookSettings returns the saved corrector settings for a custom look.
func (a *API) handleLookSettings(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httperr.Write(w, http.StatusBadRequest, "name required")
		return
	}

	settings := a.colorGradeMgr.LoadLookSettings(name)
	if settings == nil {
		httperr.Write(w, http.StatusNotFound, "no corrector settings for this look")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(settings)
}

// validateCorrectorState checks that all corrector parameters are within valid ranges.
func validateCorrectorState(s colorgrade.CorrectorState) error {
	// Lift: RGB -1 to +1.
	for i, ch := range []string{"R", "G", "B"} {
		if err := validateRange("lift."+ch, float64(s.Lift[i]), -1, 1); err != nil {
			return err
		}
	}
	// Gamma: RGB 0.1 to 4.0.
	for i, ch := range []string{"R", "G", "B"} {
		if err := validateRange("gamma."+ch, float64(s.Gamma[i]), 0.1, 4.0); err != nil {
			return err
		}
	}
	// Gain: RGB 0 to 4.0.
	for i, ch := range []string{"R", "G", "B"} {
		if err := validateRange("gain."+ch, float64(s.Gain[i]), 0, 4.0); err != nil {
			return err
		}
	}
	// Temperature: -100 to +100.
	if err := validateRange("temperature", float64(s.Temperature), -100, 100); err != nil {
		return err
	}
	// Tint: -100 to +100.
	if err := validateRange("tint", float64(s.Tint), -100, 100); err != nil {
		return err
	}
	// Saturation: 0 to 2.0.
	if err := validateRange("saturation", float64(s.Saturation), 0, 2.0); err != nil {
		return err
	}
	// Contrast: -1 to +1.
	if err := validateRange("contrast", float64(s.Contrast), -1, 1); err != nil {
		return err
	}
	// Shadows, Midtones, Highlights: RGB -1 to +1.
	for _, zone := range []struct {
		name string
		vals [3]float32
	}{
		{"shadows", s.Shadows},
		{"midtones", s.Midtones},
		{"highlights", s.Highlights},
	} {
		for i, ch := range []string{"R", "G", "B"} {
			if err := validateRange(zone.name+"."+ch, float64(zone.vals[i]), -1, 1); err != nil {
				return err
			}
		}
	}
	// Sharpness: 0 to 1.0.
	if err := validateRange("sharpness", float64(s.Sharpness), 0, 1.0); err != nil {
		return err
	}
	// Curves: validate control points are in [0,1] range.
	for _, curve := range []struct {
		name string
		pts  []colorgrade.CurvePoint
	}{
		{"curveMaster", s.CurveMaster},
		{"curveR", s.CurveR},
		{"curveG", s.CurveG},
		{"curveB", s.CurveB},
	} {
		for j, pt := range curve.pts {
			if pt.X < 0 || pt.X > 1 || pt.Y < 0 || pt.Y > 1 {
				return fmt.Errorf("%s[%d]: x and y must be 0-1 (got x=%.2f, y=%.2f)",
					curve.name, j, pt.X, pt.Y)
			}
		}
	}
	return nil
}
