package main

import (
	"log/slog"
	"sync/atomic"

	"github.com/zsiec/ccx"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/internal"
)

// initCaptions sets up the caption manager when --captions is enabled.
// Creates the manager, wires it to the switcher for SEI injection,
// and optionally sets up VANC output for MXL SDI.
func (a *App) initCaptions() error {
	if !a.cfg.Captions {
		return nil
	}

	mgr := caption.NewManager()
	a.captionMgr = mgr

	// Wire caption manager to switcher for SEI injection.
	a.sw.SetCaptionManager(mgr)

	// Wire state change callback to trigger broadcast.
	mgr.OnStateChange(func() {
		a.broadcastState(nil)
	})

	// Wire broadcast sink so authored captions go to the MoQ captions track.
	mgr.SetBroadcastSink(func(frame *ccx.CaptionFrame) {
		if relay := a.sw.ProgramRelay(); relay != nil {
			relay.BroadcastCaptions(frame)
		}
	})

	// Wire VANC sink if MXL output is configured.
	if a.mxlOutput != nil {
		var seq atomic.Uint32
		mgr.SetVANCSink(func(pairs []caption.CCPair) {
			s := uint16(seq.Add(1))
			cdp := caption.BuildCDP(pairs, s, 0)
			if cdp == nil {
				return
			}
			packet, err := caption.WrapCaptionST291(cdp)
			if err != nil {
				slog.Error("caption VANC wrap failed", "err", err)
				return
			}
			if err := a.mxlOutput.Writer().WriteDataGrain(packet); err != nil {
				slog.Error("caption VANC write failed", "err", err)
			}
		})
	}

	slog.Info("captions enabled")
	return nil
}

// enrichCaptionState adds caption state to a ControlRoomState snapshot.
func (a *App) enrichCaptionState(state internal.ControlRoomState) internal.ControlRoomState {
	if a.captionMgr == nil {
		return state
	}

	cs := a.captionMgr.State()
	state.Captions = &internal.CaptionState{
		Mode:           cs.Mode,
		AuthorBuffer:   cs.AuthorBuffer,
		SourceCaptions: cs.SourceCaptions,
	}

	// Populate HasCaptions on SourceInfo.
	if cs.SourceCaptions != nil && state.Sources != nil {
		for key, si := range state.Sources {
			if cs.SourceCaptions[key] {
				si.HasCaptions = true
				state.Sources[key] = si
			}
		}
	}

	return state
}
