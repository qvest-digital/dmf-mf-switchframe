package main

import (
	"log/slog"

	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
)

// enrichState patches a ControlRoomState snapshot with output, graphics,
// operator, and replay status. gfxOverride, if non-nil, is used instead of
// calling compositor.Status() (which would deadlock when called from the
// compositor's own callback).
func (a *App) enrichState(state internal.ControlRoomState, gfxOverride *graphics.State) internal.ControlRoomState {
	if p := a.api.LastOperator(); p != nil {
		state.LastChangedBy = *p
	}
	if recStatus := a.outputMgr.RecordingStatus(); recStatus.Active {
		state.Recording = &recStatus
	}
	if srtStatus := a.outputMgr.SRTOutputStatus(); srtStatus.Active {
		state.SRTOutput = &srtStatus
	}
	if dests := a.outputMgr.ListDestinations(); len(dests) > 0 {
		destInfos := make([]internal.DestinationInfo, len(dests))
		for i, d := range dests {
			destInfos[i] = internal.DestinationInfo{
				ID:             d.ID,
				Name:           d.Config.Name,
				Type:           d.Config.Type,
				Address:        d.Config.Address,
				Port:           d.Config.Port,
				State:          d.State,
				BytesWritten:   d.BytesWritten,
				DroppedPackets: d.DroppedPackets,
				Connections:    d.Connections,
				Error:          d.Error,
			}
		}
		state.Destinations = destInfos
	}
	var gfxStatus graphics.State
	if gfxOverride != nil {
		gfxStatus = *gfxOverride
	} else {
		gfxStatus = a.compositor.Status()
	}
	if gfxStatus.Active {
		state.Graphics = &internal.GraphicsState{
			Active:       gfxStatus.Active,
			Template:     gfxStatus.Template,
			FadePosition: gfxStatus.FadePosition,
		}
	}

	// Enrich with operator and lock state.
	if a.operatorStore.Count() > 0 {
		operators := a.operatorStore.List()
		sessions := a.sessionMgr.ActiveSessions()
		connectedSet := make(map[string]bool, len(sessions))
		for _, s := range sessions {
			connectedSet[s.OperatorID] = true
		}
		opInfos := make([]internal.OperatorInfo, len(operators))
		for i, op := range operators {
			opInfos[i] = internal.OperatorInfo{
				ID:        op.ID,
				Name:      op.Name,
				Role:      string(op.Role),
				Connected: connectedSet[op.ID],
			}
		}
		state.Operators = opInfos

		locks := a.sessionMgr.ActiveLocks()
		if len(locks) > 0 {
			lockMap := make(map[string]internal.LockInfo, len(locks))
			for sub, info := range locks {
				lockMap[string(sub)] = internal.LockInfo{
					HolderID:   info.HolderID,
					HolderName: info.HolderName,
					AcquiredAt: info.AcquiredAt.UnixMilli(),
				}
			}
			state.Locks = lockMap
		}
	}

	if a.replayMgr != nil {
		rs := a.replayMgr.Status()
		if rs.State != "idle" || rs.MarkIn != nil || len(rs.Buffers) > 0 {
			rState := &internal.ReplayState{
				State:      string(rs.State),
				Source:     rs.Source,
				Speed:      rs.Speed,
				Loop:       rs.Loop,
				Position:   rs.Position,
				MarkIn:     rs.MarkInUnixMs(),
				MarkOut:    rs.MarkOutUnixMs(),
				MarkSource: rs.MarkSource,
			}
			for _, b := range rs.Buffers {
				rState.Buffers = append(rState.Buffers, internal.ReplayBufferInfo{
					Source:       b.Source,
					FrameCount:   b.FrameCount,
					GOPCount:     b.GOPCount,
					DurationSecs: b.DurationSecs,
					BytesUsed:    b.BytesUsed,
				})
			}
			state.Replay = rState
		}
	}
	return state
}

// broadcastState publishes an enriched state snapshot to all MoQ subscribers.
// gfxOverride is passed through to enrichState (non-nil when called from
// the compositor's own callback to avoid deadlock).
func (a *App) broadcastState(gfxOverride *graphics.State) {
	a.controlPub.Publish(a.enrichState(a.sw.State(), gfxOverride))
}

// videoInfoCallback returns a callback for OnVideoInfoChange that updates the
// program relay's VideoInfo. Used by both the compositor and key bridge.
func (a *App) videoInfoCallback(subsystem string) func(sps, pps []byte, width, height int) {
	return func(sps, pps []byte, width, height int) {
		avcC := a.buildAVCConfig(sps, pps)
		if avcC != nil {
			a.programRelay.SetVideoInfo(a.buildVideoInfo(sps, avcC, width, height))
			slog.Info(subsystem+": updated program relay VideoInfo", "w", width, "h", height)
		}
	}
}

// wireStateCallbacks connects all subsystem state-change callbacks to the
// centralized state broadcast.
func (a *App) wireStateCallbacks() {
	// Allow REST API handlers to return enriched state.
	a.api.SetEnrichFunc(func(s internal.ControlRoomState) internal.ControlRoomState {
		return a.enrichState(s, nil)
	})

	// Switcher state changes (cut, preview, transition, etc.).
	a.sw.OnStateChange(func(state internal.ControlRoomState) {
		a.controlPub.Publish(a.enrichState(state, nil))
	})

	// Output state changes (recording start/stop, SRT connect/disconnect).
	a.outputMgr.OnStateChange(func() {
		a.clearLastOperator()
		a.broadcastState(nil)
	})

	// Graphics overlay state changes: receives snapshot directly to avoid deadlock.
	a.compositor.OnStateChange(func(gfxState graphics.State) {
		a.clearLastOperator()
		a.broadcastState(&gfxState)
	})

	// Replay state changes.
	if a.replayMgr != nil {
		a.replayMgr.OnStateChange(func() {
			a.clearLastOperator()
			a.broadcastState(nil)
		})
	}

	// Operator session/lock changes.
	a.sessionMgr.OnStateChange(func() {
		a.clearLastOperator()
		a.broadcastState(nil)
	})
}

// clearLastOperator resets the last-operator field before a state broadcast
// triggered by a non-handler callback (output, compositor, replay, session).
func (a *App) clearLastOperator() {
	var empty string
	a.api.SetLastOperator(&empty)
}
