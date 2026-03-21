package control

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/stmap"
)

// generateRequest is the JSON body for the ST map generate endpoint.
type generateRequest struct {
	Type          string             `json:"type"`
	Params        map[string]float64 `json:"params"`
	Name          string             `json:"name"`
	Width         int                `json:"width"`
	Height        int                `json:"height"`
	AssignSource  string             `json:"assign_source"`
	AssignProgram bool               `json:"assign_program"`
	FrameCount    int                `json:"frame_count"`
}

// stmapAssignRequest is the JSON body for source/program map assignment.
type stmapAssignRequest struct {
	Map string `json:"map"`
}

// registerSTMapRoutes registers ST map API routes on the given mux.
func (a *API) registerSTMapRoutes(mux *http.ServeMux) {
	if a.stmapRegistry == nil {
		return
	}
	mux.HandleFunc("GET /api/stmap", a.handleSTMapList)
	mux.HandleFunc("GET /api/stmap/state", a.handleSTMapState)
	mux.HandleFunc("GET /api/stmap/generators", a.handleSTMapGenerators)
	mux.HandleFunc("POST /api/stmap/generate", a.handleSTMapGenerate)
	mux.HandleFunc("POST /api/stmap/upload/{name}", a.handleSTMapUpload)
	mux.HandleFunc("GET /api/stmap/{name}", a.handleSTMapGet)
	mux.HandleFunc("DELETE /api/stmap/{name}", a.handleSTMapDelete)
	mux.HandleFunc("GET /api/stmap/{name}/download", a.handleSTMapDownload)
	mux.HandleFunc("PUT /api/stmap/source/{sourceKey}", a.handleSTMapAssignSource)
	mux.HandleFunc("DELETE /api/stmap/source/{sourceKey}", a.handleSTMapRemoveSource)
	mux.HandleFunc("PUT /api/stmap/program", a.handleSTMapAssignProgram)
	mux.HandleFunc("DELETE /api/stmap/program", a.handleSTMapRemoveProgram)
}

// handleSTMapList returns all stored map names.
func (a *API) handleSTMapList(w http.ResponseWriter, r *http.Request) {
	names := a.stmapRegistry.List()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"maps": names})
}

// handleSTMapState returns the current ST map assignments.
func (a *API) handleSTMapState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.stmapRegistry.State())
}

// handleSTMapGenerators returns all available generators with parameter schemas.
func (a *API) handleSTMapGenerators(w http.ResponseWriter, r *http.Request) {
	infos := stmap.GeneratorInfoList()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"generators": infos})
}

