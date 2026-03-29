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

	// Enable pic_timing SEI injection for broadcast decoder compatibility.
	// Downstream equipment (Evertz, Harmonic, etc.) requires pic_timing to
	// precede user_data_registered_itu_t_t35 (caption SEI) in each access unit.
	ptCfg := caption.DefaultPicTimingConfig()
	a.sw.SetPicTimingConfig(&ptCfg)

	// Wire state change callback to trigger broadcast.
	mgr.OnStateChange(func() {
		a.broadcastState(nil)
	})

	// Wire broadcast sink so authored captions go to the MoQ captions track
	// on both the program relay (full-quality SRT/recording subscribers) and
	// the program-preview relay (browser multiview subscribers).
	mgr.SetBroadcastSink(func(frame *ccx.CaptionFrame) {
		if relay := a.sw.ProgramRelay(); relay != nil {
			relay.BroadcastCaptions(frame)
		}
		if a.programPreviewRelay != nil {
			a.programPreviewRelay.BroadcastCaptions(frame)
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

	// Mirror passthrough captions (from source SEI) to program-preview relay.
	a.sw.SetOnCaptionBroadcast(func(frame *ccx.CaptionFrame) {
		if a.programPreviewRelay != nil {
			a.programPreviewRelay.BroadcastCaptions(frame)
		}
	})

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
