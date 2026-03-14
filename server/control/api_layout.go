package control

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/layout"
)

type layoutRequest struct {
	Preset string              `json:"preset,omitempty"`
	Slots  []layout.Slot `json:"slots,omitempty"`
	Name   string              `json:"name,omitempty"`
}

type slotUpdateRequest struct {
	SourceKey  string                 `json:"sourceKey,omitempty"`
	X          *int                   `json:"x,omitempty"`
	Y          *int                   `json:"y,omitempty"`
	Width      *int                   `json:"width,omitempty"`
	Height     *int                   `json:"height,omitempty"`
	Border     *layout.BorderConfig   `json:"border,omitempty"`
	Transition *layout.SlotTransition `json:"transition,omitempty"`
	ScaleMode  *string                `json:"scaleMode,omitempty"`
	CropAnchor *[2]float64            `json:"cropAnchor,omitempty"`
}

func (a *API) handleGetLayout(w http.ResponseWriter, r *http.Request) {
	l := a.layoutCompositor.GetLayout()
	w.Header().Set("Content-Type", "application/json")
	if l == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"layout": nil})
		return
	}
	_ = json.NewEncoder(w).Encode(l)
}

func (a *API) handleSetLayout(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	var req layoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	format := a.switcher.PipelineFormat()

	var l *layout.Layout
	if req.Preset != "" {
		// Try built-in presets first, then user presets.
		l = layout.ResolveBuiltinPreset(req.Preset, format.Width, format.Height)
		if l == nil {
			stored, err := a.layoutStore.Get(req.Preset)
			if err != nil {
				httperr.Write(w, http.StatusNotFound, "preset not found: "+req.Preset)
				return
			}
			l = stored
		}
	} else if len(req.Slots) > 0 {
		l = &layout.Layout{Name: "custom", Slots: req.Slots}
	} else {
		httperr.Write(w, http.StatusBadRequest, "provide preset name or slots")
		return
	}

	if err := layout.ValidateLayout(l, format.Width, format.Height); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	a.layoutCompositor.SetLayout(l)
	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(l)
}

func (a *API) handleDeleteLayout(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	a.layoutCompositor.SetLayout(nil)
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleSlotOn(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid slot id")
		return
	}
	a.layoutCompositor.SlotOn(id)
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleSlotOff(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid slot id")
		return
	}
	a.layoutCompositor.SlotOff(id)
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleSlotUpdate(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid slot id")
		return
	}

	var req slotUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	a.layoutCompositor.UpdateSlot(id, func(slot *layout.Slot) {
		if req.SourceKey != "" {
			slot.SourceKey = req.SourceKey
		}
		if req.X != nil {
			curW := slot.Rect.Dx()
			slot.Rect.Min.X = layout.EvenAlign(*req.X)
			slot.Rect.Max.X = slot.Rect.Min.X + curW
		}
		if req.Y != nil {
			curH := slot.Rect.Dy()
			slot.Rect.Min.Y = layout.EvenAlign(*req.Y)
			slot.Rect.Max.Y = slot.Rect.Min.Y + curH
		}
		if req.Width != nil {
			slot.Rect.Max.X = slot.Rect.Min.X + layout.EvenAlign(*req.Width)
		}
		if req.Height != nil {
			slot.Rect.Max.Y = slot.Rect.Min.Y + layout.EvenAlign(*req.Height)
		}
		if req.Border != nil {
			slot.Border = *req.Border
		}
		if req.Transition != nil {
			slot.Transition = *req.Transition
		}
		if req.ScaleMode != nil {
			slot.ScaleMode = *req.ScaleMode
		}
		if req.CropAnchor != nil {
			slot.CropAnchor = *req.CropAnchor
		}
	})
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleSlotSource(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid slot id")
		return
	}
	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	a.layoutCompositor.UpdateSlot(id, func(slot *layout.Slot) {
		slot.SourceKey = req.Source
	})
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleListLayoutPresets(w http.ResponseWriter, r *http.Request) {
	names := a.layoutStore.List()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(names)
}

func (a *API) handleSaveLayoutPreset(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	l := a.layoutCompositor.GetLayout()
	if l == nil {
		httperr.Write(w, http.StatusBadRequest, "no active layout to save")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		httperr.Write(w, http.StatusBadRequest, "provide a name")
		return
	}
	saved := layout.Layout{
		Name:  req.Name,
		Slots: make([]layout.Slot, len(l.Slots)),
	}
	copy(saved.Slots, l.Slots)
	if err := a.layoutStore.Save(&saved); err != nil {
		httperr.Write(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (a *API) handleDeleteLayoutPreset(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	name := r.PathValue("name")
	if err := a.layoutStore.Delete(name); err != nil {
		httperr.Write(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
