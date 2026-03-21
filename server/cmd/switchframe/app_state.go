package main

import (
	"encoding/hex"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/stmap"
)

// sourceType derives the source type string from a source key prefix.
func sourceType(key string) string {
	switch {
	case strings.HasPrefix(key, "srt:"):
		return "srt"
	case strings.HasPrefix(key, "mxl:"):
		return "mxl"
	case strings.HasPrefix(key, "clip-player-"):
		return "clip"
	case key == "replay":
		return "replay"
	default:
		return "demo"
	}
}

// enrichState patches a ControlRoomState snapshot with output, graphics,
// operator, and replay status. gfxOverride, if non-nil, is used instead of
// calling compositor.Status() (which would deadlock when called from the
// compositor's own callback).
func (a *App) enrichState(state internal.ControlRoomState, gfxOverride *graphics.State) internal.ControlRoomState {
	// Enrich sources with type field and SRT info.
	for key, info := range state.Sources {
		info.Type = sourceType(key)
		if info.Type == "srt" && a.srtStats != nil {
			cs, ok := a.srtStats.Get(key)
			if !ok {
				state.Sources[key] = info
				continue
			}
			srtInfo := cs.ToSRTSourceInfo()
			info.SRTInfo = &internal.SRTSourceInfo{
				Mode:                 srtInfo.Mode,
				StreamID:             srtInfo.StreamID,
				RemoteAddr:           srtInfo.RemoteAddr,
				LatencyMs:            srtInfo.LatencyMs,
				NegotiatedLatencyMs:  srtInfo.NegotiatedLatencyMs,
				RTTMs:                srtInfo.RTTMs,
				RTTVarMs:             srtInfo.RTTVarMs,
				LossRate:             srtInfo.LossRate,
				BitrateKbps:          srtInfo.BitrateKbps,
				RecvBufMs:            srtInfo.RecvBufMs,
				RecvBufPackets:       srtInfo.RecvBufPackets,
				FlightSize:           srtInfo.FlightSize,
				Connected:            srtInfo.Connected,
				UptimeMs:             srtInfo.UptimeMs,
				PacketsReceived:      srtInfo.PacketsReceived,
				PacketsLost:          srtInfo.PacketsLost,
				PacketsDropped:       srtInfo.PacketsDropped,
				PacketsRetransmitted: srtInfo.PacketsRetransmitted,
				PacketsBelated:       srtInfo.PacketsBelated,
				ReconnectCount:       srtInfo.ReconnectCount,
			}
		}
		state.Sources[key] = info
	}

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
	if len(gfxStatus.Layers) > 0 || gfxStatus.ProgramWidth > 0 {
		gfxState := &internal.GraphicsState{
			ProgramWidth:  gfxStatus.ProgramWidth,
			ProgramHeight: gfxStatus.ProgramHeight,
		}
		for _, l := range gfxStatus.Layers {
			gfxState.Layers = append(gfxState.Layers, internal.GraphicsLayerState{
				ID:            l.ID,
				Template:      l.Template,
				Active:        l.Active,
				FadePosition:  l.FadePosition,
				AnimationMode: l.AnimationMode,
				AnimationHz:   l.AnimationHz,
				ZOrder:        l.ZOrder,
				X:             l.Rect.X,
				Y:             l.Rect.Y,
				Width:         l.Rect.Width,
				Height:        l.Rect.Height,
				ImageName:     l.ImageName,
				ImageWidth:    l.ImageWidth,
				ImageHeight:   l.ImageHeight,
			})
		}
		state.Graphics = gfxState
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

	// CBR pacer status.
	if cbrStatus := a.outputMgr.CBRStatus(); cbrStatus != nil {
		state.CBR = &internal.CBRStatus{
			Enabled:          cbrStatus.Enabled,
			MuxrateBps:       cbrStatus.MuxrateBps,
			NullPacketsTotal: cbrStatus.NullPacketsTotal,
			RealBytesTotal:   cbrStatus.RealBytesTotal,
			PadBytesTotal:    cbrStatus.PadBytesTotal,
			BurstTicksTotal:  cbrStatus.BurstTicksTotal,
		}
	}

	if a.scte35Injector != nil {
		s := a.scte35Injector.State()
		state.SCTE35 = &internal.SCTE35State{
			Enabled:        true,
			SCTE104Enabled: a.cfg.SCTE104,
			ActiveEvents:   convertActiveEvents(s.ActiveEvents),
			EventLog:       convertEventLog(s.EventLog),
			HeartbeatOK:    s.HeartbeatOK,
			Config: internal.SCTE35Config{
				HeartbeatIntervalMs: a.cfg.SCTE35HeartbeatMs,
				DefaultPreRollMs:    a.cfg.SCTE35PreRollMs,
				PID:                 a.cfg.SCTE35PID,
				VerifyEncoding:      a.cfg.SCTE35Verify,
				WebhookURL:          a.cfg.SCTE35WebhookURL,
			},
		}
	}

	// Layout compositor state (PIP slots).
	if a.layoutCompositor != nil {
		if l := a.layoutCompositor.GetLayout(); l != nil {
			ls := &internal.LayoutState{ActivePreset: l.Name}
			for i, slot := range l.Slots {
				ls.Slots = append(ls.Slots, internal.LayoutSlotState{
					ID:         i,
					SourceKey:  slot.SourceKey,
					Enabled:    slot.Enabled,
					X:          slot.Rect.Min.X,
					Y:          slot.Rect.Min.Y,
					Width:      slot.Rect.Dx(),
					Height:     slot.Rect.Dy(),
					ZOrder:     slot.ZOrder,
					Animating:  a.layoutCompositor.SlotAnimating(i),
					ScaleMode:  slot.ScaleMode,
					CropAnchor: slot.CropAnchor,
				})
			}
			state.Layout = ls
		}
	}

	// ST map state (per-source + program assignments).
	if a.stmapRegistry != nil {
		stmapState := a.stmapRegistry.State()
		if len(stmapState.Sources) > 0 || stmapState.Program != nil || len(stmapState.Available) > 0 {
			state.STMap = &internal.STMapState{
				Sources:   stmapState.Sources,
				Available: stmapState.Available,
			}
			if stmapState.Program != nil {
				state.STMap.Program = &internal.STMapProgramState{
					Map:   stmapState.Program.Map,
					Type:  stmapState.Program.Type,
					Frame: stmapState.Program.Frame,
				}
			}
		}
	}

	// Macro execution state (running/completed progress).
	if ms := a.api.MacroState(); ms != nil {
		state.Macro = ms
	}

	// Caption state.
	state = a.enrichCaptionState(state)

	// Operator comms state.
	if a.commsMgr != nil {
		state.Comms = a.commsMgr.State()
	}

	// Clip player state.
	if a.clipMgr != nil {
		states := a.clipMgr.PlayerStates()
		clipPlayers := make([]internal.ClipPlayerInfo, len(states))
		for i, s := range states {
			clipPlayers[i] = internal.ClipPlayerInfo{
				ID:       s.ID,
				ClipID:   s.ClipID,
				ClipName: s.ClipName,
				State:    string(s.State),
				Speed:    s.Speed,
				Position: s.Position,
				Loop:     s.Loop,
			}
		}
		state.ClipPlayers = clipPlayers
	}
	if a.clipStore != nil {
		state.ClipCount = len(a.clipStore.List())
	}
	if up := a.api.UploadProgress(); up != nil {
		state.ClipUpload = up
	}

	// Encoder state (current + available backends).
	// Uses pre-computed internal.EncoderInfo slice to avoid per-broadcast allocations.
	if avail := a.sw.AvailableEncodersInternal(); len(avail) > 0 {
		state.Encoder = &internal.EncoderState{
			Current:   a.sw.EncoderName(),
			Available: avail,
		}
	}

	// Connection info for the UI (SRT ingest + output ports).
	if a.cfg.SRTListen != "" || a.cfg.SRTOutputPortBase > 0 {
		ci := &internal.ConnectionInfo{
			Domain: a.cfg.Domain,
		}
		// Parse ingest port from --srt-listen (e.g., ":6464" -> 6464).
		if a.cfg.SRTListen != "" {
			if _, portStr, err := net.SplitHostPort(a.cfg.SRTListen); err == nil {
				if p, err := strconv.Atoi(portStr); err == nil {
					ci.SRTIngestPort = p
				}
			}
		}
		// Expand output port range to slice.
		if a.cfg.SRTOutputPortBase > 0 {
			for p := a.cfg.SRTOutputPortBase; p <= a.cfg.SRTOutputPortEnd; p++ {
				ci.SRTOutputPorts = append(ci.SRTOutputPorts, p)
			}
		}
		state.ConnectionInfo = ci
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

	// Allow macro runner to trigger state broadcast on execution changes.
	a.api.SetBroadcastFunc(func() { a.broadcastState(nil) })

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
	// Only rebuild the pipeline when the compositor's Active() state actually
	// changes (e.g., first layer turned on, last layer turned off). Fade ticks
	// fire onStateChange ~60x/sec but don't change Active() — rebuilding on
	// Graphics state changes no longer trigger pipeline rebuild.
	// The compositor node is always active in the pipeline with a fast
	// no-op path when no layers are visible (<1µs RLock check). This
	// eliminates the single-frame stutter from encode goroutine handoff.
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

	// SCTE-35 injector state changes.
	if a.scte35Injector != nil {
		a.scte35Injector.OnStateChange(func() {
			a.clearLastOperator()
			a.broadcastState(nil)
		})
	}

	// ST map registry state changes. Trigger pipeline rebuild so the
	// stmap-program node picks up active state changes (e.g., program
	// map assigned/removed affects node Active() and lip-sync hint).
	if a.stmapRegistry != nil {
		a.stmapRegistry.SetOnStateChange(func(_ stmap.STMapState) {
			if a.sw != nil {
				a.sw.RebuildPipeline()
			}
			a.clearLastOperator()
			a.broadcastState(nil)
		})
	}
}

// clearLastOperator resets the last-operator field before a state broadcast
// triggered by a non-handler callback (output, compositor, replay, session).
func (a *App) clearLastOperator() {
	var empty string
	a.api.SetLastOperator(&empty)
}

// convertActiveEvents converts scte35.ActiveEventState map to internal.SCTE35Active map.
func convertActiveEvents(src map[uint32]scte35.ActiveEventState) map[uint32]internal.SCTE35Active {
	if len(src) == 0 {
		return nil
	}
	out := make(map[uint32]internal.SCTE35Active, len(src))
	for id, ae := range src {
		active := internal.SCTE35Active{
			EventID:       ae.EventID,
			CommandType:   ae.CommandType,
			IsOut:         ae.IsOut,
			DurationMs:    ae.DurationMs,
			ElapsedMs:     ae.ElapsedMs,
			RemainingMs:   ae.RemainingMs,
			AutoReturn:    ae.AutoReturn,
			Held:          ae.Held,
			SpliceTimePTS: ae.SpliceTimePTS,
			StartedAt:     ae.StartedAt,
		}
		if len(ae.Descriptors) > 0 {
			active.Descriptors = make([]internal.SCTE35DescriptorInfo, len(ae.Descriptors))
			for i, d := range ae.Descriptors {
				active.Descriptors[i] = convertDescriptor(d)
			}
		}
		out[id] = active
	}
	return out
}

// convertEventLog converts scte35.EventLogEntry slice to internal.SCTE35Event slice.
func convertEventLog(src []scte35.EventLogEntry) []internal.SCTE35Event {
	if len(src) == 0 {
		return nil
	}
	out := make([]internal.SCTE35Event, len(src))
	for i, e := range src {
		evt := internal.SCTE35Event{
			EventID:     e.EventID,
			CommandType: e.CommandType,
			IsOut:       e.IsOut,
			DurationMs:  e.DurationMs,
			AutoReturn:  e.AutoReturn,
			Timestamp:   e.Timestamp,
			Status:      e.Status,
			Source:      e.Source,
		}
		if e.SpliceTimePTS != nil {
			pts := *e.SpliceTimePTS
			evt.SpliceTimePTS = &pts
		}
		if e.AvailNum != 0 {
			an := e.AvailNum
			evt.AvailNum = &an
		}
		if e.AvailsExpected != 0 {
			ae := e.AvailsExpected
			evt.AvailsExpected = &ae
		}
		if len(e.Descriptors) > 0 {
			evt.Descriptors = make([]internal.SCTE35DescriptorInfo, len(e.Descriptors))
			for j, d := range e.Descriptors {
				evt.Descriptors[j] = convertDescriptor(d)
			}
		}
		out[i] = evt
	}
	return out
}

// convertDescriptor converts a scte35.SegmentationDescriptor to internal.SCTE35DescriptorInfo.
func convertDescriptor(d scte35.SegmentationDescriptor) internal.SCTE35DescriptorInfo {
	info := internal.SCTE35DescriptorInfo{
		SegEventID:           d.SegEventID,
		SegmentationType:     d.SegmentationType,
		SegmentationTypeName: segmentationTypeNames[d.SegmentationType],
		UPIDType:             d.UPIDType,
		UPIDTypeName:         upidTypeNames[d.UPIDType],
		SubSegmentNum:        d.SubSegmentNum,
		SubSegmentsExpected:  d.SubSegmentsExpected,
		Cancelled:            d.SegmentationEventCancelIndicator,
	}
	if len(d.UPID) > 0 {
		info.UPID = hex.EncodeToString(d.UPID)
	}
	if d.DurationTicks != nil {
		ms := int64(*d.DurationTicks / 90) // 90 kHz ticks to ms
		info.DurationMs = &ms
	}
	return info
}

// segmentationTypeNames maps SCTE-35 segmentation_type_id values to human-readable names.
var segmentationTypeNames = map[uint8]string{
	0x00: "Not Indicated",
	0x01: "Content Identification",
	0x10: "Program Start",
	0x11: "Program End",
	0x12: "Program Early Termination",
	0x13: "Program Breakaway",
	0x14: "Program Resumption",
	0x15: "Program Runover Planned",
	0x16: "Program Runover Unplanned",
	0x17: "Program Overlap Start",
	0x18: "Program Blackout Override",
	0x19: "Program Start In Progress",
	0x20: "Chapter Start",
	0x21: "Chapter End",
	0x22: "Break Start",
	0x23: "Break End",
	0x30: "Provider Advertisement Start",
	0x31: "Provider Advertisement End",
	0x32: "Distributor Advertisement Start",
	0x33: "Distributor Advertisement End",
	0x34: "Provider Placement Opportunity Start",
	0x35: "Provider Placement Opportunity End",
	0x36: "Distributor Placement Opportunity Start",
	0x37: "Distributor Placement Opportunity End",
	0x40: "Unscheduled Event Start",
	0x41: "Unscheduled Event End",
	0x42: "Alternate Content Opportunity Start",
	0x43: "Alternate Content Opportunity End",
	0x44: "Provider Ad Block Start",
	0x45: "Provider Ad Block End",
	0x46: "Distributor Ad Block Start",
	0x47: "Distributor Ad Block End",
	0x50: "Network Start",
	0x51: "Network End",
}

// upidTypeNames maps SCTE-35 segmentation_upid_type values to human-readable names.
var upidTypeNames = map[uint8]string{
	0x00: "Not Used",
	0x01: "User Defined (Deprecated)",
	0x02: "ISCI (Deprecated)",
	0x03: "Ad-ID",
	0x04: "UMID",
	0x05: "ISAN (Deprecated)",
	0x06: "ISAN",
	0x07: "TID",
	0x08: "TI",
	0x09: "ADI",
	0x0A: "EIDR",
	0x0B: "ATSC Content Identifier",
	0x0C: "MPU",
	0x0D: "MID",
	0x0E: "ADS Information",
	0x0F: "URI",
	0x10: "UUID",
	0x11: "SCR",
}
