package perf

import (
	"encoding/json"
	"net/http"
)

// HandlePerf serves GET /api/perf with optional ?baseline=name query param.
func (s *Sampler) HandlePerf(w http.ResponseWriter, r *http.Request) {
	baseline := r.URL.Query().Get("baseline")
	snap := s.Snapshot(baseline)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snap)
}

// HandleSaveBaseline serves POST /api/perf/baseline.
func (s *Sampler) HandleSaveBaseline(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "name required"})
		return
	}
	s.SaveBaseline(req.Name)
	w.WriteHeader(204)
}

// HandleDeleteBaseline serves DELETE /api/perf/baseline/{name}.
func (s *Sampler) HandleDeleteBaseline(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "name required"})
		return
	}
	s.DeleteBaseline(name)
	w.WriteHeader(204)
}
