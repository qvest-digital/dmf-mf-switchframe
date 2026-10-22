package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/google/uuid"
	"github.com/zsiec/switchframe/server/nmos"
	"github.com/zsiec/switchframe/server/st2110"
	"github.com/zsiec/switchframe/server/switcher"
)

// st2110State holds all ST 2110 subsystem state for the application lifecycle.
type st2110State struct {
	instance   *st2110.Instance
	store      *nmos.ResourceStore
	nodeServer *nmos.NodeServer
	connServer *nmos.ConnectionServer
	httpServer *http.Server
	sources    []*st2110.Source
	sourcesMu  sync.Mutex
	output     *st2110.Output
	dnsSDStop  nmos.StopFunc

	// TX configs for program output. Stored so we can recreate TX handles
	// when the output is restarted (Output.Stop closes TX handles).
	videoTxCfg *st2110.TxVideoConfig
	audioTxCfg *st2110.TxAudioConfig

	// Sender ID tracking for cleanup.
	videoSenderID string
	audioSenderID string

	// NMOS resource IDs for cleanup.
	nodeID   string
	deviceID string

	// Receiver ID -> source key mapping for deactivation.
	receiverSources map[string]string
}

// initST2110 initializes the ST 2110 subsystem: MTL instance, NMOS IS-04
// resource model, IS-05 Connection API with activation callbacks, and DNS-SD
// advertisement. Skipped silently if --st2110-port is not set.
func (a *App) initST2110(ctx context.Context) error {
	if a.cfg.ST2110Port == "" {
		return nil // ST 2110 not configured.
	}

	slog.Info("ST 2110: initializing",
		"port", a.cfg.ST2110Port,
		"ip", a.cfg.ST2110IP,
		"inputs", a.cfg.ST2110Inputs,
		"ptp", a.cfg.ST2110PTP,
		"nmos_port", a.cfg.ST2110NMOSPort,
	)

	// Create MTL instance.
	instance, err := st2110.NewInstance(st2110.InstanceConfig{
		Port:     a.cfg.ST2110Port,
		SourceIP: a.cfg.ST2110IP,
		TxQueues: a.cfg.ST2110Inputs + 2, // inputs + program video/audio TX
		RxQueues: a.cfg.ST2110Inputs * 2, // video + audio per input
		PTPMode:  a.cfg.ST2110PTP,
		LogLevel: 3, // MTL info level
	})
	if err != nil {
		return fmt.Errorf("ST 2110: create MTL instance: %w", err)
	}

	state := &st2110State{
		instance:        instance,
		receiverSources: make(map[string]string),
	}
	a.st2110State = state

	// Build NMOS IS-04 resource model.
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "switchframe"
	}

	state.nodeID = uuid.New().String()
	state.deviceID = uuid.New().String()

	store := nmos.NewResourceStore()
	state.store = store

	store.SetNode(&nmos.Node{
		ID:          state.nodeID,
		Label:       "SwitchFrame",
		Description: "SwitchFrame ST 2110 Node",
		Hostname:    hostname,
		Tags:        map[string][]string{},
		Caps:        map[string]interface{}{},
		Href:        fmt.Sprintf("http://%s:%d/", a.cfg.ST2110IP, a.cfg.ST2110NMOSPort),
		API: nmos.NodeAPI{
			Versions: []string{"v1.3"},
			Endpoints: []nmos.APIEndpoint{
				{
					Host:     a.cfg.ST2110IP,
					Port:     a.cfg.ST2110NMOSPort,
					Protocol: "http",
				},
			},
		},
		Clocks: []nmos.Clock{
			{
				Name:    "clk0",
				RefType: clockRefType(a.cfg.ST2110PTP),
			},
		},
		Interfaces: []nmos.Interface{},
		Services:   []interface{}{},
	})

	// Create device with IS-05 connection API control endpoint.
	connAPIHref := fmt.Sprintf("http://%s:%d/x-nmos/connection/v1.1/", a.cfg.ST2110IP, a.cfg.ST2110NMOSPort)

	var receiverIDs []string
	var senderIDs []string

	// Create receivers (inputs).
	for i := range a.cfg.ST2110Inputs {
		// Video receiver.
		videoRecvID := uuid.New().String()
		videoSourceID := uuid.New().String()
		videoFlowID := uuid.New().String()

		store.AddSource(&nmos.Source{
			ID:        videoSourceID,
			Label:     fmt.Sprintf("ST 2110 Input %d Video", i+1),
			Format:    nmos.FormatVideo,
			DeviceID:  state.deviceID,
			ClockName: "clk0",
			Tags:      map[string][]string{},
			Caps:      map[string]interface{}{},
			Parents:   []string{},
		})

		store.AddFlow(&nmos.Flow{
			ID:        videoFlowID,
			Label:     fmt.Sprintf("ST 2110 Input %d Video Flow", i+1),
			Format:    nmos.FormatVideo,
			SourceID:  videoSourceID,
			DeviceID:  state.deviceID,
			MediaType: "video/raw",
			Tags:      map[string][]string{},
			Parents:   []string{},
		})

		store.AddReceiver(&nmos.Receiver{
			ID:          videoRecvID,
			Label:       fmt.Sprintf("ST 2110 Input %d Video", i+1),
			Description: fmt.Sprintf("ST 2110-20 video receiver for input %d", i+1),
			Format:      nmos.FormatVideo,
			Transport:   nmos.TransportRTP,
			DeviceID:    state.deviceID,
			Tags:        map[string][]string{},
			Caps: nmos.ReceiverCaps{
				MediaTypes: []string{"video/raw"},
			},
			InterfaceBindings: []string{},
			Subscription:      nmos.ReceiverSubscription{},
		})
		receiverIDs = append(receiverIDs, videoRecvID)

		// Audio receiver.
		audioRecvID := uuid.New().String()
		audioSourceID := uuid.New().String()
		audioFlowID := uuid.New().String()

		store.AddSource(&nmos.Source{
			ID:        audioSourceID,
			Label:     fmt.Sprintf("ST 2110 Input %d Audio", i+1),
			Format:    nmos.FormatAudio,
			DeviceID:  state.deviceID,
			ClockName: "clk0",
			Tags:      map[string][]string{},
			Caps:      map[string]interface{}{},
			Parents:   []string{},
			Channels: []nmos.SourceChannel{
				{Label: "Left", Symbol: "L"},
				{Label: "Right", Symbol: "R"},
			},
		})

		store.AddFlow(&nmos.Flow{
			ID:        audioFlowID,
			Label:     fmt.Sprintf("ST 2110 Input %d Audio Flow", i+1),
			Format:    nmos.FormatAudio,
			SourceID:  audioSourceID,
			DeviceID:  state.deviceID,
			MediaType: "audio/L24",
			Tags:      map[string][]string{},
			Parents:   []string{},
			SampleRate: &nmos.Rational{
				Numerator:   48000,
				Denominator: 1,
			},
			BitDepth: 24,
		})

		store.AddReceiver(&nmos.Receiver{
			ID:          audioRecvID,
			Label:       fmt.Sprintf("ST 2110 Input %d Audio", i+1),
			Description: fmt.Sprintf("ST 2110-30 audio receiver for input %d", i+1),
			Format:      nmos.FormatAudio,
			Transport:   nmos.TransportRTP,
			DeviceID:    state.deviceID,
			Tags:        map[string][]string{},
			Caps: nmos.ReceiverCaps{
				MediaTypes: []string{"audio/L24"},
			},
			InterfaceBindings: []string{},
			Subscription:      nmos.ReceiverSubscription{},
		})
		receiverIDs = append(receiverIDs, audioRecvID)
	}

	// Program output senders (video + audio).
	pgmVideoSenderID := uuid.New().String()
	pgmAudioSenderID := uuid.New().String()

	pgmVideoSourceID := uuid.New().String()
	pgmAudioSourceID := uuid.New().String()
	pgmVideoFlowID := uuid.New().String()
	pgmAudioFlowID := uuid.New().String()

	pf := a.sw.PipelineFormat()

	store.AddSource(&nmos.Source{
		ID:        pgmVideoSourceID,
		Label:     "Program Video",
		Format:    nmos.FormatVideo,
		DeviceID:  state.deviceID,
		ClockName: "clk0",
		Tags:      map[string][]string{},
		Caps:      map[string]interface{}{},
		Parents:   []string{},
	})

	store.AddFlow(&nmos.Flow{
		ID:          pgmVideoFlowID,
		Label:       "Program Video Flow",
		Format:      nmos.FormatVideo,
		SourceID:    pgmVideoSourceID,
		DeviceID:    state.deviceID,
		MediaType:   "video/raw",
		Tags:        map[string][]string{},
		Parents:     []string{},
		FrameWidth:  pf.Width,
		FrameHeight: pf.Height,
		Colorspace:  "BT709",
		GrainRate: &nmos.Rational{
			Numerator:   int64(pf.FPSNum),
			Denominator: int64(pf.FPSDen),
		},
	})

	store.AddSender(&nmos.Sender{
		ID:                pgmVideoSenderID,
		Label:             "Program Video",
		Description:       "ST 2110-20 program video output",
		FlowID:            pgmVideoFlowID,
		Transport:         nmos.TransportRTPMcast,
		DeviceID:          state.deviceID,
		ManifestHref:      nmos.StringPtr(fmt.Sprintf("http://%s:%d/x-nmos/connection/v1.1/single/senders/%s/transportfile", a.cfg.ST2110IP, a.cfg.ST2110NMOSPort, pgmVideoSenderID)),
		Tags:              map[string][]string{},
		InterfaceBindings: []string{},
		Subscription:      nmos.SenderSubscription{},
	})
	senderIDs = append(senderIDs, pgmVideoSenderID)

	store.AddSource(&nmos.Source{
		ID:        pgmAudioSourceID,
		Label:     "Program Audio",
		Format:    nmos.FormatAudio,
		DeviceID:  state.deviceID,
		ClockName: "clk0",
		Tags:      map[string][]string{},
		Caps:      map[string]interface{}{},
		Parents:   []string{},
		Channels: []nmos.SourceChannel{
			{Label: "Left", Symbol: "L"},
			{Label: "Right", Symbol: "R"},
		},
	})

	store.AddFlow(&nmos.Flow{
		ID:        pgmAudioFlowID,
		Label:     "Program Audio Flow",
		Format:    nmos.FormatAudio,
		SourceID:  pgmAudioSourceID,
		DeviceID:  state.deviceID,
		MediaType: "audio/L24",
		Tags:      map[string][]string{},
		Parents:   []string{},
		SampleRate: &nmos.Rational{
			Numerator:   48000,
			Denominator: 1,
		},
		BitDepth: 24,
	})

	store.AddSender(&nmos.Sender{
		ID:                pgmAudioSenderID,
		Label:             "Program Audio",
		Description:       "ST 2110-30 program audio output",
		FlowID:            pgmAudioFlowID,
		Transport:         nmos.TransportRTPMcast,
		DeviceID:          state.deviceID,
		ManifestHref:      nmos.StringPtr(fmt.Sprintf("http://%s:%d/x-nmos/connection/v1.1/single/senders/%s/transportfile", a.cfg.ST2110IP, a.cfg.ST2110NMOSPort, pgmAudioSenderID)),
		Tags:              map[string][]string{},
		InterfaceBindings: []string{},
		Subscription:      nmos.SenderSubscription{},
	})
	senderIDs = append(senderIDs, pgmAudioSenderID)

	// Register device with all senders and receivers.
	store.AddDevice(&nmos.Device{
		ID:          state.deviceID,
		Label:       "SwitchFrame Switcher",
		Description: "SwitchFrame live production switcher",
		Type:        nmos.DeviceTypePipeline,
		NodeID:      state.nodeID,
		Tags:        map[string][]string{},
		Senders:     senderIDs,
		Receivers:   receiverIDs,
		Controls: []nmos.Control{
			{
				Type: nmos.ControlTypeSRCtrl,
				Href: connAPIHref,
			},
		},
	})

	// Create IS-04 Node API server.
	state.nodeServer = nmos.NewNodeServer(store)

	// Create IS-05 Connection API server with activation callbacks.
	state.connServer = nmos.NewConnectionServer(nmos.ConnectionServerOpts{
		OnActivateReceiver:   a.activateST2110Receiver,
		OnDeactivateReceiver: a.deactivateST2110Receiver,
		OnActivateSender:     a.activateST2110Sender,
		OnDeactivateSender:   a.deactivateST2110Sender,
	})

	// Register all receivers and senders with the connection server.
	for _, id := range receiverIDs {
		state.connServer.AddReceiver(id)
	}
	for _, id := range senderIDs {
		state.connServer.AddSender(id, "") // SDP generated on activation
	}

	// Start NMOS HTTP server (serves both IS-04 and IS-05).
	mux := http.NewServeMux()
	mux.Handle("/x-nmos/node/", state.nodeServer.Handler())
	mux.Handle("/x-nmos/connection/", state.connServer.Handler())

	listenAddr := fmt.Sprintf(":%d", a.cfg.ST2110NMOSPort)
	state.httpServer = &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		_ = instance.Close()
		a.st2110State = nil
		return fmt.Errorf("ST 2110: listen NMOS HTTP on %s: %w", listenAddr, err)
	}

	go func() {
		if err := state.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("ST 2110: NMOS HTTP server error", "error", err)
		}
	}()

	slog.Info("ST 2110: NMOS HTTP server started",
		"addr", listenAddr,
		"node_id", state.nodeID,
		"device_id", state.deviceID,
		"receivers", len(receiverIDs),
		"senders", len(senderIDs),
	)

	// Advertise via DNS-SD.
	stopDNS, err := nmos.AdvertiseNode(ctx, "switchframe-"+state.nodeID[:8], a.cfg.ST2110NMOSPort, []string{"v1.3"})
	if err != nil {
		slog.Warn("ST 2110: DNS-SD advertisement failed (non-fatal)", "error", err)
	} else {
		state.dnsSDStop = stopDNS
		slog.Info("ST 2110: DNS-SD advertisement started")
	}

	return nil
}

