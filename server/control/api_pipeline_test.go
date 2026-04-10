package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// setupPipelineTestAPI creates an API with a test switcher that has a built
// pipeline (using mock codecs) and a thumbnail cache.
func setupPipelineTestAPI(t *testing.T) (*API, *switcher.ThumbnailCache) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("cam1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("cam2", r2)

	// Install mock codecs and build the pipeline so nodes are available.
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	if err := sw.BuildPipeline(); err != nil {
		t.Fatalf("BuildPipeline: %v", err)
	}

	tc := switcher.NewThumbnailCache()
	api := NewAPI(sw, WithThumbnailCache(tc))
	return api, tc
}

func TestPipelineSnapshotEndpoint(t *testing.T) {
	api, _ := setupPipelineTestAPI(t)

	req := httptest.NewRequest("GET", "/api/pipeline/snapshot", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	// Must have top-level keys
	for _, key := range []string{"epoch", "totalLatencyUs", "nodes"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("response missing key %q", key)
		}
	}

	// nodes should be an array
	nodes, ok := resp["nodes"].([]any)
	if !ok {
		t.Fatalf("nodes should be an array, got %T", resp["nodes"])
	}

	// With a built pipeline, the encode node should be present
	if len(nodes) == 0 {
		t.Fatal("expected at least one pipeline node")
	}

	// Check node structure
	node0 := nodes[0].(map[string]any)
	for _, key := range []string{"name", "active", "bypassed", "lastNs", "maxNs", "latencyUs", "health"} {
		if _, ok := node0[key]; !ok {
			t.Errorf("node missing key %q", key)
		}
	}

	// connections should exist
	conns, ok := resp["connections"].([]any)
	if !ok {
		t.Fatalf("connections should be an array, got %T", resp["connections"])
	}
	if len(conns) == 0 {
		t.Fatal("expected at least one connection")
	}
}

func TestPipelineBypassEndpoint(t *testing.T) {
	api, _ := setupPipelineTestAPI(t)

	// Get the snapshot to find a bypassable node (not gpu_encode)
	req := httptest.NewRequest("GET", "/api/pipeline/snapshot", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	var snap map[string]any
	_ = json.NewDecoder(w.Body).Decode(&snap)

	nodes, _ := snap["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatal("no pipeline nodes available")
	}

	// Find a non-encode node to bypass
	var nodeName string
	for _, n := range nodes {
		nm := n.(map[string]any)
		name := nm["name"].(string)
		if name != "gpu_encode" {
			nodeName = name
			break
		}
	}
	if nodeName == "" {
		t.Skip("no bypassable node found")
	}

	// PUT bypass
	req = httptest.NewRequest("PUT", "/api/pipeline/nodes/"+nodeName+"/bypass", nil)
	w = httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for bypass PUT, got %d: %s", w.Code, w.Body.String())
	}

	var putResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&putResp)

	// Find the node in response and verify bypassed=true
	respNodes, _ := putResp["nodes"].([]any)
	found := false
	for _, n := range respNodes {
		nm := n.(map[string]any)
		if nm["name"] == nodeName {
			if nm["bypassed"] != true {
				t.Errorf("expected bypassed=true for %s", nodeName)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("node %s not found in response", nodeName)
	}

	// bypassCount should be >= 1
	if putResp["bypassCount"].(float64) < 1 {
		t.Errorf("expected bypassCount >= 1, got %v", putResp["bypassCount"])
	}
}

func TestPipelineBypassClearEndpoint(t *testing.T) {
	api, _ := setupPipelineTestAPI(t)

	// Get snapshot to find a bypassable node
	req := httptest.NewRequest("GET", "/api/pipeline/snapshot", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	var snap map[string]any
	_ = json.NewDecoder(w.Body).Decode(&snap)

	nodes, _ := snap["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatal("no pipeline nodes available")
	}

	var nodeName string
	for _, n := range nodes {
		nm := n.(map[string]any)
		name := nm["name"].(string)
		if name != "gpu_encode" {
			nodeName = name
			break
		}
	}
	if nodeName == "" {
		t.Skip("no bypassable node found")
	}

	// First bypass it
	if err := api.switcher.SetPipelineBypass(nodeName, true); err != nil {
		t.Fatalf("SetPipelineBypass: %v", err)
	}

	// DELETE bypass
	req = httptest.NewRequest("DELETE", "/api/pipeline/nodes/"+nodeName+"/bypass", nil)
	w = httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for bypass DELETE, got %d: %s", w.Code, w.Body.String())
	}

	var delResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&delResp)

	respNodes, _ := delResp["nodes"].([]any)
	for _, n := range respNodes {
		nm := n.(map[string]any)
		if nm["name"] == nodeName {
			if nm["bypassed"] != false {
				t.Errorf("expected bypassed=false after DELETE for %s", nodeName)
			}
		}
	}

	// bypassCount should be 0
	if delResp["bypassCount"].(float64) != 0 {
		t.Errorf("expected bypassCount=0 after clear, got %v", delResp["bypassCount"])
	}
}

func TestPipelineBypassProtectedNode(t *testing.T) {
	api, _ := setupPipelineTestAPI(t)

	// gpu_encode is protected — PUT bypass should return 400
	req := httptest.NewRequest("PUT", "/api/pipeline/nodes/gpu_encode/bypass", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for protected node, got %d: %s", w.Code, w.Body.String())
	}

	var errResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if errResp["error"] == nil || errResp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestPipelineThumbnailsEndpoint(t *testing.T) {
	api, tc := setupPipelineTestAPI(t)

	// Store a test thumbnail
	tc.Store("cam1", []byte{0xFF, 0xD8, 0xFF}, 160, 90)

	req := httptest.NewRequest("GET", "/api/pipeline/thumbnails", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	nodesMap, ok := resp["nodes"].(map[string]any)
	if !ok {
		t.Fatalf("expected nodes to be a map, got %T", resp["nodes"])
	}

	cam1, ok := nodesMap["cam1"]
	if !ok {
		t.Fatal("expected cam1 in thumbnails")
	}

	cam1Map := cam1.(map[string]any)
	if cam1Map["width"] != float64(160) {
		t.Errorf("expected width=160, got %v", cam1Map["width"])
	}
	if cam1Map["height"] != float64(90) {
		t.Errorf("expected height=90, got %v", cam1Map["height"])
	}
	if cam1Map["jpeg"] == nil || cam1Map["jpeg"] == "" {
		t.Error("expected non-empty jpeg base64 data")
	}
	if cam1Map["capturedAtMs"] == nil {
		t.Error("expected capturedAtMs field")
	}
}

func TestPipelineThumbnailsEndpoint_Empty(t *testing.T) {
	api, _ := setupPipelineTestAPI(t)

	// No thumbnails stored — should return empty nodes map
	req := httptest.NewRequest("GET", "/api/pipeline/thumbnails", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)

	nodesMap, ok := resp["nodes"].(map[string]any)
	if !ok {
		t.Fatalf("expected nodes to be a map, got %T", resp["nodes"])
	}
	if len(nodesMap) != 0 {
		t.Errorf("expected empty nodes map, got %d entries", len(nodesMap))
	}
}

func TestPipelineBypassUnknownNode(t *testing.T) {
	api, _ := setupPipelineTestAPI(t)

	// Unknown node should return 400
	req := httptest.NewRequest("PUT", "/api/pipeline/nodes/nonexistent/bypass", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown node, got %d: %s", w.Code, w.Body.String())
	}
}