// handleSTMapGenerate generates a new ST map and stores it.
func (a *API) handleSTMapGenerate(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	var req generateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Type == "" {
		httperr.Write(w, http.StatusBadRequest, "type is required")
		return
	}
	if req.Name == "" {
		httperr.Write(w, http.StatusBadRequest, "name is required")
		return
	}

	// Default dimensions from pipeline format.
	if req.Width == 0 || req.Height == 0 {
		pf := a.switcher.PipelineFormat()
		req.Width = pf.Width
		req.Height = pf.Height
	}

	// Try static generator first, then animated.
	staticGens := stmap.ListGenerators()
	isStatic := false
	for _, name := range staticGens {
		if name == req.Type {
			isStatic = true
			break
		}
	}

	var respMap map[string]interface{}

	if isStatic {
		m, err := stmap.Generate(req.Type, req.Params, req.Width, req.Height)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, err.Error())
			return
		}
		m.Name = req.Name
		if err := a.stmapRegistry.Store(m); err != nil {
			httperr.WriteErr(w, errorStatus(err), err)
			return
		}
		respMap = map[string]interface{}{
			"name":   m.Name,
			"width":  m.Width,
			"height": m.Height,
			"type":   "static",
		}
	} else {
		// Try animated
		frameCount := req.FrameCount
		if frameCount <= 0 {
			frameCount = 90
		}
		am, err := stmap.GenerateAnimated(req.Type, req.Params, req.Width, req.Height, frameCount)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, err.Error())
			return
		}
		am.Name = req.Name
		if err := a.stmapRegistry.StoreAnimated(am); err != nil {
			httperr.WriteErr(w, errorStatus(err), err)
			return
		}
		respMap = map[string]interface{}{
			"name":        am.Name,
			"width":       req.Width,
			"height":      req.Height,
			"type":        "animated",
			"frame_count": len(am.Frames),
		}
	}

	// Optional assignment.
	if req.AssignSource != "" {
		if err := a.stmapRegistry.AssignSource(req.AssignSource, req.Name); err != nil {
			httperr.WriteErr(w, errorStatus(err), err)
			return
		}
	}
	if req.AssignProgram {
		if err := a.stmapRegistry.AssignProgram(req.Name); err != nil {
			httperr.WriteErr(w, errorStatus(err), err)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(respMap)
}

// handleSTMapUpload accepts an uploaded ST map file (PNG, EXR, or raw binary).
func (a *API) handleSTMapUpload(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	name := r.PathValue("name")
	if err := stmap.ValidateName(name); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	// Detect format from Content-Type or extension.
	ct := r.Header.Get("Content-Type")
	ext := strings.ToLower(filepath.Ext(name))

	r.Body = http.MaxBytesReader(w, r.Body, 64<<20) // 64MB limit
	data, err := io.ReadAll(r.Body)
	if err != nil {
		httperr.Write(w, http.StatusRequestEntityTooLarge, "upload too large (max 64MB)")
		return
	}

	var m *stmap.STMap

	switch {
	case ct == "image/x-exr" || ext == ".exr":
		m, err = stmap.ReadEXR(data, name)
	case ct == "image/png" || ext == ".png":
		m, err = stmap.ReadPNG(data, name)
	case ext == ".stmap":
		m, err = stmap.ReadRaw(data, name)
	default:
		// Auto-detect by magic bytes, then try PNG, then raw.
		if stmap.IsEXR(data) {
			m, err = stmap.ReadEXR(data, name)
		} else {
			m, err = stmap.ReadPNG(data, name)
			if err != nil {
				m, err = stmap.ReadRaw(data, name)
			}
		}
	}

	if err != nil {
		httperr.Write(w, http.StatusBadRequest, fmt.Sprintf("failed to parse upload: %v", err))
		return
	}

	if err := a.stmapRegistry.Store(m); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"name":   m.Name,
		"width":  m.Width,
		"height": m.Height,
		"type":   "static",
	})
}

// handleSTMapGet returns metadata about a stored map.
func (a *API) handleSTMapGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if m, ok := a.stmapRegistry.Get(name); ok {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name":   m.Name,
			"width":  m.Width,
			"height": m.Height,
			"type":   "static",
		})
		return
	}

	if am, ok := a.stmapRegistry.GetAnimated(name); ok {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name":        am.Name,
			"width":       am.Frames[0].Width,
			"height":      am.Frames[0].Height,
			"type":        "animated",
			"frame_count": len(am.Frames),
		})
		return
	}

	httperr.WriteErr(w, http.StatusNotFound, stmap.ErrNotFound)
}

// handleSTMapDelete removes a stored map.
func (a *API) handleSTMapDelete(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	name := r.PathValue("name")
	if err := a.stmapRegistry.Delete(name); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSTMapDownload returns a map as raw binary.
func (a *API) handleSTMapDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	m, ok := a.stmapRegistry.Get(name)
	if !ok {
		httperr.WriteErr(w, http.StatusNotFound, stmap.ErrNotFound)
		return
	}

	data, err := stmap.WriteRaw(m)
	if err != nil {
		httperr.WriteErr(w, http.StatusInternalServerError, err)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".stmap"}))
	_, _ = w.Write(data)
}

// handleSTMapAssignSource assigns a map to a source.
func (a *API) handleSTMapAssignSource(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	sourceKey := r.PathValue("sourceKey")
	var req stmapAssignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Map == "" {
		httperr.Write(w, http.StatusBadRequest, "map name is required")
		return
	}

	if err := a.stmapRegistry.AssignSource(sourceKey, req.Map); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.stmapRegistry.State())
}

// handleSTMapRemoveSource removes a map assignment from a source.
func (a *API) handleSTMapRemoveSource(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	sourceKey := r.PathValue("sourceKey")
	a.stmapRegistry.RemoveSource(sourceKey)
	w.WriteHeader(http.StatusNoContent)
}

// handleSTMapAssignProgram assigns a map to the program output.
func (a *API) handleSTMapAssignProgram(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	var req stmapAssignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Map == "" {
		httperr.Write(w, http.StatusBadRequest, "map name is required")
		return
	}

	if err := a.stmapRegistry.AssignProgram(req.Map); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.stmapRegistry.State())
}

// handleSTMapRemoveProgram removes the program map assignment.
func (a *API) handleSTMapRemoveProgram(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	a.stmapRegistry.RemoveProgram()
	w.WriteHeader(http.StatusNoContent)
}