// closeST2110 stops all ST 2110 sources, output, DNS-SD, and the MTL instance
// in reverse initialization order.
func (a *App) closeST2110() {
	state := a.st2110State
	if state == nil {
		return
	}

	slog.Info("ST 2110: shutting down")

	// Stop sources first.
	state.sourcesMu.Lock()
	sources := state.sources
	state.sources = nil
	state.sourcesMu.Unlock()

	for _, src := range sources {
		src.Stop()
	}

	// Stop output.
	if state.output != nil {
		state.output.Stop()
	}

	// Stop NMOS HTTP server.
	if state.httpServer != nil {
		_ = state.httpServer.Close()
	}

	// Stop DNS-SD advertisement.
	if state.dnsSDStop != nil {
		state.dnsSDStop()
	}

	// Close MTL instance last.
	if state.instance != nil {
		if err := state.instance.Close(); err != nil {
			slog.Error("ST 2110: close MTL instance", "error", err)
		}
	}

	slog.Info("ST 2110: shutdown complete")
}

// activateST2110Receiver is called by the IS-05 Connection API when a receiver
// connection is activated. It creates ST 2110 RX sessions and registers the
// source with the switcher and audio mixer, following the app_mxl_demo.go pattern.
func (a *App) activateST2110Receiver(id string, params nmos.StagedParams) error {
	state := a.st2110State
	if state == nil {
		return fmt.Errorf("ST 2110 not initialized")
	}

	// Determine the receiver's format from the resource store.
	recv := state.store.GetReceiver(id)
	if recv == nil {
		return fmt.Errorf("receiver %s not found", id)
	}

	sourceKey := "st2110:" + id

	slog.Info("ST 2110: activating receiver",
		"id", id,
		"format", recv.Format,
		"source_key", sourceKey,
	)

	// Parse transport parameters to build RX config.
	if len(params.TransportParams) == 0 {
		return fmt.Errorf("no transport params for receiver %s", id)
	}
	tp := params.TransportParams[0]

	var multicastIP, sourceIP string
	var port, payloadType int

	if tp.DestinationIP != nil {
		multicastIP = *tp.DestinationIP
	}
	if tp.SourceIP != nil {
		sourceIP = *tp.SourceIP
	}
	if tp.DestinationPort != nil {
		port = *tp.DestinationPort
	}

	// Parse SDP if provided (IS-05 receivers may include transport_file).
	if params.TransportFile != nil && params.TransportFile.Data != "" {
		sdpParams, err := nmos.ParseSDP(params.TransportFile.Data)
		if err != nil {
			slog.Warn("ST 2110: failed to parse SDP, using transport params",
				"id", id, "error", err)
		} else {
			if sdpParams.MulticastIP != "" {
				multicastIP = sdpParams.MulticastIP
			}
			if sdpParams.SourceIP != "" {
				sourceIP = sdpParams.SourceIP
			}
			if sdpParams.Port != 0 {
				port = sdpParams.Port
			}
			if sdpParams.PayloadType != 0 {
				payloadType = sdpParams.PayloadType
			}
		}
	}

	if multicastIP == "" || port == 0 {
		return fmt.Errorf("incomplete transport params for receiver %s: multicast=%q port=%d",
			id, multicastIP, port)
	}

	pf := a.sw.PipelineFormat()

	switch recv.Format {
	case nmos.FormatVideo:
		// Create video RX session.
		videoRx, err := state.instance.CreateVideoRx(st2110.RxVideoConfig{
			MulticastIP: multicastIP,
			SourceIP:    sourceIP,
			Port:        port,
			PayloadType: payloadType,
			Format: st2110.VideoFormat{
				Width:       pf.Width,
				Height:      pf.Height,
				FPSNum:      pf.FPSNum,
				FPSDen:      pf.FPSDen,
				Sampling:    "YCbCr-4:2:2",
				Colorimetry: "BT709",
				BitDepth:    10,
			},
			FrameBuffers: 3,
		})
		if err != nil {
			return fmt.Errorf("create video RX for %s: %w", id, err)
		}

		// Register as MXL source (uses IngestRawVideo path, not relay viewer).
		// Note: mixer channel is NOT added here — only audio receivers register
		// with the mixer. Video-only receivers don't produce audio.
		a.sw.RegisterMXLSource(sourceKey)

		// Create ST 2110 source with callbacks matching app_mxl_demo.go pattern.
		src := st2110.NewSource(st2110.SourceConfig{
			Key:      sourceKey,
			Channels: 2,
			OnRawVideo: func(key string, yuv []byte, w, h int, pts int64) {
				pf := &switcher.ProcessingFrame{
					Data:   yuv,
					Width:  w,
					Height: h,
					PTS:    pts,
					DTS:    pts,
					Codec:  "raw",
				}
				a.sw.IngestRawVideo(key, pf)
			},
		})

		src.Start(context.Background(), videoRx, nil, nil)

		state.sourcesMu.Lock()
		state.sources = append(state.sources, src)
		state.receiverSources[id] = sourceKey
		state.sourcesMu.Unlock()

		// Update receiver subscription in IS-04 model.
		if params.SenderID != nil {
			state.store.UpdateReceiverSubscription(id, params.SenderID, true)
		}

		slog.Info("ST 2110: video receiver activated",
			"id", id,
			"source_key", sourceKey,
			"multicast", multicastIP,
			"port", port,
		)

	case nmos.FormatAudio:
		// Create audio RX session.
		audioRx, err := state.instance.CreateAudioRx(st2110.RxAudioConfig{
			MulticastIP: multicastIP,
			SourceIP:    sourceIP,
			Port:        port,
			PayloadType: payloadType,
			Format: st2110.AudioFormat{
				SampleRate:   48000,
				Channels:     2,
				BitDepth:     24,
				PacketTimeUs: 1000,
			},
			FrameBuffers: 3,
		})
		if err != nil {
			return fmt.Errorf("create audio RX for %s: %w", id, err)
		}

		// For audio-only receivers, register audio channel if not already
		// registered by a paired video receiver.
		a.mixer.AddChannel(sourceKey)
		_ = a.mixer.SetAFV(sourceKey, true)

		src := st2110.NewSource(st2110.SourceConfig{
			Key:      sourceKey,
			Channels: 2,
			OnRawAudio: func(key string, pcm []float32, pts int64, channels int) {
				a.mixer.IngestPCM(key, pcm, pts, channels)
			},
		})

		src.Start(context.Background(), nil, audioRx, nil)

		state.sourcesMu.Lock()
		state.sources = append(state.sources, src)
		state.receiverSources[id] = sourceKey
		state.sourcesMu.Unlock()

		if params.SenderID != nil {
			state.store.UpdateReceiverSubscription(id, params.SenderID, true)
		}

		slog.Info("ST 2110: audio receiver activated",
			"id", id,
			"source_key", sourceKey,
			"multicast", multicastIP,
			"port", port,
		)

	default:
		return fmt.Errorf("unsupported receiver format %q for %s", recv.Format, id)
	}

	return nil
}

