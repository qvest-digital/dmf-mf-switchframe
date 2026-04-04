package control

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/dve"
)

type dveLayoutRequest struct {
	Preset string     `json:"preset,omitempty"`
	Name   string     `json:"name,omitempty"`
	Slots  []dve.Slot `json:"slots,omitempty"`
}

type dveSlotUpdateRequest struct {
	SourceKey *string        `json:"sourceKey,omitempty"`
	Transform *dve.Transform `json:"transform,omitempty"`
	ZOrder    *int           `json:"zOrder,omitempty"`
	Enabled   *bool          `json:"enabled,omitempty"`
}

type dvePresetSaveRequest struct {
	Name string `json:"name"`
}

type dvePresetRecallRequest struct {
	DurationMs int    `json:"durationMs,omitempty"`
	Easing     string `json:"easing,omitempty"`
}

// registerDVERoutes registers DVE-related API routes on the given mux.
func (a *API) registerDVERoutes(mux *http.ServeMux) {
	if a.layoutCompositor == nil {
		return
	}
	mux.HandleFunc("GET /api/dve", a.handleGetDVE)
	mux.HandleFunc("PUT /api/dve", a.handleSetDVE)
	mux.HandleFunc("DELETE /api/dve", a.handleDeleteDVE)
	mux.HandleFunc("PUT /api/dve/slots/{id}", a.handleDVESlotUpdate)
	mux.HandleFunc("POST /api/dve/slots/{id}/on", a.handleDVESlotOn)
	mux.HandleFunc("POST /api/dve/slots/{id}/off", a.handleDVESlotOff)
	if a.dvePresetStore != nil {
		mux.HandleFunc("GET /api/dve/presets", a.handleListDVEPresets)
		mux.HandleFunc("POST /api/dve/presets", a.handleSaveDVEPreset)
		mux.HandleFunc("PUT /api/dve/presets/{id}/recall", a.handleRecallDVEPreset)
		mux.HandleFunc("DELETE /api/dve/presets/{id}", a.handleDeleteDVEPreset)
	}
}

func (a *API) handleGetDVE(w http.ResponseWriter, r *http.Request) {
	dl := a.layoutCompositor.GetDVELayout()
	w.Header().Set("Content-Type", "application/json")
	if dl == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"layout": nil})
		return
	}
	_ = json.NewEncoder(w).Encode(dl)
}

func (a *API) handleSetDVE(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	var req dveLayoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	format := a.switcher.PipelineFormat()

	var dl *dve.Layout
	if req.Preset != "" {
		// Try built-in presets first, then user presets.
		dl = dveLayoutFromPreset(dve.ResolveBuiltinPreset(req.Preset, format.Width, format.Height))
		if dl == nil && a.dvePresetStore != nil {
			stored, err := a.dvePresetStore.Load(req.Preset)
			if err != nil {
				httperr.Write(w, http.StatusNotFound, "preset not found: "+req.Preset)
				return
			}
			dl = dveLayoutFromPreset(stored)
		}
		if dl == nil {
			httperr.Write(w, http.StatusNotFound, "preset not found: "+req.Preset)
			return
		}
	} else if len(req.Slots) > 0 {
		name := req.Name
		if name == "" {
			name = "custom"
		}
		dl = &dve.Layout{Name: name, Slots: req.Slots}
	} else {
		httperr.Write(w, http.StatusBadRequest, "provide preset name or slots")
		return
	}

	if err := dve.ValidateLayout(dl); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	a.layoutCompositor.SetDVELayout(dl)
	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dl)
}

func (a *API) handleDeleteDVE(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	a.layoutCompositor.SetDVELayout(nil)
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

// parseDVESlotID parses the slot ID from the request path and validates
// it against the current DVE layout bounds. Returns the validated ID or
// writes an error response and returns -1.
func (a *API) parseDVESlotID(w http.ResponseWriter, r *http.Request) int {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid slot id")
		return -1
	}
	dl := a.layoutCompositor.GetDVELayout()
	if id < 0 || dl == nil || id >= len(dl.Slots) {
		httperr.Write(w, http.StatusBadRequest, "slot id out of range")
		return -1
	}
	return id
}

