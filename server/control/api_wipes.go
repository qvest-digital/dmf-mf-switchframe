package control

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

// registerWipeRoutes registers wipe pattern API routes on the given mux.
func (a *API) registerWipeRoutes(mux *http.ServeMux) {
	if a.wipeStore == nil {
		return
	}
	mux.HandleFunc("GET /api/wipes/patterns", a.handleListWipePatterns)
	mux.HandleFunc("GET /api/wipes/patterns/{id}", a.handleGetWipePattern)
	mux.HandleFunc("GET /api/wipes/patterns/{id}/preview", a.handleWipePatternPreview)
	mux.HandleFunc("POST /api/wipes/custom", a.handleUploadCustomWipe)
	mux.HandleFunc("DELETE /api/wipes/custom/{id}", a.handleDeleteCustomWipe)
}

// handleListWipePatterns returns all registered wipe patterns.
func (a *API) handleListWipePatterns(w http.ResponseWriter, r *http.Request) {
	patterns := a.wipeStore.List()

	// Group by category for the response.
	type categoryGroup struct {
		Category string                `json:"category"`
		Patterns []wipemap.WipePattern `json:"patterns"`
	}

	categories := make(map[string][]wipemap.WipePattern)
	for _, p := range patterns {
		categories[p.Category] = append(categories[p.Category], p)
	}

	var groups []categoryGroup
	// Emit groups in order of the patterns (List() already sorts by category then ID).
	seen := make(map[string]bool)
	for _, p := range patterns {
		if !seen[p.Category] {
			seen[p.Category] = true
			groups = append(groups, categoryGroup{
				Category: p.Category,
				Patterns: categories[p.Category],
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"categories": groups,
		"total":      len(patterns),
	})
}

// handleGetWipePattern returns metadata for a single pattern.
func (a *API) handleGetWipePattern(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := a.wipeStore.GetPattern(id)
	if err != nil {
		httperr.Write(w, http.StatusNotFound, "pattern not found: "+id)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

// handleWipePatternPreview renders the gradient map as a grayscale PNG at 320x180.
func (a *API) handleWipePatternPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	const previewW, previewH = 320, 180
	gm, err := a.wipeStore.GetMap(id, previewW, previewH)
	if err != nil {
		httperr.Write(w, http.StatusNotFound, "pattern not found: "+id)
		return
	}

	// Convert gradient map to grayscale image.
	img := image.NewGray(image.Rect(0, 0, previewW, previewH))
	for y := 0; y < previewH; y++ {
		for x := 0; x < previewW; x++ {
			img.SetGray(x, y, color.Gray{Y: gm[y*previewW+x]})
		}
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_ = png.Encode(w, img)
}

// handleUploadCustomWipe accepts a grayscale PNG upload as a custom wipe pattern.
func (a *API) handleUploadCustomWipe(w http.ResponseWriter, r *http.Request) {
	// Limit upload to 10MB.
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()

	// Decode as PNG.
	img, err := png.Decode(file)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid PNG: "+err.Error())
		return
	}

	bounds := img.Bounds()
	imgW := bounds.Dx()
	imgH := bounds.Dy()
	if imgW < 2 || imgH < 2 {
		httperr.Write(w, http.StatusBadRequest, "image too small (minimum 2x2)")
		return
	}

	// Extract grayscale values.
	mapData := make([]byte, imgW*imgH)
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			// Convert to 8-bit grayscale using luminance formula.
			lum := (19595*r + 38470*g + 7471*b + 1<<15) >> 24
			mapData[y*imgW+x] = byte(lum)
		}
	}

	// Derive ID and name from filename (strip extension).
	name := header.Filename
	id := strings.TrimSuffix(name, ".png")
	id = strings.TrimSuffix(id, ".PNG")
	// Sanitize: only allow alphanumeric, dash, underscore.
	sanitized := make([]byte, 0, len(id))
	for _, c := range []byte(id) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			sanitized = append(sanitized, c)
		}
	}
	id = string(sanitized)
	if id == "" {
		id = "custom"
	}
	// Prefix with "custom-" to avoid collisions with builtins.
	if !strings.HasPrefix(id, "custom-") {
		id = "custom-" + id
	}

	if err := a.wipeStore.UploadCustom(id, name, mapData, imgW, imgH); err != nil {
		httperr.WriteErr(w, http.StatusInternalServerError, err)
		return
	}

	p, _ := a.wipeStore.GetPattern(id)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(p)
}

// handleDeleteCustomWipe deletes a custom wipe pattern.
func (a *API) handleDeleteCustomWipe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.wipeStore.DeleteCustom(id); err != nil {
		if strings.Contains(err.Error(), "builtin") {
			httperr.Write(w, http.StatusForbidden, err.Error())
		} else {
			httperr.Write(w, http.StatusNotFound, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