// deactivateST2110Receiver stops the source associated with a receiver.
func (a *App) deactivateST2110Receiver(id string) error {
	state := a.st2110State
	if state == nil {
		return fmt.Errorf("ST 2110 not initialized")
	}

	state.sourcesMu.Lock()
	sourceKey, ok := state.receiverSources[id]
	if !ok {
		state.sourcesMu.Unlock()
		slog.Warn("ST 2110: deactivate receiver: no active source", "id", id)
		return nil
	}

	// Find and remove the matching source.
	var found *st2110.Source
	remaining := state.sources[:0]
	for _, src := range state.sources {
		if src.Key() == sourceKey {
			found = src
		} else {
			remaining = append(remaining, src)
		}
	}
	state.sources = remaining
	delete(state.receiverSources, id)
	state.sourcesMu.Unlock()

	if found != nil {
		found.Stop()
	}

	// Clean up switcher and mixer registrations.
	a.sw.UnregisterSource(sourceKey)
	a.mixer.RemoveChannel(sourceKey)

	// Clear receiver subscription in IS-04 model.
	state.store.UpdateReceiverSubscription(id, nil, false)

	slog.Info("ST 2110: receiver deactivated",
		"id", id,
		"source_key", sourceKey,
	)

	return nil
}

// activateST2110Sender is called by the IS-05 Connection API when a sender
// connection is activated. It creates MTL TX sessions and wires program video
// or audio output through an st2110.Output orchestrator.
func (a *App) activateST2110Sender(id string, params nmos.StagedParams) error {
	state := a.st2110State
	if state == nil {
		return fmt.Errorf("ST 2110 not initialized")
	}

	if len(params.TransportParams) == 0 {
		return fmt.Errorf("no transport params for sender %s", id)
	}
	tp := params.TransportParams[0]

	var destIP string
	var port int
	if tp.DestinationIP != nil {
		destIP = *tp.DestinationIP
	}
	if tp.DestinationPort != nil {
		port = *tp.DestinationPort
	}
	if destIP == "" || port == 0 {
		return fmt.Errorf("incomplete transport params for sender %s: dest=%q port=%d", id, destIP, port)
	}

	pf := a.sw.PipelineFormat()

	// Look up the sender and its flow to determine media type.
	sender := state.store.GetSender(id)
	if sender == nil {
		return fmt.Errorf("sender %s not found", id)
	}

	flow := state.store.GetFlow(sender.FlowID)
	if flow == nil {
		return fmt.Errorf("flow %s not found for sender %s", sender.FlowID, id)
	}

	switch flow.Format {
	case nmos.FormatVideo:
		cfg := st2110.TxVideoConfig{
			MulticastIP: destIP,
			SourceIP:    a.cfg.ST2110IP,
			Port:        port,
			Format: st2110.VideoFormat{
				Width:  pf.Width,
				Height: pf.Height,
				FPSNum: pf.FPSNum,
				FPSDen: pf.FPSDen,
			},
			FrameBuffers: 3,
		}

		state.videoTxCfg = &cfg
		state.videoSenderID = id
		if err := a.startOrRestartST2110Output(); err != nil {
			state.videoTxCfg = nil
			state.videoSenderID = ""
			return fmt.Errorf("start output for video sender %s: %w", id, err)
		}

		slog.Info("ST 2110: video sender activated",
			"id", id,
			"dest", destIP,
			"port", port,
			"resolution", fmt.Sprintf("%dx%d", pf.Width, pf.Height),
		)

	case nmos.FormatAudio:
		cfg := st2110.TxAudioConfig{
			MulticastIP: destIP,
			SourceIP:    a.cfg.ST2110IP,
			Port:        port,
			Format: st2110.AudioFormat{
				SampleRate:   48000,
				Channels:     2,
				BitDepth:     24,
				PacketTimeUs: 1000,
			},
			FrameBuffers: 3,
		}

		state.audioTxCfg = &cfg
		state.audioSenderID = id
		if err := a.startOrRestartST2110Output(); err != nil {
			state.audioTxCfg = nil
			state.audioSenderID = ""
			return fmt.Errorf("start output for audio sender %s: %w", id, err)
		}

		slog.Info("ST 2110: audio sender activated",
			"id", id,
			"dest", destIP,
			"port", port,
		)

	default:
		return fmt.Errorf("unsupported format %q for sender %s", flow.Format, id)
	}

	return nil
}

