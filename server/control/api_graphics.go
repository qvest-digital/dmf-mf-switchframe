package control

import (
	"encoding/json"
	"image"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/transition"
)

// graphicsFrameRequest is the JSON body for the graphics frame upload endpoint.
type graphicsFrameRequest struct {
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Template string `json:"template"`
	RGBA     []byte `json:"rgba"`
}

// parseLayerID extracts the layer ID from the URL path parameter.
func parseLayerID(r *http.Request) (int, error) {
	s := r.PathValue("id")
	id, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// handleGraphicsAddLayer creates a new graphics layer.
func (a *API) handleGraphicsAddLayer(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := a.compositor.AddLayer()
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]int{"id": id})
}

// handleGraphicsRemoveLayer removes a graphics layer by ID.
func (a *API) handleGraphicsRemoveLayer(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	if err := a.compositor.RemoveLayer(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGraphicsOn activates a layer immediately (CUT ON).
func (a *API) handleGraphicsOn(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	if err := a.compositor.On(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsOff deactivates a layer immediately (CUT OFF).
func (a *API) handleGraphicsOff(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	if err := a.compositor.Off(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsAutoOn starts a 500ms fade-in transition (AUTO ON).
func (a *API) handleGraphicsAutoOn(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	if err := a.compositor.AutoOn(id, 500*time.Millisecond); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsAutoOff starts a 500ms fade-out transition (AUTO OFF).
func (a *API) handleGraphicsAutoOff(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	if err := a.compositor.AutoOff(id, 500*time.Millisecond); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsStatus returns the current graphics compositor state.
func (a *API) handleGraphicsStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsFrame receives an RGBA overlay frame for a specific layer.
func (a *API) handleGraphicsFrame(w http.ResponseWriter, r *http.Request) {
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}

	body := io.LimitReader(r.Body, 16*1024*1024)

	var req graphicsFrameRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Width <= 0 || req.Height <= 0 {
		httperr.Write(w, http.StatusBadRequest, "width and height must be positive")
		return
	}
	if req.Width > 3840 || req.Height > 2160 {
		httperr.Write(w, http.StatusBadRequest, "resolution exceeds 4K limit")
		return
	}
	expected := req.Width * req.Height * 4
	if len(req.RGBA) != expected {
		httperr.Write(w, http.StatusBadRequest, "rgba data size mismatch")
		return
	}

	if err := a.compositor.SetOverlay(id, req.RGBA, req.Width, req.Height, req.Template); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// animateRequest is the JSON body for the graphics animation endpoint.
type animateRequest struct {
	Mode       string              `json:"mode"`
	MinAlpha   float64             `json:"minAlpha"`
	MaxAlpha   float64             `json:"maxAlpha"`
	SpeedHz    float64             `json:"speedHz"`
	ToRect     *graphics.RectState `json:"toRect,omitempty"`
	ToAlpha    *float64            `json:"toAlpha,omitempty"`
	DurationMs int                 `json:"durationMs,omitempty"`
	Easing     string              `json:"easing,omitempty"`
}

// handleGraphicsAnimate starts an animation on a specific layer.
func (a *API) handleGraphicsAnimate(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	var req animateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Mode != "pulse" && req.Mode != "transition" {
		httperr.Write(w, http.StatusBadRequest, "mode must be \"pulse\" or \"transition\"")
		return
	}
	if req.Mode == "pulse" {
		if req.MinAlpha < 0 || req.MinAlpha > 1 || req.MaxAlpha < 0 || req.MaxAlpha > 1 {
			httperr.Write(w, http.StatusBadRequest, "alpha values must be in [0,1]")
			return
		}
		if req.MinAlpha >= req.MaxAlpha {
			httperr.Write(w, http.StatusBadRequest, "minAlpha must be less than maxAlpha")
			return
		}
		if req.SpeedHz <= 0 || req.SpeedHz > 10 {
			httperr.Write(w, http.StatusBadRequest, "speedHz must be in (0,10]")
			return
		}
	}
	if req.Mode == "transition" {
		if req.ToRect == nil && req.ToAlpha == nil {
			httperr.Write(w, http.StatusBadRequest, "transition mode requires at least one of toRect or toAlpha")
			return
		}
		if req.DurationMs <= 0 {
			httperr.Write(w, http.StatusBadRequest, "durationMs must be positive for transition mode")
			return
		}
	}
	cfg := graphics.AnimationConfig{
		Mode:       req.Mode,
		MinAlpha:   req.MinAlpha,
		MaxAlpha:   req.MaxAlpha,
		SpeedHz:    req.SpeedHz,
		ToRect:     req.ToRect,
		ToAlpha:    req.ToAlpha,
		DurationMs: req.DurationMs,
		Easing:     req.Easing,
	}
	if err := a.compositor.Animate(id, cfg); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsAnimateStop stops any running animation on a layer.
func (a *API) handleGraphicsAnimateStop(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	if err := a.compositor.StopAnimation(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// rectUpdateRequest is the JSON body for updating a layer's rect.
type rectUpdateRequest struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// handleGraphicsLayerRect updates a layer's position rectangle.
func (a *API) handleGraphicsLayerRect(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	var req rectUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	rect := image.Rect(req.X, req.Y, req.X+req.Width, req.Y+req.Height)
	if err := a.compositor.SetLayerRect(id, rect); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// zorderUpdateRequest is the JSON body for updating a layer's z-order.
type zorderUpdateRequest struct {
	ZOrder int `json:"zOrder"`
}

// handleGraphicsLayerZOrder updates a layer's z-order.
func (a *API) handleGraphicsLayerZOrder(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	var req zorderUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.compositor.SetLayerZOrder(id, req.ZOrder); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// flyRequest is the JSON body for fly-in/fly-out endpoints.
type flyRequest struct {
	Direction  string `json:"direction"`
	DurationMs int    `json:"durationMs"`
}

// slideRequest is the JSON body for the slide endpoint.
type slideRequest struct {
	X          int    `json:"x"`
	Y          int    `json:"y"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	DurationMs int    `json:"durationMs"`
	Easing     string `json:"easing,omitempty"`
}

// handleGraphicsFlyIn animates a layer from off-screen to its current position.
func (a *API) handleGraphicsFlyIn(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	var req flyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Direction != "left" && req.Direction != "right" && req.Direction != "top" && req.Direction != "bottom" {
		httperr.Write(w, http.StatusBadRequest, "direction must be \"left\", \"right\", \"top\", or \"bottom\"")
		return
	}
	if req.DurationMs <= 0 {
		req.DurationMs = 500
	}
	if err := a.compositor.FlyIn(id, req.Direction, req.DurationMs); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsFlyOut animates a layer from its current position to off-screen.
func (a *API) handleGraphicsFlyOut(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	var req flyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Direction != "left" && req.Direction != "right" && req.Direction != "top" && req.Direction != "bottom" {
		httperr.Write(w, http.StatusBadRequest, "direction must be \"left\", \"right\", \"top\", or \"bottom\"")
		return
	}
	if req.DurationMs <= 0 {
		req.DurationMs = 500
	}
	if err := a.compositor.FlyOut(id, req.Direction, req.DurationMs); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsSlide animates a layer from its current position to a new rect.
func (a *API) handleGraphicsSlide(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	id, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}
	var req slideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.DurationMs <= 0 {
		req.DurationMs = 500
	}
	toRect := image.Rect(req.X, req.Y, req.X+req.Width, req.Y+req.Height)
	if err := a.compositor.SlideLayer(id, toRect, req.DurationMs); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleStingerList returns all loaded stinger clip names.
func (a *API) handleStingerList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.stingerStore.List())
}

// handleStingerDelete removes a stinger clip by name.
func (a *API) handleStingerDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := a.stingerStore.Delete(name); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stingerCutPointRequest is the JSON body for updating a stinger's cut point.
type stingerCutPointRequest struct {
	CutPoint float64 `json:"cutPoint"`
}

// handleStingerCutPoint updates the cut point for a stinger clip.
func (a *API) handleStingerCutPoint(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req stingerCutPointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.stingerStore.SetCutPoint(name, req.CutPoint); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleStingerUpload accepts a zip file upload containing PNG frames for a stinger.
func (a *API) handleStingerUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)

	data, err := io.ReadAll(r.Body)
	if err != nil {
		httperr.Write(w, http.StatusRequestEntityTooLarge, "upload too large (max 256MB)")
		return
	}

	if err := a.stingerStore.Upload(name, data); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// clipToStingerData converts a stinger.Clip to transition.StingerData.
func clipToStingerData(clip *stinger.Clip) *transition.StingerData {
	frames := make([]transition.StingerFrameData, len(clip.Frames))
	for i, f := range clip.Frames {
		frames[i] = transition.StingerFrameData{
			YUV:   f.YUV,
			Alpha: f.Alpha,
		}
	}
	return &transition.StingerData{
		Frames:          frames,
		Width:           clip.Width,
		Height:          clip.Height,
		CutPoint:        clip.CutPoint,
		Audio:           clip.Audio,
		AudioSampleRate: clip.AudioSampleRate,
		AudioChannels:   clip.AudioChannels,
	}
}
