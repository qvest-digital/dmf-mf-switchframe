package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/control/httperr"
)

// encoderResponse is the JSON response for GET /api/encoder.
type encoderResponse struct {
	Current   string              `json:"current"`
	Available []codec.EncoderInfo `json:"available"`
}

// encoderRequest is the JSON body for PUT /api/encoder.
type encoderRequest struct {
	Encoder string `json:"encoder"`
}

// registerEncoderRoutes registers encoder-related API routes on the given mux.
func (a *API) registerEncoderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/encoder", a.handleGetEncoder)
	mux.HandleFunc("PUT /api/encoder", a.handleSetEncoder)
}

// handleGetEncoder returns the current encoder and available encoder options.
func (a *API) handleGetEncoder(w http.ResponseWriter, _ *http.Request) {
	resp := encoderResponse{
		Current:   a.switcher.EncoderName(),
		Available: a.switcher.AvailableEncoders(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleSetEncoder switches the video encoder at runtime.
func (a *API) handleSetEncoder(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	var req encoderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Encoder == "" {
		httperr.Write(w, http.StatusBadRequest, "encoder name required")
		return
	}
	if err := a.switcher.SetEncoder(req.Encoder); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}
