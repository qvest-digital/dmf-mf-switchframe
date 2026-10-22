package main

import (
	"encoding/hex"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/peer"
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
	case strings.HasPrefix(key, "playout:"):
		return "playout"
	case strings.HasPrefix(key, "clip:"):
		return "clip"
	default:
		return "demo"
	}
}

// enrichState patches a ControlRoomState snapshot with output, graphics,
// operator, and replay status. gfxOverride, if non-nil, is used instead of
// calling compositor.Status() (which would deadlock when called from the
// compositor's own callback).
func (a *App) enrichState(state internal.ControlRoomState, gfxOverride *graphics.State) internal.ControlRoomState {
	// Enrich sources with type field, SRT info, and per-source corrector.
	for key, info := range state.Sources {
		info.Type = sourceType(key)
		// An input on air stays listed even when disconnected, so the
		// operator can see and switch away from what is on air.
		info.Unconnected = key != state.ProgramSource && key != state.PreviewSource &&
			a.mxlInputUnconnected(key)
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
		// Per-source color corrector state.
		if a.sourceCorrectors != nil && a.sourceCorrectors.HasCorrection(key) {
			cs := a.sourceCorrectors.Get(key)
			info.Corrector = &cs
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
				URL:            d.Config.URL,
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
	// DSK fade duration is a top-level field: surface it regardless of
	// whether any layers exist so the UI can render the control from an
	// empty-compositor starting state.
	state.DSKFadeDurationMs = gfxStatus.DSKFadeDurationMs
	if len(gfxStatus.Layers) > 0 || gfxStatus.ProgramWidth > 0 {
		gfxState := &internal.GraphicsState{
			ProgramWidth:  gfxStatus.ProgramWidth,
			ProgramHeight: gfxStatus.ProgramHeight,
		}
		for _, l := range gfxStatus.Layers {
			gfxState.Layers = append(gfxState.Layers, internal.GraphicsLayerState{
				ID:                 l.ID,
				Template:           l.Template,
				Active:             l.Active,
				FadePosition:       l.FadePosition,
				AnimationMode:      l.AnimationMode,
				AnimationHz:        l.AnimationHz,
				ZOrder:             l.ZOrder,
				X:                  l.Rect.X,
				Y:                  l.Rect.Y,
				Width:              l.Rect.Width,
				Height:             l.Rect.Height,
				ImageName:          l.ImageName,
				ImageWidth:         l.ImageWidth,
				ImageHeight:        l.ImageHeight,
				SequenceLoaded:     l.SequenceLoaded,
				SequencePhase:      l.SequencePhase,
				SequenceProcessing: l.SequenceProcessing,
				SequenceProgress:   l.SequenceProgress,
				SequenceSpeed:      l.SequenceSpeed,
			})
		}
		if a.htmlGfxMgr != nil {
			for _, bs := range a.htmlGfxMgr.BrowserStates() {
				if bs.Status == "empty" || bs.LayerID < 0 {
					continue
				}
				for i := range gfxState.Layers {
					if gfxState.Layers[i].ID == bs.LayerID {
						gfxState.Layers[i].HTML5URL = bs.URL
						gfxState.Layers[i].HTML5Format = bs.Format
						gfxState.Layers[i].HTML5Status = bs.Status
						gfxState.Layers[i].HTML5DataSchema = bs.DataSchema
						break
					}
				}
			}
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
				State:          string(rs.State),
				Source:         rs.Source,
				Speed:          rs.Speed,
				Loop:           rs.Loop,
				Position:       rs.Position,
				MarkIn:         rs.MarkInUnixMs(),
				MarkOut:        rs.MarkOutUnixMs(),
				MarkSource:     rs.MarkSource,
				ShutterAngle:   rs.ShutterAngle,
				GPUAccelerated: rs.GPUAccelerated,
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

	// Layout compositor state (PIP slots) — built from DVE compositor.
	if a.dveCompositor != nil {
		if dl := a.dveCompositor.GetLayout(); dl != nil {
			ls := &internal.LayoutState{ActivePreset: dl.Name}
			for i, slot := range dl.Slots {
				tr := slot.Transform
				rect := tr.ToRect() // integer rect for backward compat
				slotState := internal.LayoutSlotState{
					ID:         i,
					SourceKey:  slot.SourceKey,
					Enabled:    slot.Enabled,
					X:          rect.Min.X,
					Y:          rect.Min.Y,
					Width:      rect.Dx(),
					Height:     rect.Dy(),
					ZOrder:     slot.ZOrder,
					Animating:  a.dveCompositor.SlotAnimating(i),
					ScaleMode:  tr.ScaleMode,
					CropAnchor: tr.CropAnchor,
					DVE:        dveSlotStateFromTransform(tr),
				}
				ls.Slots = append(ls.Slots, slotState)
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

	// Neural key state (GPU/TensorRT builds only).
	if a.cfg.NeuralKeyModelPath != "" {
		state.NeuralKey = &internal.NeuralKeyState{Available: true}
	}

	// AI segmentation state (GPU/TensorRT builds only).
	if a.segEngine != nil && a.segAdapter != nil {
		mgr := &aiSegmentManagerAdapter{engine: a.segEngine, adapter: a.segAdapter}
		aiState := &internal.AISegmentationState{
			Available: true,
			ModelName: a.cfg.AIModelsDir,
			Sources:   mgr.AllConfigs(),
		}
		state.AISegmentation = aiState
	}

	// Color grade state.
	if a.colorGradeCtrl != nil {
		cgState := a.colorGradeCtrl.GetState()
		state.ColorGrade = &cgState
	}

	// Macro execution state (running/completed progress).
	if ms := a.api.MacroState(); ms != nil {
		state.Macro = ms
	}

	// Caption state.
	state = a.enrichCaptionState(state)

	// ASR state.
	state = a.enrichASRState(state)

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

	// Playout channel state.
	if a.playoutMgr != nil {
		states := a.playoutMgr.ChannelStates()
		channels := make([]internal.PlayoutChannelInfo, len(states))
		for i, s := range states {
			ch := internal.PlayoutChannelInfo{
				ID:            s.ID,
				State:         s.State,
				Mode:          s.Mode,
				PlaylistName:  s.PlaylistName,
				CurrentItemID: s.CurrentItemID,
				CurrentTitle:  s.CurrentTitle,
				NextItemID:    s.NextItemID,
				NextTitle:     s.NextTitle,
				ItemIndex:     s.ItemIndex,
				ItemCount:     s.ItemCount,
				ItemProgress:  s.ItemProgress,
				ItemRemainMs:  s.ItemRemainMs,
			}
			if s.Pods != nil {
				ch.PodsArmed = s.Pods.PodsArmed
				ch.PodsCompleted = s.Pods.PodsCompleted
				if s.Pods.ActivePod != nil {
					ch.ActivePod = &internal.PodStatusInfo{
						ID:          s.Pods.ActivePod.ID,
						Type:        s.Pods.ActivePod.Type,
						State:       s.Pods.ActivePod.State,
						ElapsedMs:   s.Pods.ActivePod.ElapsedMs,
						RemainingMs: s.Pods.ActivePod.RemainingMs,
						DurationMs:  s.Pods.ActivePod.DurationMs,
						Armed:       s.Pods.ActivePod.Armed,
					}
				}
				for _, ps := range s.Pods.Pods {
					ch.Pods = append(ch.Pods, internal.PodStatusInfo{
						ID:          ps.ID,
						Type:        ps.Type,
						State:       ps.State,
						ElapsedMs:   ps.ElapsedMs,
						RemainingMs: ps.RemainingMs,
						DurationMs:  ps.DurationMs,
						Armed:       ps.Armed,
					})
				}
			}
			channels[i] = ch
		}
		state.Playout = &internal.PlayoutState{Channels: channels}
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

	// Peer engine health status (nil = no peer configured).
	state.EngineLabel = a.cfg.EngineLabel
	if a.peerPoller != nil {
		reachable := a.peerPoller.PeerReachable()
		state.PeerReachable = &reachable
		if lastSeen := a.peerPoller.PeerLastSeen(); !lastSeen.IsZero() {
			state.PeerLastSeenMs = lastSeen.UnixMilli()
		}
		state.EngineBURL = peerBrowserURL(a.cfg)
		state.Role = a.peerPoller.Role().String()
	} else {
		state.Role = peer.RoleStandalone.String()
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
// For AVC pipelines, vps is nil and the callback builds an AVCDecoderConfigurationRecord.
// For HEVC pipelines, vps is set and the codec string is "hev1".
func (a *App) videoInfoCallback(subsystem string) func(vps, sps, pps []byte, width, height int) {
	return func(vps, sps, pps []byte, width, height int) {
		if vps != nil {
			// HEVC path: set codec string for HEVC.
			// DecoderConfig (HEVCDecoderConfigurationRecord) is not yet built
			// — viewers use in-band VPS/SPS/PPS from keyframes for now.
			a.programRelay.SetVideoInfo(distribution.VideoInfo{
				Codec:  "hev1",
				Width:  width,
				Height: height,
			})
			slog.Info(subsystem+": updated program relay VideoInfo (HEVC)", "w", width, "h", height)
		} else {
			// AVC path: build AVCDecoderConfigurationRecord.
			avcC := a.buildAVCConfig(sps, pps)
			if avcC != nil {
				a.programRelay.SetVideoInfo(a.buildVideoInfo(sps, avcC, width, height))
				slog.Info(subsystem+": updated program relay VideoInfo", "w", width, "h", height)
			}
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
	// Track program source to flush ASR on source changes.
	var lastProgramSource string
	a.sw.OnStateChange(func(state internal.ControlRoomState) {
		if state.ProgramSource != lastProgramSource && lastProgramSource != "" {
			a.resetASROnProgramChange()
		}
		lastProgramSource = state.ProgramSource

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

	// HTML5 graphics sidecar state changes.
	if a.htmlGfxMgr != nil {
		a.htmlGfxMgr.OnStateChange(func() {
			a.clearLastOperator()
			a.broadcastState(nil)
		})
	}

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
			PreRollMs:     ae.PreRollMs,
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

// dveSlotStateFromTransform converts a DVE Transform to a DVESlotState for broadcast.
func dveSlotStateFromTransform(t dve.Transform) *internal.DVESlotState {
	state := &internal.DVESlotState{
		PositionX:     t.PositionX,
		PositionY:     t.PositionY,
		Width:         t.Width,
		Height:        t.Height,
		RotationDeg:   t.RotationDeg,
		AnchorX:       t.AnchorX,
		AnchorY:       t.AnchorY,
		PerspectiveX:  t.PerspectiveX,
		PerspectiveY:  t.PerspectiveY,
		CropTop:       t.CropTop,
		CropBottom:    t.CropBottom,
		CropLeft:      t.CropLeft,
		CropRight:     t.CropRight,
		CropSoftness:  t.CropSoftness,
		Opacity:       t.Opacity,
		DefocusRadius: t.DefocusRadius,
		MirrorH:       t.MirrorH,
		MirrorV:       t.MirrorV,
		MosaicSize:    t.MosaicSize,
		Saturation:    t.Saturation,
		Brightness:    t.Brightness,
		Contrast:      t.Contrast,
	}

	if t.Border.Width > 0 || t.Border.CornerRadius > 0 {
		state.Border = &internal.DVEBorderState{
			Width:        t.Border.Width,
			ColorY:       t.Border.ColorY,
			ColorCb:      t.Border.ColorCb,
			ColorCr:      t.Border.ColorCr,
			CornerRadius: t.Border.CornerRadius,
			Softness:     t.Border.Softness,
			InnerWidth:   t.Border.InnerWidth,
		}
	}

	if t.Shadow.Enabled {
		state.Shadow = &internal.DVEShadowState{
			Enabled: true,
			OffsetX: t.Shadow.OffsetX,
			OffsetY: t.Shadow.OffsetY,
			Blur:    t.Shadow.Blur,
			Opacity: t.Shadow.Opacity,
			ColorY:  t.Shadow.ColorY,
		}
	}

	// 3D DVE parameters.
	if t.Is3D() {
		state.Mode3D = t.Mode3D
		state.RotateX = t.Transform3D.RotateX
		state.RotateY = t.Transform3D.RotateY
		state.RotateZ = t.Transform3D.RotateZ
	}

	return state
}

// captureFullSnapshot reads from every subsystem and assembles a
// FullStateSnapshot representing the complete restorable engine state.
// This is used for multi-region active-active redundancy: a browser
// detecting state divergence pulls a snapshot from the authoritative
// engine and pushes it to the lagging one.
func (a *App) captureFullSnapshot() internal.FullStateSnapshot {
	state := a.sw.State()

	snap := internal.FullStateSnapshot{
		Version:    "1",
		CapturedAt: clock.Now(),

		// Switching
		ProgramSource: state.ProgramSource,
		PreviewSource: state.PreviewSource,

		// Transition
		TransitionType:       state.TransitionType,
		TransitionDurationMs: state.TransitionDurationMs,
		TransitionEasing:     state.TransitionEasing,
		FTBActive:            state.FTBActive,

		// Encoder
		Encoder: a.sw.EncoderName(),
	}

	// --- Audio channels ---
	if channels := a.mixer.ChannelStates(); len(channels) > 0 {
		snap.AudioChannels = make(map[string]internal.AudioChannelSnapshot, len(channels))
		for key, ch := range channels {
			acs := internal.AudioChannelSnapshot{
				Level:        ch.Level,
				Trim:         ch.Trim,
				Muted:        ch.Muted,
				AFV:          ch.AFV,
				PhaseInvert:  ch.PhaseInvert,
				Balance:      ch.Balance,
				HPFFrequency: ch.HPFFrequency,
				HPFEnabled:   ch.HPFEnabled,
				Compressor: internal.CompressorSnapshot{
					Threshold:  ch.Compressor.Threshold,
					Ratio:      ch.Compressor.Ratio,
					Attack:     ch.Compressor.Attack,
					Release:    ch.Compressor.Release,
					MakeupGain: ch.Compressor.MakeupGain,
				},
				Gate: internal.GateSnapshot{
					Threshold: ch.Gate.Threshold,
					Range:     ch.Gate.Range,
					Attack:    ch.Gate.Attack,
					Release:   ch.Gate.Release,
					Hold:      ch.Gate.Hold,
					Enabled:   ch.GateEnabled,
				},
				AudioDelayMs: ch.AudioDelayMs,
			}
			for i := 0; i < 3; i++ {
				acs.EQ[i] = internal.EQBandSnapshot{
					Frequency: ch.EQ[i].Frequency,
					Gain:      ch.EQ[i].Gain,
					Q:         ch.EQ[i].Q,
					Enabled:   ch.EQ[i].Enabled,
				}
			}
			snap.AudioChannels[key] = acs
		}
	}

	// --- Audio master bus ---
	snap.AudioMaster.MasterLevel = a.mixer.MasterLevel()
	if state.Limiter != nil {
		snap.AudioMaster.LimiterThreshold = state.Limiter.Threshold
		snap.AudioMaster.LimiterCeiling = state.Limiter.Ceiling
	} else {
		// Fall back to direct mixer query.
		threshold, ceiling, _, _ := a.mixer.LimiterSettings()
		snap.AudioMaster.LimiterThreshold = threshold
		snap.AudioMaster.LimiterCeiling = ceiling
	}

	// --- Graphics (DSK layers) ---
	gfxStatus := a.compositor.Status()
	for _, l := range gfxStatus.Layers {
		snap.Graphics = append(snap.Graphics, internal.GraphicsLayerSnapshot{
			ID:        l.ID,
			Active:    l.Active,
			ZOrder:    l.ZOrder,
			X:         l.Rect.X,
			Y:         l.Rect.Y,
			Width:     l.Rect.Width,
			Height:    l.Rect.Height,
			Template:  l.Template,
			ImageName: l.ImageName,
		})
	}

	// --- DVE / Layout ---
	if a.dveCompositor != nil {
		if dl := a.dveCompositor.GetLayout(); dl != nil {
			snap.Layout.ActivePreset = dl.Name
			for _, slot := range dl.Slots {
				snap.Layout.Slots = append(snap.Layout.Slots, dveSlotSnapshotFromTransform(slot))
			}
		}
	}

	// --- Captions ---
	if a.captionMgr != nil {
		cs := a.captionMgr.State()
		snap.CaptionMode = cs.Mode
	}

	// --- Clip players ---
	snap.ClipPlayers, snap.ClipPlayerPositions = a.captureClipPlayers()

	// --- Source labels and positions ---
	if len(state.Sources) > 0 {
		snap.Sources = make(map[string]internal.SourceConfigSnapshot, len(state.Sources))
		for key, info := range state.Sources {
			snap.Sources[key] = internal.SourceConfigSnapshot{
				Label:    info.Label,
				Position: info.Position,
				DelayMs:  info.DelayMs,
			}
		}
	}

	// --- Upstream keys ---
	for key, info := range state.Sources {
		if info.KeyConfig != nil && info.KeyConfig.Type != "" {
			if snap.UpstreamKeys == nil {
				snap.UpstreamKeys = make(map[string]internal.UpstreamKeySnapshot)
			}
			snap.UpstreamKeys[key] = internal.UpstreamKeySnapshot{
				Type:           info.KeyConfig.Type,
				Enabled:        info.KeyConfig.Enabled,
				KeyColorY:      info.KeyConfig.KeyColorY,
				KeyColorCb:     info.KeyConfig.KeyColorCb,
				KeyColorCr:     info.KeyConfig.KeyColorCr,
				Similarity:     info.KeyConfig.Similarity,
				Smoothness:     info.KeyConfig.Smoothness,
				SpillSuppress:  info.KeyConfig.SpillSuppress,
				SpillReplaceCb: info.KeyConfig.SpillReplaceCb,
				SpillReplaceCr: info.KeyConfig.SpillReplaceCr,
				LowClip:        info.KeyConfig.LowClip,
				HighClip:       info.KeyConfig.HighClip,
				Softness:       info.KeyConfig.Softness,
				FillSource:     info.KeyConfig.FillSource,
			}
		}
	}

	// --- Color grading ---
	if a.colorGradeCtrl != nil {
		cgState := a.colorGradeCtrl.GetState()
		snap.ColorGrade = &internal.ColorGradeSnapshot{
			Enabled:   cgState.Enabled,
			LookName:  cgState.LookName,
			LookGroup: cgState.LookGroup,
			Intensity: cgState.Intensity,
		}
	}

	// --- ST map assignments ---
	if a.stmapRegistry != nil {
		stmapState := a.stmapRegistry.State()
		if len(stmapState.Sources) > 0 || stmapState.Program != nil {
			stSnap := &internal.STMapSnapshot{
				Sources: stmapState.Sources,
			}
			if stmapState.Program != nil {
				stSnap.Program = stmapState.Program.Map
			}
			snap.STMaps = stSnap
		}
	}

	// --- Recording ---
	if recStatus := a.outputMgr.RecordingStatus(); recStatus.Active {
		snap.Recording = &internal.RecordingSnapshot{Active: true}
	}

	// --- Output destinations ---
	if dests := a.outputMgr.ListDestinations(); len(dests) > 0 {
		snap.OutputDestinations = make([]internal.DestinationSnapshot, 0, len(dests))
		for _, d := range dests {
			snap.OutputDestinations = append(snap.OutputDestinations, internal.DestinationSnapshot{
				ID: d.ID,
				Config: internal.DestinationSnapshotConfig{
					Type:     d.Config.Type,
					Address:  d.Config.Address,
					Port:     d.Config.Port,
					URL:      d.Config.URL,
					Latency:  d.Config.Latency,
					StreamID: d.Config.StreamID,
					Name:     d.Config.Name,
					Engine:   d.Config.Engine,
				},
				Active: d.State == "connected" || d.State == "starting",
			})
		}
	}

	// --- Replay marks & player ---
	if a.replayMgr != nil {
		rs := a.replayMgr.Status()
		if rs.MarkIn != nil || rs.MarkOut != nil {
			snap.ReplayMarks = &internal.ReplayMarksSnapshot{
				Source: rs.MarkSource,
			}
			if rs.MarkIn != nil {
				snap.ReplayMarks.MarkIn = rs.MarkIn.UnixMilli()
			}
			if rs.MarkOut != nil {
				snap.ReplayMarks.MarkOut = rs.MarkOut.UnixMilli()
			}
		}
		if rs.State != "idle" {
			snap.ReplayPlayer = &internal.ReplayPlayerSnapshot{
				Source: rs.Source,
				State:  string(rs.State),
				Speed:  rs.Speed,
			}
		}
	}

	// --- Macro execution ---
	if ms := a.api.MacroState(); ms != nil && ms.Running {
		snap.MacroExecution = &internal.MacroExecutionSnapshot{
			MacroID:   ms.MacroName,
			StepIndex: ms.CurrentStep,
			State:     "running",
		}
	}

	// --- Operator sessions ---
	if sessions := a.sessionMgr.ActiveSessions(); len(sessions) > 0 {
		locks := a.sessionMgr.ActiveLocks()
		snap.OperatorSessions = make([]internal.OperatorSessionSnapshot, 0, len(sessions))
		for _, s := range sessions {
			os := internal.OperatorSessionSnapshot{
				OperatorID: s.OperatorID,
				Name:       s.Name,
				Role:       string(s.Role),
			}
			for sub, info := range locks {
				if info.HolderID == s.OperatorID {
					os.LockedSubsystems = append(os.LockedSubsystems, string(sub))
				}
			}
			snap.OperatorSessions = append(snap.OperatorSessions, os)
		}
	}

	// --- Comms participants ---
	if a.commsMgr != nil {
		if cs := a.commsMgr.State(); cs != nil && cs.Active {
			names := make([]string, 0, len(cs.Participants))
			for _, p := range cs.Participants {
				names = append(names, p.Name)
			}
			snap.CommsParticipants = names
		}
	}

	// --- Playout channels ---
	if a.playoutMgr != nil {
		states := a.playoutMgr.ChannelStates()
		for i, s := range states {
			if s.State == "idle" && s.PlaylistName == "" {
				continue
			}
			schedState := s.SchedulerState
			if schedState == "" {
				schedState = s.State // fallback for non-on-air channels
			}
			snap.PlayoutChannels = append(snap.PlayoutChannels, internal.PlayoutChannelSnapshot{
				ChannelIndex:     i,
				ChannelState:     s.State,
				CurrentItemIndex: s.ItemIndex,
				CurrentItemID:    s.CurrentItemID,
				ElapsedMs:        a.playoutMgr.ChannelElapsedMs(i + 1),
				SchedulerState:   schedState,
				SchedulerMode:    s.Mode,
				PlaylistHash:     a.playoutMgr.PlaylistHash(i + 1),
			})
		}
	}

	// --- SCTE-35 active events ---
	if a.scte35Injector != nil {
		s := a.scte35Injector.State()
		for _, ae := range s.ActiveEvents {
			remainMs := int64(0)
			if ae.RemainingMs != nil {
				remainMs = *ae.RemainingMs
			}
			snap.SCTE35Active = append(snap.SCTE35Active, internal.SCTE35ActiveEventSnapshot{
				EventID:     ae.EventID,
				Type:        ae.CommandType,
				RemainingMs: remainMs,
				Hold:        ae.Held,
			})
		}
	}

	// --- Stinger loaded ---
	if a.stingerStore != nil {
		if names := a.stingerStore.List(); len(names) > 0 {
			snap.StingerLoaded = names
		}
	}

	return snap
}

// captureClipPlayers records every clip player slot (ClipPlayers) and, for
// slots holding a clip, its name and playback state (ClipPlayerPositions),
// from a single read of the player states.
func (a *App) captureClipPlayers() ([]internal.ClipPlayerSnapshot, []internal.ClipPositionSnapshot) {
	if a.clipMgr == nil {
		return nil, nil
	}
	var players []internal.ClipPlayerSnapshot
	var positions []internal.ClipPositionSnapshot
	for _, ps := range a.clipMgr.PlayerStates() {
		players = append(players, internal.ClipPlayerSnapshot{
			ID:     ps.ID,
			ClipID: ps.ClipID,
			State:  string(ps.State),
			Speed:  ps.Speed,
			Loop:   ps.Loop,
		})
		if ps.ClipName == "" {
			continue
		}
		posMs := int64(ps.Position * 1000) // position is 0.0-1.0 progress; convert to approx ms
		positions = append(positions, internal.ClipPositionSnapshot{
			ClipName:    ps.ClipName,
			PlayerIndex: ps.ID - 1, // 1-based ID to 0-based index
			PositionMs:  posMs,
			State:       string(ps.State),
			Speed:       ps.Speed,
			Loop:        ps.Loop,
		})
	}
	return players, positions
}

// dveSlotSnapshotFromTransform converts a DVE Slot to a DVESlotSnapshot
// for the full state snapshot.
func dveSlotSnapshotFromTransform(slot dve.Slot) internal.DVESlotSnapshot {
	t := slot.Transform
	ds := internal.DVESlotSnapshot{
		SourceKey:     slot.SourceKey,
		Enabled:       slot.Enabled,
		ZOrder:        slot.ZOrder,
		PositionX:     t.PositionX,
		PositionY:     t.PositionY,
		Width:         t.Width,
		Height:        t.Height,
		RotationDeg:   t.RotationDeg,
		AnchorX:       t.AnchorX,
		AnchorY:       t.AnchorY,
		PerspectiveX:  t.PerspectiveX,
		PerspectiveY:  t.PerspectiveY,
		CropTop:       t.CropTop,
		CropBottom:    t.CropBottom,
		CropLeft:      t.CropLeft,
		CropRight:     t.CropRight,
		CropSoftness:  t.CropSoftness,
		Opacity:       t.Opacity,
		ScaleMode:     t.ScaleMode,
		CropAnchor:    t.CropAnchor,
		DefocusRadius: t.DefocusRadius,
		MirrorH:       t.MirrorH,
		MirrorV:       t.MirrorV,
		MosaicSize:    t.MosaicSize,
		Saturation:    t.Saturation,
		Brightness:    t.Brightness,
		Contrast:      t.Contrast,
	}

	if t.Border.Width > 0 || t.Border.CornerRadius > 0 {
		ds.Border = &internal.DVEBorderSnapshot{
			Width:        t.Border.Width,
			ColorY:       t.Border.ColorY,
			ColorCb:      t.Border.ColorCb,
			ColorCr:      t.Border.ColorCr,
			CornerRadius: t.Border.CornerRadius,
			Softness:     t.Border.Softness,
			InnerWidth:   t.Border.InnerWidth,
		}
	}

	if t.Shadow.Enabled {
		ds.Shadow = &internal.DVEShadowSnapshot{
			Enabled: true,
			OffsetX: t.Shadow.OffsetX,
			OffsetY: t.Shadow.OffsetY,
			Blur:    t.Shadow.Blur,
			Opacity: t.Shadow.Opacity,
			ColorY:  t.Shadow.ColorY,
		}
	}

	if t.Is3D() {
		ds.Mode3D = t.Mode3D
		ds.RotateX = t.Transform3D.RotateX
		ds.RotateY = t.Transform3D.RotateY
		ds.RotateZ = t.Transform3D.RotateZ
	}

	return ds
}

// peerBrowserURL is the peer engine's origin as the browser reaches it:
// --peer-public-url when set, else the --peer-engine-url this engine polls.
// The two differ where engines reach each other on an internal address the
// browser cannot.
func peerBrowserURL(cfg AppConfig) string {
	if cfg.PeerPublicURL != "" {
		return cfg.PeerPublicURL
	}
	return cfg.PeerEngineURL
}
