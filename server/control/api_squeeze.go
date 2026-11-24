package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/dve"
)

// registerSqueezeRoutes registers the squeeze-back routes on the given mux.
func (a *API) registerSqueezeRoutes(mux *http.ServeMux) {
	if a.squeeze == nil {
		return
	}
	mux.HandleFunc("GET /api/squeezeback", a.handleGetSqueeze)
	mux.HandleFunc("PUT /api/squeezeback", a.relayedHandler("squeezeback_set", a.handleSetSqueezeInner))
	mux.HandleFunc("POST /api/squeezeback/on", a.relayedHandler("squeezeback_on", a.handleSqueezeOnInner))
	mux.HandleFunc("POST /api/squeezeback/off", a.relayedHandler("squeezeback_off", a.handleSqueezeOffInner))
}

func (a *API) handleGetSqueeze(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.squeeze.Snapshot())
}

func (a *API) handleSetSqueezeInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	var cfg dve.SqueezeConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if cfg.Background != "" && !a.switcher.HasSource(cfg.Background) {
		httperr.Write(w, http.StatusBadRequest, "unknown background source: "+cfg.Background)
		return
	}
	if err := a.squeeze.Set(cfg); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.squeeze.Snapshot())
}

func (a *API) handleSqueezeOnInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.handleSqueezeMove(w, r, body, true)
}

func (a *API) handleSqueezeOffInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.handleSqueezeMove(w, r, body, false)
}

// handleSqueezeMove runs On or Off; an empty body takes the configured
// duration and easing.
func (a *API) handleSqueezeMove(w http.ResponseWriter, r *http.Request, body []byte, on bool) {
	a.setLastOperator(r)
	if err := a.MoveSqueeze(on, body); err != nil {
		var he *html5Error
		if errors.As(err, &he) {
			httperr.Write(w, he.status, he.msg)
			return
		}
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

// squeezeMoveRequest is an On or Off and the HTML5 graphics data sent
// with it, as the layout around an L-frame changes with the move.
type squeezeMoveRequest struct {
	dve.SqueezeMove
	Graphics []squeezeGraphic `json:"graphics,omitempty"`
	// GraphicsOffsetMs replaces the configured offset for this move.
	GraphicsOffsetMs *int `json:"graphicsOffsetMs,omitempty"`
}

// squeezeGraphic is data for the HTML5 graphic on a layer, as
// PUT /api/graphics/{id}/html5/data sends it.
type squeezeGraphic struct {
	LayerID *int            `json:"layerId"`
	Data    json.RawMessage `json:"data"`
}

// squeezePending is what a move has still to do once its graphics offset
// has passed: send its graphics, or, sent first, start the move.
type squeezePending struct {
	mu    sync.Mutex // guards timer and the gen bump
	gen   atomic.Uint64
	timer *time.Timer

	// sendMu orders graphics sends; held only while sending, never by a
	// caller of MoveSqueeze, which a frame loop may be.
	sendMu sync.Mutex
}

// MoveSqueeze runs a squeezeback On or Off from its request body, and sends
// the HTML5 graphics data the body carries the graphics offset after the
// move starts, before it when negative: the move then waits. A move drops
// what an earlier one has still to do. Graphics are checked before
// anything moves, and an error naming an HTML5 graphic is an *html5Error.
// Graphics are sent on a goroutine of their own: a timed move runs on the
// frame loop, which a slow graphics sidecar must not hold up.
func (a *API) MoveSqueeze(on bool, body []byte) error {
	if a.squeeze == nil {
		return nil
	}
	var req squeezeMoveRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return errors.New("invalid json")
		}
	}
	for _, g := range req.Graphics {
		if g.LayerID == nil {
			return errors.New("graphics layerId is required")
		}
		if len(g.Data) == 0 {
			return errors.New("graphics data is required")
		}
		if _, err := a.html5Browser(*g.LayerID); err != nil {
			return err
		}
	}
	cfg := a.squeeze.Snapshot().SqueezeConfig
	offset := cfg.GraphicsOffset()
	if req.GraphicsOffsetMs != nil {
		offset = (dve.SqueezeConfig{GraphicsOffsetMs: req.GraphicsOffsetMs}).GraphicsOffset()
		if offset < -dve.MaxMorphDurationMs*time.Millisecond || offset > dve.MaxMorphDurationMs*time.Millisecond {
			return fmt.Errorf("graphicsOffsetMs must be between %d and %d, got %d",
				-dve.MaxMorphDurationMs, dve.MaxMorphDurationMs, *req.GraphicsOffsetMs)
		}
	}
	move := func() error {
		if on {
			return a.squeeze.On(req.SqueezeMove)
		}
		return a.squeeze.Off(req.SqueezeMove)
	}
	p := &a.squeezePending
	p.mu.Lock()
	defer p.mu.Unlock()
	gen := p.gen.Add(1)
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	if len(req.Graphics) == 0 {
		return move()
	}
	send := func() {
		p.sendMu.Lock()
		defer p.sendMu.Unlock()
		if p.gen.Load() != gen {
			return
		}
		for _, g := range req.Graphics {
			browserID, err := a.html5Browser(*g.LayerID)
			if err == nil {
				err = a.htmlGfxMgr.Update(browserID, string(g.Data))
			}
			if err != nil {
				slog.Warn("squeezeback: graphics data not sent", "layer", *g.LayerID, "error", err)
			}
		}
	}
	if offset < 0 {
		if err := a.squeeze.CheckMove(on, req.SqueezeMove); err != nil {
			return err
		}
		go send()
		p.timer = time.AfterFunc(-offset, func() {
			p.mu.Lock()
			if p.gen.Load() != gen {
				p.mu.Unlock()
				return
			}
			p.timer = nil
			err := move()
			p.mu.Unlock()
			if err != nil {
				slog.Warn("squeezeback: move after its graphics failed", "on", on, "error", err)
			}
			a.broadcast()
		})
		return nil
	}
	if err := move(); err != nil {
		return err
	}
	if offset == 0 {
		go send()
	} else {
		p.timer = time.AfterFunc(offset, send)
	}
	return nil
}
