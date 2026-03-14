package control

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/switcher"
)

// createPresetRequest is the JSON body for creating a preset.
type createPresetRequest struct {
	Name string `json:"name"`
}

// updatePresetRequest is the JSON body for updating a preset.
type updatePresetRequest struct {
	Name string `json:"name"`
}

// recallPresetResponse is the JSON response for recalling a preset.
type recallPresetResponse struct {
	Preset   preset.Preset `json:"preset"`
	Warnings []string      `json:"warnings,omitempty"`
}

// registerPresetRoutes registers preset-related API routes on the given mux.
func (a *API) registerPresetRoutes(mux *http.ServeMux) {
	if a.presetStore == nil {
		return
	}
	mux.HandleFunc("GET /api/presets", a.handleListPresets)
	mux.HandleFunc("POST /api/presets", a.handleCreatePreset)
	mux.HandleFunc("GET /api/presets/{id}", a.handleGetPreset)
	mux.HandleFunc("PUT /api/presets/{id}", a.handleUpdatePreset)
	mux.HandleFunc("DELETE /api/presets/{id}", a.handleDeletePreset)
	mux.HandleFunc("POST /api/presets/{id}/recall", a.handleRecallPreset)
}

// handleListPresets returns all presets.
func (a *API) handleListPresets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.presetStore.List())
}

// handleCreatePreset creates a new preset from the current switcher state.
func (a *API) handleCreatePreset(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	var req createPresetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Name == "" {
		httperr.Write(w, http.StatusBadRequest, "name required")
		return
	}

	state := a.enrichedState()
	snapshot := stateToSnapshot(state)

	p, err := a.presetStore.Create(req.Name, snapshot)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(p)
}

// handleGetPreset returns a single preset by ID.
func (a *API) handleGetPreset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := a.presetStore.Get(id)
	if !ok {
		httperr.Write(w, http.StatusNotFound, "preset not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

// handleUpdatePreset updates a preset's name.
func (a *API) handleUpdatePreset(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id := r.PathValue("id")
	var req updatePresetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	updates := preset.Update{}
	if req.Name != "" {
		updates.Name = &req.Name
	}

	p, err := a.presetStore.Update(id, updates)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

// handleDeletePreset deletes a preset by ID.
func (a *API) handleDeletePreset(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id := r.PathValue("id")
	if err := a.presetStore.Delete(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// handleRecallPreset applies a preset to the switcher and mixer.
func (a *API) handleRecallPreset(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id := r.PathValue("id")
	p, ok := a.presetStore.Get(id)
	if !ok {
		httperr.Write(w, http.StatusNotFound, "preset not found")
		return
	}

	target := &apiRecallTarget{
		switcher: a.switcher,
		mixer:    a.mixer,
	}

	warnings := preset.Recall(r.Context(), p, target)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(recallPresetResponse{
		Preset:   p,
		Warnings: warnings,
	})
}

// apiRecallTarget adapts the API's switcher and mixer to the preset.RecallTarget
// interface so Recall() can apply presets without knowing about concrete types.
type apiRecallTarget struct {
	switcher *switcher.Switcher
	mixer    AudioMixerAPI
}

func (t *apiRecallTarget) Cut(ctx context.Context, source string) error {
	return t.switcher.Cut(ctx, source)
}

func (t *apiRecallTarget) SetPreview(ctx context.Context, source string) error {
	return t.switcher.SetPreview(ctx, source)
}

func (t *apiRecallTarget) SetLevel(sourceKey string, levelDB float64) error {
	if t.mixer == nil {
		return nil
	}
	return t.mixer.SetLevel(sourceKey, levelDB)
}

func (t *apiRecallTarget) SetMuted(sourceKey string, muted bool) error {
	if t.mixer == nil {
		return nil
	}
	return t.mixer.SetMuted(sourceKey, muted)
}

func (t *apiRecallTarget) SetAFV(sourceKey string, afv bool) error {
	if t.mixer == nil {
		return nil
	}
	return t.mixer.SetAFV(sourceKey, afv)
}

func (t *apiRecallTarget) SetMasterLevel(level float64) {
	if t.mixer == nil {
		return
	}
	t.mixer.SetMasterLevel(level)
}

// stateToSnapshot converts a ControlRoomState to a ControlRoomSnapshot
// for creating presets from the current state.
func stateToSnapshot(state internal.ControlRoomState) preset.ControlRoomSnapshot {
	channels := make(map[string]preset.AudioChannelSnapshot, len(state.AudioChannels))
	for k, ch := range state.AudioChannels {
		channels[k] = preset.AudioChannelSnapshot{
			Level: ch.Level,
			Muted: ch.Muted,
			AFV:   ch.AFV,
		}
	}
	return preset.ControlRoomSnapshot{
		ProgramSource:        state.ProgramSource,
		PreviewSource:        state.PreviewSource,
		TransitionType:       state.TransitionType,
		TransitionDurationMs: state.TransitionDurationMs,
		AudioChannels:        channels,
		MasterLevel:          state.MasterLevel,
	}
}
