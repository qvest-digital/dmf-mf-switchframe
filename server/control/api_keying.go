package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/graphics"
)

// registerKeyRoutes registers upstream key API routes on the given mux.
func (a *API) registerKeyRoutes(mux *http.ServeMux) {
	if a.keyer == nil {
		return
	}
	mux.HandleFunc("PUT /api/sources/{source}/key", timedHandlerWithPath(a.cmdQueue, "key_set", []string{"source"}, a.handleSetSourceKeyInner))
	mux.HandleFunc("GET /api/sources/{source}/key", a.handleGetSourceKey)
	mux.HandleFunc("DELETE /api/sources/{source}/key", timedHandlerWithPath(a.cmdQueue, "key_delete", []string{"source"}, a.handleDeleteSourceKeyInner))
}

// handleSetSourceKeyInner configures an upstream key for a source.
// body is the pre-read request body provided by timedHandlerWithPath.
func (a *API) handleSetSourceKeyInner(w http.ResponseWriter, r *http.Request, body []byte) {
	source := r.PathValue("source")
	var cfg graphics.KeyConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if cfg.Type != graphics.KeyTypeChroma && cfg.Type != graphics.KeyTypeLuma && cfg.Type != graphics.KeyTypeNeural {
		httperr.Write(w, http.StatusBadRequest, "type must be 'chroma', 'luma', or 'neural'")
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

// handleDeleteSourceKeyInner removes the key configuration for a source.
// body is the pre-read request body provided by timedHandlerWithPath.
func (a *API) handleDeleteSourceKeyInner(w http.ResponseWriter, r *http.Request, _ []byte) {
	source := r.PathValue("source")
	a.keyer.RemoveKey(source)
	if a.keyBridge != nil {
		a.keyBridge.RemoveFillSource(source)
	}
	w.WriteHeader(http.StatusNoContent)
}
