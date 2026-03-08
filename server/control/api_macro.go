package control

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/switcher"
)

// handleListMacros returns all macros.
func (a *API) handleListMacros(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.macroStore.List())
}

// handleGetMacro returns a single macro by name.
func (a *API) handleGetMacro(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	m, err := a.macroStore.Get(name)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

// handleSaveMacro creates or updates a macro.
func (a *API) handleSaveMacro(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var m macro.Macro
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	m.Name = name

	if err := a.macroStore.Save(m); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

// handleDeleteMacro deletes a macro by name.
func (a *API) handleDeleteMacro(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := a.macroStore.Delete(name); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunMacro triggers execution of a macro.
func (a *API) handleRunMacro(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	m, err := a.macroStore.Get(name)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	target := &apiMacroTarget{
		switcher: a.switcher,
		mixer:    a.mixer,
	}

	if err := macro.Run(r.Context(), m, target); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// apiMacroTarget adapts the API's switcher and mixer to the macro.MacroTarget
// interface so Run() can execute macro steps without knowing concrete types.
type apiMacroTarget struct {
	switcher *switcher.Switcher
	mixer    AudioMixerAPI
}

func (t *apiMacroTarget) Cut(ctx context.Context, source string) error {
	return t.switcher.Cut(ctx, source)
}

func (t *apiMacroTarget) SetPreview(ctx context.Context, source string) error {
	return t.switcher.SetPreview(ctx, source)
}

func (t *apiMacroTarget) StartTransition(ctx context.Context, source string, transType string, durationMs int) error {
	return t.switcher.StartTransition(ctx, source, transType, durationMs, "")
}

func (t *apiMacroTarget) SetLevel(ctx context.Context, source string, level float64) error {
	if t.mixer == nil {
		return nil
	}
	return t.mixer.SetLevel(source, level)
}
