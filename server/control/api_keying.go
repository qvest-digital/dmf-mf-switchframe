package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/graphics"
)

// handleSetSourceKey configures an upstream key for a source.
func (a *API) handleSetSourceKey(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	var cfg graphics.KeyConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if cfg.Type != graphics.KeyTypeChroma && cfg.Type != graphics.KeyTypeLuma {
		httperr.Write(w, http.StatusBadRequest, "type must be 'chroma' or 'luma'")
		return
	}
	a.keyer.SetKey(source, cfg)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}

// handleGetSourceKey returns the current key configuration for a source.
func (a *API) handleGetSourceKey(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	cfg, ok := a.keyer.GetKey(source)
	if !ok {
		httperr.Write(w, http.StatusNotFound, "no key configured for source")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}

// handleDeleteSourceKey removes the key configuration for a source.
func (a *API) handleDeleteSourceKey(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	a.keyer.RemoveKey(source)
	w.WriteHeader(http.StatusNoContent)
}