func (a *API) handleDVESlotOn(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id := a.parseDVESlotID(w, r)
	if id < 0 {
		return
	}
	dl := a.layoutCompositor.GetDVELayout()
	if dl == nil || id >= len(dl.Slots) {
		httperr.Write(w, http.StatusNotFound, "DVE layout changed")
		return
	}
	// Deep copy to avoid mutating the live layout read by concurrent goroutines.
	cp := *dl
	cp.Slots = make([]dve.Slot, len(dl.Slots))
	copy(cp.Slots, dl.Slots)
	cp.Slots[id].Enabled = true
	a.layoutCompositor.SetDVELayout(&cp)
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleDVESlotOff(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id := a.parseDVESlotID(w, r)
	if id < 0 {
		return
	}
	dl := a.layoutCompositor.GetDVELayout()
	if dl == nil || id >= len(dl.Slots) {
		httperr.Write(w, http.StatusNotFound, "DVE layout changed")
		return
	}
	// Deep copy to avoid mutating the live layout read by concurrent goroutines.
	cp := *dl
	cp.Slots = make([]dve.Slot, len(dl.Slots))
	copy(cp.Slots, dl.Slots)
	cp.Slots[id].Enabled = false
	a.layoutCompositor.SetDVELayout(&cp)
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleDVESlotUpdate(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id := a.parseDVESlotID(w, r)
	if id < 0 {
		return
	}

	var req dveSlotUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	dl := a.layoutCompositor.GetDVELayout()
	if dl == nil || id >= len(dl.Slots) {
		httperr.Write(w, http.StatusNotFound, "DVE layout changed")
		return
	}
	// Deep copy to avoid mutating the live layout read by concurrent goroutines.
	cp := *dl
	cp.Slots = make([]dve.Slot, len(dl.Slots))
	copy(cp.Slots, dl.Slots)
	slot := &cp.Slots[id]

	if req.SourceKey != nil {
		slot.SourceKey = *req.SourceKey
	}
	if req.Transform != nil {
		slot.Transform = *req.Transform
	}
	if req.ZOrder != nil {
		slot.ZOrder = *req.ZOrder
	}
	if req.Enabled != nil {
		slot.Enabled = *req.Enabled
	}

	if err := dve.ValidateSlot(*slot); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	a.layoutCompositor.SetDVELayout(&cp)
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleListDVEPresets(w http.ResponseWriter, r *http.Request) {
	format := a.switcher.PipelineFormat()

	// Collect built-in preset infos.
	builtins := dve.BuiltinPresets(format.Width, format.Height)
	var all []dve.PresetInfo
	for _, p := range builtins {
		all = append(all, dve.PresetInfo{ID: p.ID, Name: p.Name})
	}

	// Append user presets.
	if a.dvePresetStore != nil {
		userPresets, err := a.dvePresetStore.List()
		if err == nil {
			all = append(all, userPresets...)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(all)
}

func (a *API) handleSaveDVEPreset(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	dl := a.layoutCompositor.GetDVELayout()
	if dl == nil {
		httperr.Write(w, http.StatusBadRequest, "no active DVE layout to save")
		return
	}

	var req dvePresetSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		httperr.Write(w, http.StatusBadRequest, "provide a name")
		return
	}
	if err := validateStringLen("name", req.Name, MaxNameLen); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	id := sanitizePresetID(req.Name)
	preset := &dve.Preset{
		ID:    id,
		Name:  req.Name,
		Slots: make([]dve.Slot, len(dl.Slots)),
	}
	copy(preset.Slots, dl.Slots)

	if err := a.dvePresetStore.Save(preset); err != nil {
		httperr.Write(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusCreated)
}

func (a *API) handleRecallDVEPreset(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id := r.PathValue("id")

	var req dvePresetRecallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	format := a.switcher.PipelineFormat()

	// Try built-in first, then user presets.
	var preset *dve.Preset
	preset = dve.ResolveBuiltinPreset(id, format.Width, format.Height)
	if preset == nil && a.dvePresetStore != nil {
		var err error
		preset, err = a.dvePresetStore.Load(id)
		if err != nil {
			httperr.Write(w, http.StatusNotFound, "preset not found: "+id)
			return
		}
	}
	if preset == nil {
		httperr.Write(w, http.StatusNotFound, "preset not found: "+id)
		return
	}

	targetLayout := dveLayoutFromPreset(preset)

	// Animated preset recall requires a compositor animation ticker (not yet implemented).
	// For now, presets apply instantly. The durationMs/easing fields are accepted in the
	// request for forward compatibility but have no effect until the animation system
	// is wired up.

	// Apply target layout immediately.
	a.layoutCompositor.SetDVELayout(targetLayout)
	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(targetLayout)
}

func (a *API) handleDeleteDVEPreset(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id := r.PathValue("id")
	if err := a.dvePresetStore.Delete(id); err != nil {
		httperr.Write(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// dveLayoutFromPreset converts a DVE Preset to a DVE Layout. Returns nil if preset is nil.
func dveLayoutFromPreset(p *dve.Preset) *dve.Layout {
	if p == nil {
		return nil
	}
	return &dve.Layout{
		Name:  p.Name,
		Slots: p.Slots,
	}
}

// sanitizePresetID creates a filesystem-safe ID from a name.
func sanitizePresetID(name string) string {
	id := strings.ToLower(name)
	id = strings.ReplaceAll(id, " ", "-")
	// Remove characters that are not alphanumeric, hyphens, or underscores.
	var safe []byte
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			safe = append(safe, c)
		}
	}
	if len(safe) == 0 {
		return "preset"
	}
	return string(safe)
}
