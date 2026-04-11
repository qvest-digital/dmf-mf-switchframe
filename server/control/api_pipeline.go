package control

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/switcher"
)

// WithThumbnailCache attaches a pipeline thumbnail cache to the API.
func WithThumbnailCache(tc *switcher.ThumbnailCache) APIOption {
	return func(a *API) { a.thumbnailCache = tc }
}

// registerPipelineRoutes registers pipeline inspection/bypass routes on the given mux.
func (a *API) registerPipelineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pipeline/snapshot", a.handlePipelineSnapshot)
	mux.HandleFunc("PUT /api/pipeline/nodes/{name}/bypass", a.handlePipelineBypassSet)
	mux.HandleFunc("DELETE /api/pipeline/nodes/{name}/bypass", a.handlePipelineBypassClear)
	if a.thumbnailCache != nil {
		mux.HandleFunc("GET /api/pipeline/thumbnails", a.handlePipelineThumbnails)
	}
}

// pipelineNodeResponse is the JSON representation of a single pipeline node.
type pipelineNodeResponse struct {
	Name      string `json:"name"`
	Category  string `json:"category"`
	Active    bool   `json:"active"`
	Bypassed  bool   `json:"bypassed"`
	LastNs    int64  `json:"lastNs"`
	MaxNs     int64  `json:"maxNs"`
	LatencyUs int64  `json:"latencyUs"`
	Health    string `json:"health"`
	Error     string `json:"error,omitempty"`
}

// nodeCategory derives a UI category from the pipeline node name.
func nodeCategory(name string) string {
	switch {
	case strings.HasPrefix(name, "srt:") || strings.HasPrefix(name, "cam") ||
		strings.HasPrefix(name, "mxl:") || strings.HasPrefix(name, "clip"):
		return "source"
	case name == "h264-encode" || name == "gpu_encode" || name == "gpu_preview_encode":
		return "output"
	case name == "raw-sink-mxl" || name == "gpu_raw_sink":
		return "output"
	case name == "program_output":
		return "output"
	case strings.HasPrefix(name, "gpu_"):
		return "gpu"
	case name == "dve-compositor" || name == "compositor":
		return "gpu"
	default:
		return "decode"
	}
}

// pipelineSnapshotResponse is the JSON representation of the pipeline state.
type pipelineSnapshotResponse struct {
	Epoch          uint64                 `json:"epoch"`
	TotalLatencyUs int64                  `json:"totalLatencyUs"`
	BypassCount    int                    `json:"bypassCount"`
	Nodes          []pipelineNodeResponse `json:"nodes"`
	Connections    []connectionResponse   `json:"connections"`
}

// connectionResponse is the JSON representation of a pipeline connection.
type connectionResponse struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Active bool   `json:"active"`
}