// startOrRestartST2110Output creates (or recreates) the st2110.Output and wires
// the switcher raw video sink and mixer raw audio sink. Called when either the
// video or audio sender is activated. If an output already exists it is stopped
// first — Output.Stop() closes the old TX handles, so fresh TX sessions are
// created from the stored configs.
func (a *App) startOrRestartST2110Output() error {
	state := a.st2110State

	// Stop existing output if any (closes old TX handles).
	if state.output != nil {
		state.output.Stop()
		state.output = nil
	}

	// Create fresh TX handles from stored configs.
	var videoTx st2110.VideoTransmitter
	var audioTx st2110.AudioTransmitter

	if state.videoTxCfg != nil {
		vtx, err := state.instance.CreateVideoTx(*state.videoTxCfg)
		if err != nil {
			return fmt.Errorf("create video TX: %w", err)
		}
		videoTx = vtx
	}

	if state.audioTxCfg != nil {
		atx, err := state.instance.CreateAudioTx(*state.audioTxCfg)
		if err != nil {
			// Clean up video TX if we already created it.
			if videoTx != nil {
				_ = videoTx.Close()
			}
			return fmt.Errorf("create audio TX: %w", err)
		}
		audioTx = atx
	}

	pf := a.sw.PipelineFormat()
	state.output = st2110.NewOutput(st2110.OutputConfig{
		FPSNum: pf.FPSNum,
		FPSDen: pf.FPSDen,
	})
	state.output.Start(videoTx, audioTx, nil)

	// Wire switcher raw video sink to the output.
	if videoTx != nil {
		out := state.output
		a.sw.SetRawVideoSink(switcher.RawVideoSink(func(pf *switcher.ProcessingFrame) {
			out.WriteVideo(pf.Data, pf.Width, pf.Height, pf.PTS)
		}))
		a.updateGPURawVideoSink()
	}

	// Wire mixer raw audio sink to the output.
	if audioTx != nil {
		out := state.output
		a.mixer.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
			out.WriteAudio(pcm, pts, sampleRate, channels)
		})
	}

	return nil
}

// deactivateST2110Sender stops the program output for a sender.
// Output.Stop() closes all TX handles, so both video and audio TX are
// invalidated. Both sinks are cleared and all sender tracking is reset.
func (a *App) deactivateST2110Sender(id string) error {
	state := a.st2110State
	if state == nil {
		return fmt.Errorf("ST 2110 not initialized")
	}

	slog.Info("ST 2110: sender deactivation requested", "id", id)

	// Stop the output (internally closes all TX handles).
	if state.output != nil {
		state.output.Stop()
		state.output = nil
	}

	// Clear all TX configs — Output.Stop() closed the handles so they
	// cannot be reused.
	state.videoTxCfg = nil
	state.audioTxCfg = nil
	state.videoSenderID = ""
	state.audioSenderID = ""

	// Clear both sinks.
	a.sw.SetRawVideoSink(nil)
	a.updateGPURawVideoSink()
	a.mixer.SetRawAudioSink(nil)

	slog.Info("ST 2110: sender deactivated", "id", id)
	return nil
}

// clockRefType returns the IS-04 clock ref_type based on PTP configuration.
func clockRefType(ptpMode string) string {
	if ptpMode != "" && ptpMode != "none" {
		return "ptp"
	}
	return "internal"
}
