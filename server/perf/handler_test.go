package perf

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlePerf_ReturnsJSON(t *testing.T) {
	sampler := NewSampler(
		&mockSwitcherPerf{sample: SwitcherSample{
			Sources:       map[string]SourceSample{},
			NodeTimings:   map[string]int64{},
			FrameBudgetNs: 33000000,
		}},
		&mockMixerPerf{},
		&mockOutputPerf{},
	)
	sampler.tick() // populate with one sample

	req := httptest.NewRequest("GET", "/api/perf", nil)
	w := httptest.NewRecorder()
	sampler.HandlePerf(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var snap PerfSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("JSON decode: %v", err)
	}
	if snap.FrameBudgetNs != 33000000 {
		t.Errorf("FrameBudgetNs = %d, want 33000000", snap.FrameBudgetNs)
	}
}

func TestHandlePerf_WithBaseline_IncludesDiff(t *testing.T) {
	sampler := NewSampler(
		&mockSwitcherPerf{sample: SwitcherSample{
			Sources:        map[string]SourceSample{},
			NodeTimings:    map[string]int64{},
			FrameBudgetNs:  33000000,
			PipelineLastNs: 10000,
		}},
		&mockMixerPerf{},
		&mockOutputPerf{},
	)
	sampler.tick()
	sampler.SaveBaseline("test")
	sampler.tick()

	req := httptest.NewRequest("GET", "/api/perf?baseline=test", nil)
	w := httptest.NewRecorder()
	sampler.HandlePerf(w, req)

	var snap PerfSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("JSON decode: %v", err)
	}
	if snap.Baseline == nil {
		t.Fatal("expected baseline diff to be non-nil")
	}
	if snap.Baseline.Name != "test" {
		t.Errorf("baseline name = %q, want %q", snap.Baseline.Name, "test")
	}
}

func TestHandlePerf_UnknownBaseline_NoDiff(t *testing.T) {
	sampler := NewSampler(
		&mockSwitcherPerf{sample: SwitcherSample{
			Sources:     map[string]SourceSample{},
			NodeTimings: map[string]int64{},
		}},
		&mockMixerPerf{},
		&mockOutputPerf{},
	)
	sampler.tick()

	req := httptest.NewRequest("GET", "/api/perf?baseline=nonexistent", nil)
	w := httptest.NewRecorder()
	sampler.HandlePerf(w, req)

	var snap PerfSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("JSON decode: %v", err)
	}
	if snap.Baseline != nil {
		t.Fatal("expected baseline to be nil for unknown name")
	}
}

func TestHandleSaveBaseline_RequiresName(t *testing.T) {
	sampler := NewSampler(
		&mockSwitcherPerf{sample: SwitcherSample{
			Sources:     map[string]SourceSample{},
			NodeTimings: map[string]int64{},
		}},
		&mockMixerPerf{},
		&mockOutputPerf{},
	)

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest("POST", "/api/perf/baseline", body)
	w := httptest.NewRecorder()
	sampler.HandleSaveBaseline(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleSaveBaseline_Success(t *testing.T) {
	sampler := NewSampler(
		&mockSwitcherPerf{sample: SwitcherSample{
			Sources:     map[string]SourceSample{},
			NodeTimings: map[string]int64{},
		}},
		&mockMixerPerf{},
		&mockOutputPerf{},
	)
	sampler.tick()

	body := bytes.NewBufferString(`{"name":"baseline1"}`)
	req := httptest.NewRequest("POST", "/api/perf/baseline", body)
	w := httptest.NewRecorder()
	sampler.HandleSaveBaseline(w, req)

	if w.Code != 204 {
		t.Errorf("status = %d, want 204", w.Code)
	}

	// Verify baseline exists
	sampler.mu.RLock()
	_, ok := sampler.baselines["baseline1"]
	sampler.mu.RUnlock()
	if !ok {
		t.Error("expected baseline 'baseline1' to exist")
	}
}

func TestHandleDeleteBaseline(t *testing.T) {
	sampler := NewSampler(
		&mockSwitcherPerf{sample: SwitcherSample{
			Sources:     map[string]SourceSample{},
			NodeTimings: map[string]int64{},
		}},
		&mockMixerPerf{},
		&mockOutputPerf{},
	)
	sampler.tick()
	sampler.SaveBaseline("todelete")

	// Use a mux to handle path parameters
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/perf/baseline/{name}", sampler.HandleDeleteBaseline)

	req := httptest.NewRequest("DELETE", "/api/perf/baseline/todelete", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 204 {
		t.Errorf("status = %d, want 204", w.Code)
	}

	sampler.mu.RLock()
	_, ok := sampler.baselines["todelete"]
	sampler.mu.RUnlock()
	if ok {
		t.Error("expected baseline 'todelete' to be deleted")
	}
}
