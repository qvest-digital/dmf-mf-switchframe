package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/playout"
)

func (a *API) registerPlayoutPodRoutes(mux *http.ServeMux) {
	if a.playoutMgr == nil {
		return
	}
	mux.HandleFunc("GET /api/playout/channels/{n}/pods", a.handlePodList)
	mux.HandleFunc("POST /api/playout/channels/{n}/pods", a.handlePodCreate)
	mux.HandleFunc("GET /api/playout/channels/{n}/pods/{podId}", a.handlePodGet)
	mux.HandleFunc("DELETE /api/playout/channels/{n}/pods/{podId}", a.handlePodDelete)
	mux.HandleFunc("POST /api/playout/channels/{n}/pods/{podId}/arm", a.handlePodArm)
	mux.HandleFunc("POST /api/playout/channels/{n}/pods/{podId}/disarm", a.handlePodDisarm)
	mux.HandleFunc("POST /api/playout/channels/{n}/pods/{podId}/move", a.handlePodMove)
	mux.HandleFunc("POST /api/playout/channels/{n}/pods/{podId}/swap", a.handlePodSwap)
	mux.HandleFunc("POST /api/playout/channels/{n}/pods/{podId}/resize", a.handlePodResize)
	mux.HandleFunc("POST /api/playout/channels/{n}/pods/{podId}/fire", a.handlePodFire)
	mux.HandleFunc("POST /api/playout/channels/{n}/pods/{podId}/defer", a.handlePodDefer)
	mux.HandleFunc("POST /api/playout/channels/{n}/pods/{podId}/dump", a.handlePodDump)
	mux.HandleFunc("POST /api/playout/channels/{n}/pods/{podId}/hold", a.handlePodHold)
	mux.HandleFunc("POST /api/playout/channels/{n}/pods/{podId}/extend", a.handlePodExtend)

	mux.HandleFunc("GET /api/playout/pods/templates", a.handlePodTemplateList)
	mux.HandleFunc("POST /api/playout/pods/templates", a.handlePodTemplateCreate)
	mux.HandleFunc("PUT /api/playout/pods/templates/{type}", a.handlePodTemplateUpdate)
	mux.HandleFunc("DELETE /api/playout/pods/templates/{type}", a.handlePodTemplateDelete)
}

func (a *API) getPodBoard(w http.ResponseWriter, r *http.Request) (*playout.PodBoard, bool) {
	n, err := parseChannelID(r)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return nil, false
	}
	pb, err := a.playoutMgr.PodBoard(n)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return nil, false
	}
	return pb, true
}

func (a *API) handlePodList(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pb.ListPods())
}

func (a *API) handlePodCreate(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}

	var req struct {
		Template string             `json:"template"`
		Config   *playout.PodConfig `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	var pod *playout.Pod
	var err error
	if req.Template != "" {
		pod, err = pb.CreateFromTemplate(req.Template)
	} else if req.Config != nil {
		pod, err = pb.CreatePod(*req.Config)
	} else {
		httperr.Write(w, http.StatusBadRequest, "template or config required")
		return
	}
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(pod)
}

func (a *API) handlePodGet(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	pod, err := pb.GetPod(r.PathValue("podId"))
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pod)
}

func (a *API) handlePodDelete(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	if err := pb.DeletePod(r.PathValue("podId")); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePodArm(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	if err := pb.Arm(r.PathValue("podId")); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePodDisarm(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	if err := pb.Disarm(r.PathValue("podId")); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePodMove(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	var req struct {
		Position string `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := pb.Move(r.PathValue("podId"), req.Position); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePodSwap(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	var req struct {
		OtherPodID string `json:"otherPodId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := pb.Swap(r.PathValue("podId"), req.OtherPodID); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePodResize(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	var req struct {
		DurationMs int64 `json:"duration_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := pb.Resize(r.PathValue("podId"), req.DurationMs); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePodFire(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	if err := pb.Fire(r.PathValue("podId")); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePodDefer(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	var req struct {
		Position string `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := pb.Defer(r.PathValue("podId"), req.Position); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePodDump(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	if err := pb.DumpOut(r.PathValue("podId")); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePodHold(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	if err := pb.Hold(r.PathValue("podId")); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePodExtend(w http.ResponseWriter, r *http.Request) {
	pb, ok := a.getPodBoard(w, r)
	if !ok {
		return
	}
	var req struct {
		AdditionalMs int64 `json:"additional_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := pb.Extend(r.PathValue("podId"), req.AdditionalMs); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePodTemplateList(w http.ResponseWriter, _ *http.Request) {
	store := a.playoutMgr.TemplateStore()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(store.List())
}

func (a *API) handlePodTemplateCreate(w http.ResponseWriter, r *http.Request) {
	store := a.playoutMgr.TemplateStore()
	var tmpl playout.PodTemplate
	if err := json.NewDecoder(r.Body).Decode(&tmpl); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := store.Put(tmpl); err != nil {
		httperr.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(tmpl)
}

func (a *API) handlePodTemplateUpdate(w http.ResponseWriter, r *http.Request) {
	store := a.playoutMgr.TemplateStore()
	var tmpl playout.PodTemplate
	if err := json.NewDecoder(r.Body).Decode(&tmpl); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	tmpl.Type = r.PathValue("type")
	if err := store.Put(tmpl); err != nil {
		httperr.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tmpl)
}

func (a *API) handlePodTemplateDelete(w http.ResponseWriter, r *http.Request) {
	store := a.playoutMgr.TemplateStore()
	if err := store.Delete(r.PathValue("type")); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
