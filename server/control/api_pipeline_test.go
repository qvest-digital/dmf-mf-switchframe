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
	sw := switcher.NewTestSwitcher(t, programRelay)
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

	// With a built pipeline + 2 sources, should have at least 3 nodes
	if len(nodes) < 3 {
		t.Fatalf("expected at least 3 nodes (2 sources + 1 pipeline), got %d", len(nodes))
	}

	// Check node structure: every node must have these fields
	node0 := nodes[0].(map[string]any)
	for _, key := range []string{"name", "category", "active", "bypassed", "lastNs", "maxNs", "latencyUs", "health"} {
		if _, ok := node0[key]; !ok {
			t.Errorf("node missing key %q", key)
		}
	}

	// Verify sources are included
	foundSource := false
	for _, n := range nodes {
		nm := n.(map[string]any)
		if nm["category"] == "source" {
			foundSource = true
			break
		}
	}
	if !foundSource {
		t.Error("expected at least one source node in response")
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

	// Get the snapshot to find a bypassable pipeline node (not gpu_encode, not source)
	req := httptest.NewRequest("GET", "/api/pipeline/snapshot", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	var snap map[string]any
	_ = json.NewDecoder(w.Body).Decode(&snap)

	nodes, _ := snap["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatal("no pipeline nodes available")
	}

	// Find a pipeline (non-source, non-encode, non-protected) node to bypass
	var nodeName string
	for _, n := range nodes {
		nm := n.(map[string]any)
		name := nm["name"].(string)
		cat := nm["category"].(string)
		if cat == "source" || name == "gpu_encode" || name == "gpu_raw_sink" {
			continue
		}
		nodeName = name
		break
	}
	if nodeName == "" {
		t.Skip("no bypassable pipeline node found")
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

	// Get snapshot to find a bypassable pipeline node (not source, not protected)
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
		cat := nm["category"].(string)
		if cat == "source" || name == "gpu_encode" || name == "gpu_raw_sink" {
			continue
		}
		nodeName = name
		break
	}
	if nodeName == "" {
		t.Skip("no bypassable pipeline node found")
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

func TestBuildSnapshotResponse_GPUPipeline(t *testing.T) {
	// Test buildSnapshotResponse with a combined snapshot containing
	// GPU pipeline nodes and sources.
	raw := map[string]any{
		"sources": map[string]map[string]any{
			"cam1":       {"health": "healthy"},
			"cam2":       {"health": "healthy"},
			"srt:sintel": {"health": "stale"},
			"mxl:raw1":   {"health": "offline"},
		},
		"gpu_pipeline": map[string]any{
			"gpu":              true,
			"backend":          "metal",
			"total_latency_us": int64(500),
			"active_nodes": []map[string]any{
				{"name": "gpu_key", "last_ns": int64(80000), "max_ns": int64(150000), "latency_us": int64(80), "bypassed": true},
				{"name": "gpu_layout", "last_ns": int64(95000), "max_ns": int64(180000), "latency_us": int64(95), "bypassed": false},
				{"name": "gpu_dsk", "last_ns": int64(60000), "max_ns": int64(120000), "latency_us": int64(60), "bypassed": false},
				{"name": "gpu_encode", "last_ns": int64(200000), "max_ns": int64(500000), "latency_us": int64(200), "bypassed": false},
			},
		},
		// CPU pipeline also present but should be ignored when GPU is active.
		"cpu_pipeline": map[string]any{
			"epoch":            uint64(1),
			"total_latency_us": int64(100),
			"active_nodes": []map[string]any{
				{"name": "h264-encode", "last_ns": int64(0), "max_ns": int64(0), "latency_us": int64(100)},
			},
		},
	}

	resp := buildSnapshotResponse(raw)

	// Should have 4 sources + 4 GPU nodes = 8 nodes total.
	if len(resp.Nodes) != 8 {
		t.Fatalf("expected 8 nodes, got %d", len(resp.Nodes))
	}

	// Verify source nodes.
	sourceCount := 0
	for _, n := range resp.Nodes {
		if n.Category == "source" {
			sourceCount++
			switch n.Name {
			case "cam1", "cam2":
				if n.Health != "healthy" {
					t.Errorf("expected healthy for %s, got %s", n.Name, n.Health)
				}
			case "srt:sintel":
				if n.Health != "degraded" {
					t.Errorf("expected degraded for stale source, got %s", n.Health)
				}
			case "mxl:raw1":
				if n.Health != "error" {
					t.Errorf("expected error for offline source, got %s", n.Health)
				}
				if n.Active {
					t.Error("expected offline source to be inactive")
				}
			}
		}
	}
	if sourceCount != 4 {
		t.Errorf("expected 4 source nodes, got %d", sourceCount)
	}

	// Verify GPU nodes (not CPU nodes).
	gpuCount := 0
	for _, n := range resp.Nodes {
		if n.Category == "gpu" || n.Category == "output" {
			gpuCount++
		}
	}
	if gpuCount != 4 {
		t.Errorf("expected 4 GPU pipeline nodes, got %d", gpuCount)
	}

	// GPU total latency should be used (not CPU).
	if resp.TotalLatencyUs != 500 {
		t.Errorf("expected total latency 500, got %d", resp.TotalLatencyUs)
	}

	// Bypass count should reflect GPU pipeline bypass state (gpu_key is bypassed).
	if resp.BypassCount != 1 {
		t.Errorf("expected bypassCount 1 (gpu_key bypassed), got %d", resp.BypassCount)
	}

	// Connections should exist: 4 sources → gpu_key, plus 3 node-to-node, plus 1 to output.
	if len(resp.Connections) == 0 {
		t.Fatal("expected connections")
	}
}

func TestBuildSnapshotResponse_CPUFallback(t *testing.T) {
	// When no GPU pipeline is present, CPU pipeline nodes should be used.
	raw := map[string]any{
		"sources": map[string]map[string]any{
			"cam1": {"health": "healthy"},
		},
		"cpu_pipeline": map[string]any{
			"epoch":            uint64(5),
			"total_latency_us": int64(100),
			"active_nodes": []map[string]any{
				{"name": "dve-compositor", "last_ns": int64(50000), "max_ns": int64(100000), "latency_us": int64(50), "bypassed": false},
				{"name": "h264-encode", "last_ns": int64(100000), "max_ns": int64(200000), "latency_us": int64(100), "bypassed": false},
			},
		},
	}

	resp := buildSnapshotResponse(raw)

	// 1 source + 2 CPU pipeline nodes = 3.
	if len(resp.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(resp.Nodes))
	}

	// Epoch should come from CPU pipeline.
	if resp.Epoch != 5 {
		t.Errorf("expected epoch 5, got %d", resp.Epoch)
	}

	if resp.TotalLatencyUs != 100 {
		t.Errorf("expected total latency 100, got %d", resp.TotalLatencyUs)
	}
}

func TestBuildSnapshotResponse_NilSnapshot(t *testing.T) {
	resp := buildSnapshotResponse(nil)
	if len(resp.Nodes) != 0 {
		t.Errorf("expected 0 nodes for nil input, got %d", len(resp.Nodes))
	}
	if len(resp.Connections) != 0 {
		t.Errorf("expected 0 connections for nil input, got %d", len(resp.Connections))
	}
}

func TestNodeCategory(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"cam1", "source"},
		{"cam2", "source"},
		{"srt:sintel", "source"},
		{"mxl:raw1", "source"},
		{"clip1", "source"},
		{"gpu_key", "gpu"},
		{"gpu_layout", "gpu"},
		{"gpu_dsk", "gpu"},
		{"gpu_colorgrade", "gpu"},
		{"gpu_stmap", "gpu"},
		{"gpu_encode", "output"},
		{"gpu_preview_encode", "output"},
		{"gpu_raw_sink", "output"},
		{"h264-encode", "output"},
		{"raw-sink-mxl", "output"},
		{"program_output", "output"},
		{"dve-compositor", "gpu"},
		{"compositor", "gpu"},
	}

	for _, tt := range tests {
		got := nodeCategory(tt.name)
		if got != tt.expected {
			t.Errorf("nodeCategory(%q) = %q, want %q", tt.name, got, tt.expected)
		}
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