// buildSnapshotResponse converts the combined pipeline snapshot into a typed response.
// The snapshot contains cpu_pipeline, gpu_pipeline, and sources sections.
func buildSnapshotResponse(raw map[string]any) pipelineSnapshotResponse {
	resp := pipelineSnapshotResponse{
		Nodes:       []pipelineNodeResponse{},
		Connections: []connectionResponse{},
	}

	if raw == nil {
		return resp
	}

	// ── Source nodes ──
	if srcMap, ok := raw["sources"].(map[string]map[string]any); ok {
		for key, info := range srcMap {
			health := "healthy"
			if h, ok := info["health"].(string); ok {
				switch h {
				case "stale":
					health = "degraded"
				case "no_signal", "offline":
					health = "error"
				}
			}
			resp.Nodes = append(resp.Nodes, pipelineNodeResponse{
				Name:     key,
				Category: "source",
				Active:   health != "error",
				Health:   health,
			})
		}
	}

	// ── GPU pipeline nodes (preferred — this is where the real processing happens) ──
	var gpuNodeNames []string
	if gpuPipeline, ok := raw["gpu_pipeline"].(map[string]any); ok {
		if v, ok := gpuPipeline["total_latency_us"].(int64); ok {
			resp.TotalLatencyUs = v
		}
		if nodes, ok := gpuPipeline["active_nodes"].([]map[string]any); ok {
			for _, n := range nodes {
				node := pipelineNodeResponse{
					Active: true,
					Health: "healthy",
				}
				if v, ok := n["name"].(string); ok {
					node.Name = v
					node.Category = nodeCategory(v)
				}
				if v, ok := n["bypassed"].(bool); ok {
					node.Bypassed = v
					if v {
						resp.BypassCount++
					}
				}
				if v, ok := n["last_ns"].(int64); ok {
					node.LastNs = v
				}
				if v, ok := n["max_ns"].(int64); ok {
					node.MaxNs = v
				}
				if v, ok := n["latency_us"].(int64); ok {
					node.LatencyUs = v
				}
				if errStr, ok := n["last_error"].(string); ok {
					node.Health = "error"
					node.Error = errStr
				}
				resp.Nodes = append(resp.Nodes, node)
				gpuNodeNames = append(gpuNodeNames, node.Name)
			}
		}
	}

	// ── CPU pipeline nodes (fallback when no GPU pipeline is active) ──
	if len(gpuNodeNames) == 0 {
		if cpuPipeline, ok := raw["cpu_pipeline"].(map[string]any); ok {
			if v, ok := cpuPipeline["epoch"].(uint64); ok {
				resp.Epoch = v
			}
			if v, ok := cpuPipeline["total_latency_us"].(int64); ok {
				resp.TotalLatencyUs = v
			}
			if nodes, ok := cpuPipeline["active_nodes"].([]map[string]any); ok {
				for _, n := range nodes {
					node := pipelineNodeResponse{
						Active: true,
						Health: "healthy",
					}
					if v, ok := n["name"].(string); ok {
						node.Name = v
						node.Category = nodeCategory(v)
					}
					if v, ok := n["bypassed"].(bool); ok {
						node.Bypassed = v
						if v {
							resp.BypassCount++
						}
					}
					if v, ok := n["last_ns"].(int64); ok {
						node.LastNs = v
					}
					if v, ok := n["max_ns"].(int64); ok {
						node.MaxNs = v
					}
					if v, ok := n["latency_us"].(int64); ok {
						node.LatencyUs = v
					}
					if _, ok := n["last_error"]; ok {
						node.Health = "error"
					}
					resp.Nodes = append(resp.Nodes, node)
				}
			}
		}
	}

	// ── Build topology connections ──

	// Collect source and processing nodes.
	var sourceNames []string
	var processingNodes []pipelineNodeResponse
	for _, n := range resp.Nodes {
		if n.Category == "source" {
			sourceNames = append(sourceNames, n.Name)
		} else {
			processingNodes = append(processingNodes, n)
		}
	}

	// Sources → first processing node.
	if len(processingNodes) > 0 {
		firstNode := processingNodes[0].Name
		for _, src := range sourceNames {
			resp.Connections = append(resp.Connections, connectionResponse{
				From:   src,
				To:     firstNode,
				Active: true,
			})
		}
		// Sequential chaining of processing nodes.
		for i := 0; i < len(processingNodes)-1; i++ {
			resp.Connections = append(resp.Connections, connectionResponse{
				From:   processingNodes[i].Name,
				To:     processingNodes[i+1].Name,
				Active: true,
			})
		}
		// Last processing node → program output.
		lastNode := processingNodes[len(processingNodes)-1].Name
		resp.Connections = append(resp.Connections, connectionResponse{
			From:   lastNode,
			To:     "output:program",
			Active: true,
		})
	}

	return resp
}

// handlePipelineSnapshot returns the current pipeline state as JSON.
func (a *API) handlePipelineSnapshot(w http.ResponseWriter, r *http.Request) {
	raw := a.switcher.PipelineSnapshot()
	resp := buildSnapshotResponse(raw)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handlePipelineBypassSet enables bypass on a pipeline node.
func (a *API) handlePipelineBypassSet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httperr.Write(w, http.StatusBadRequest, "node name required")
		return
	}
	if err := a.switcher.SetPipelineBypass(name, true); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	raw := a.switcher.PipelineSnapshot()
	resp := buildSnapshotResponse(raw)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handlePipelineBypassClear disables bypass on a pipeline node.
func (a *API) handlePipelineBypassClear(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httperr.Write(w, http.StatusBadRequest, "node name required")
		return
	}
	if err := a.switcher.SetPipelineBypass(name, false); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	raw := a.switcher.PipelineSnapshot()
	resp := buildSnapshotResponse(raw)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// thumbnailNodeResponse is a single node's thumbnail in the response.
type thumbnailNodeResponse struct {
	JPEG         string `json:"jpeg"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	CapturedAtMs int64  `json:"capturedAtMs"`
}

// handlePipelineThumbnails returns cached pipeline thumbnails as base64 JSON.
func (a *API) handlePipelineThumbnails(w http.ResponseWriter, r *http.Request) {
	a.thumbnailCache.RequestCapture()
	all := a.thumbnailCache.GetAll()

	nodes := make(map[string]thumbnailNodeResponse, len(all))
	for id, info := range all {
		nodes[id] = thumbnailNodeResponse{
			JPEG:         base64.StdEncoding.EncodeToString(info.JPEG),
			Width:        info.Width,
			Height:       info.Height,
			CapturedAtMs: info.CapturedAt.UnixMilli(),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"nodes": nodes,
	})
}
