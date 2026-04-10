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
}

// nodeCategory derives a UI category from the pipeline node name.
func nodeCategory(name string) string {
	switch {
	case strings.HasPrefix(name, "gpu_"):
		return "gpu"
	case strings.HasPrefix(name, "srt:") || strings.HasPrefix(name, "cam") || strings.HasPrefix(name, "mxl:") || strings.HasPrefix(name, "clip"):
		return "source"
	case name == "program_output":
		return "output"
	case name == "h264-encode" || name == "gpu_encode":
		return "output"
	case name == "raw-sink-mxl" || name == "gpu_raw_sink":
		return "output"
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

// buildSnapshotResponse converts the raw pipeline snapshot map into a typed response.
func buildSnapshotResponse(raw map[string]any, sourceKeys []string) pipelineSnapshotResponse {
	resp := pipelineSnapshotResponse{
		Nodes:       []pipelineNodeResponse{},
		Connections: []connectionResponse{},
	}

	if raw == nil {
		return resp
	}

	if v, ok := raw["epoch"]; ok {
		if epoch, ok := v.(uint64); ok {
			resp.Epoch = epoch
		}
	}
	if v, ok := raw["total_latency_us"]; ok {
		if lat, ok := v.(int64); ok {
			resp.TotalLatencyUs = lat
		}
	}

	activeNodes, _ := raw["active_nodes"].([]map[string]any)
	for _, n := range activeNodes {
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

	// Build static topology connections:
	// sources -> first node, sequential chaining, last node -> outputs
	if len(resp.Nodes) > 0 {
		firstNode := resp.Nodes[0].Name
		for _, src := range sourceKeys {
			resp.Connections = append(resp.Connections, connectionResponse{
				From:   "source:" + src,
				To:     firstNode,
				Active: true,
			})
		}
		for i := 0; i < len(resp.Nodes)-1; i++ {
			resp.Connections = append(resp.Connections, connectionResponse{
				From:   resp.Nodes[i].Name,
				To:     resp.Nodes[i+1].Name,
				Active: true,
			})
		}
		lastNode := resp.Nodes[len(resp.Nodes)-1].Name
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
	resp := buildSnapshotResponse(raw, a.switcher.SourceKeys())
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
	resp := buildSnapshotResponse(raw, a.switcher.SourceKeys())
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
	resp := buildSnapshotResponse(raw, a.switcher.SourceKeys())
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
