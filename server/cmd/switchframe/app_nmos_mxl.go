package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/nmos"
	"github.com/zsiec/switchframe/server/peer"
)

// mxlNMOSConfig configures the NMOS node for the named MXL inputs
// (--mxl-nmos-*). Port 0 disables it.
type mxlNMOSConfig struct {
	Port        int
	Seed        string
	Label       string
	Host        string
	RegistryURL string
	QueryURL    string
}

// peerForwardHeader marks an IS-05 request one engine of an active-active
// pair passes on to the other, which applies it without passing it back.
const peerForwardHeader = "X-Switchframe-Peer-Forward"

type peerForwardKey struct{}

func fromPeer(ctx context.Context) bool {
	v, _ := ctx.Value(peerForwardKey{}).(bool)
	return v
}

// mxlEssence is which reader of a named input a receiver drives.
type mxlEssence string

const (
	essenceVideo mxlEssence = "video"
	essenceAudio mxlEssence = "audio"
	essenceData  mxlEssence = "data"
)

type mxlReceiverRef struct {
	input   string
	essence mxlEssence
}

// mxlNMOS is the NMOS node (AMWA IS-04, IS-05 and BCP-007-03) through which
// a controller connects the named MXL inputs to flows.
//
// Each named input is a group of MXL Receivers, one per essence, and a
// connection moves the input's reader to the flow named, which keeps its
// stream and source key. Nothing here switches the program: what an input
// carries changes, not which input is on air.
//
// Both engines of an active-active pair present the same node, with ids
// derived from the same seed. Only the leader registers it, so a controller
// sees one switcher, and each engine passes the activations it receives to
// the other, so a failover keeps the inputs as they were connected.
type mxlNMOS struct {
	cfg      mxlNMOSConfig
	domainID string
	peerBase string

	store     *nmos.ResourceStore
	conn      *nmos.MXLConnectionServer
	receivers map[string]mxlReceiverRef
	order     []string
	reg       *nmos.RegistrationClient
	query     *nmos.QueryClient
	srv       *http.Server
	http      *http.Client

	mu         sync.Mutex
	registered bool
}

// readMXLDomainID returns the id in the domain's domain_def.json, which
// BCP-007-03 has every MXL domain carry.
func readMXLDomainID(domainPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(domainPath, "domain_def.json"))
	if err != nil {
		return "", err
	}
	var def struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &def); err != nil {
		return "", fmt.Errorf("domain_def.json: %w", err)
	}
	if def.ID == "" {
		return "", errors.New("domain_def.json has no id")
	}
	return def.ID, nil
}

// initMXLNMOS starts the NMOS node when --mxl-nmos-port is set.
func (a *App) initMXLNMOS() error {
	if a.cfg.MXLNMOS.Port == 0 {
		return nil
	}
	n, err := a.newMXLNMOS()
	if err != nil {
		return err
	}
	a.mxlNMOS = n

	addr := fmt.Sprintf(":%d", n.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("mxl nmos: listen %s: %w", addr, err)
	}
	n.srv = &http.Server{Handler: n.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := n.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("mxl nmos: server", "error", err)
		}
	}()

	ctx := a.bgCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if n.reg != nil {
		a.bgWG.Add(1)
		go func() { defer a.bgWG.Done(); a.runMXLNMOSRegistration(ctx) }()
	}
	if n.peerBase != "" {
		a.bgWG.Add(1)
		go func() { defer a.bgWG.Done(); a.catchUpMXLNMOS(ctx) }()
	}
	slog.Info("mxl nmos: node up", "port", n.cfg.Port, "receivers", len(n.receivers),
		"registry", n.cfg.RegistryURL, "domain", n.domainID)
	return nil
}

// newMXLNMOS builds the node from the configuration and the named inputs.
func (a *App) newMXLNMOS() (*mxlNMOS, error) {
	cfg := a.cfg.MXLNMOS
	if cfg.Seed == "" {
		return nil, errors.New("--mxl-nmos-seed is required with --mxl-nmos-port: it keeps resource ids stable and both engines of a pair alike")
	}
	if cfg.Host == "" {
		h, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("--mxl-nmos-host: %w", err)
		}
		cfg.Host = h
	}
	if cfg.Label == "" {
		cfg.Label = "switchframe"
	}
	domainID, err := readMXLDomainID(a.cfg.MXLDomain)
	if err != nil {
		// Without the id, auto cannot resolve and every connection that
		// names a domain is refused, but one naming only a flow works.
		slog.Warn("mxl nmos: MXL domain id unknown", "domain", a.cfg.MXLDomain, "error", err)
	}

	n := &mxlNMOS{
		cfg:       cfg,
		domainID:  domainID,
		store:     nmos.NewResourceStore(),
		receivers: map[string]mxlReceiverRef{},
		http:      &http.Client{Timeout: 5 * time.Second},
	}
	if a.cfg.PeerEngineURL != "" {
		if u, err := url.Parse(a.cfg.PeerEngineURL); err == nil && u.Hostname() != "" {
			n.peerBase = "http://" + net.JoinHostPort(u.Hostname(), strconv.Itoa(cfg.Port))
		}
	}
	if cfg.RegistryURL != "" {
		n.reg = nmos.NewRegistrationClient(cfg.RegistryURL, nmos.DeriveID(cfg.Seed, "node"))
	}
	if q := cfg.QueryURL; q != "" || cfg.RegistryURL != "" {
		if q == "" {
			q = cfg.RegistryURL
		}
		n.query = nmos.NewQueryClient(q)
	}
	n.conn = nmos.NewMXLConnectionServer(nmos.MXLConnectionServerOpts{
		DomainID:           domainID,
		OnActivateReceiver: a.activateMXLReceiver,
	})
	if err := a.buildMXLNMOSResources(n); err != nil {
		return nil, err
	}
	return n, nil
}

// handler serves the Node API and the Connection API, marking requests the
// peer engine passed on.
func (n *mxlNMOS) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/x-nmos/node/", nmos.NewNodeServer(n.store).Handler())
	conn := n.conn.Handler()
	mux.Handle("/x-nmos/connection/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(peerForwardHeader) != "" {
			r = r.WithContext(context.WithValue(r.Context(), peerForwardKey{}, true))
		}
		conn.ServeHTTP(w, r)
	}))
	return mux
}

func (a *App) closeMXLNMOS() {
	n := a.mxlNMOS
	if n == nil || n.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = n.srv.Shutdown(ctx)
}

// buildMXLNMOSResources creates the node, its device and a receiver per
// essence of each named input, and records the flows an input was booked
// with as its receivers' active state.
func (a *App) buildMXLNMOSResources(n *mxlNMOS) error {
	cfg := n.cfg
	base := "http://" + net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	nodeID := nmos.DeriveID(cfg.Seed, "node")
	deviceID := nmos.DeriveID(cfg.Seed, "device")
	pf := a.sw.PipelineFormat()

	var specs []mxlSourceSpec
	for _, raw := range a.cfg.MXLSources {
		spec, err := parseMXLSourceSpec(raw)
		if err != nil {
			return err
		}
		if spec.Name != "" {
			specs = append(specs, spec)
		}
	}

	type booked struct {
		id   string
		flow string
	}
	var initial []booked
	var rxIDs []string
	for _, spec := range specs {
		essences := []mxlEssence{essenceVideo, essenceAudio}
		if spec.Data != "" {
			essences = append(essences, essenceData)
		}
		for _, e := range essences {
			id := nmos.DeriveID(cfg.Seed, "receiver", spec.Name, string(e))
			rx := &nmos.Receiver{
				ID:                id,
				Version:           nmos.NewVersion(),
				Label:             spec.Name + " " + string(e),
				Description:       fmt.Sprintf("MXL input %s, %s", spec.Name, e),
				Tags:              map[string][]string{nmos.GroupHintTag: {spec.Name + ":" + groupRole(e)}},
				Transport:         nmos.TransportMXL,
				DeviceID:          deviceID,
				InterfaceBindings: []string{},
				Subscription:      nmos.ReceiverSubscription{},
			}
			switch e {
			case essenceVideo:
				rx.Format = nmos.FormatVideo
				rx.Caps = nmos.ReceiverCaps{
					MediaTypes: []string{nmos.MediaTypeV210},
					ConstraintSets: []map[string]any{{
						nmos.CapMediaType:   map[string]any{"enum": []string{nmos.MediaTypeV210}},
						nmos.CapFrameWidth:  map[string]any{"enum": []int{pf.Width}},
						nmos.CapFrameHeight: map[string]any{"enum": []int{pf.Height}},
						nmos.CapGrainRate: map[string]any{"enum": []map[string]int{
							{"numerator": pf.FPSNum, "denominator": pf.FPSDen},
						}},
					}},
					Version: nmos.NewVersion(),
				}
			case essenceAudio:
				rx.Format = nmos.FormatAudio
				rx.Caps = nmos.ReceiverCaps{
					MediaTypes: []string{nmos.MediaTypeFloat32},
					ConstraintSets: []map[string]any{{
						nmos.CapMediaType:  map[string]any{"enum": []string{nmos.MediaTypeFloat32}},
						nmos.CapSampleRate: map[string]any{"enum": []map[string]int{{"numerator": 48000}}},
					}},
					Version: nmos.NewVersion(),
				}
			case essenceData:
				rx.Format = nmos.FormatData
				rx.Caps = nmos.ReceiverCaps{MediaTypes: []string{nmos.MediaTypeSMPTE291}}
			}
			n.store.AddReceiver(rx)
			n.conn.AddReceiver(id)
			n.receivers[id] = mxlReceiverRef{input: spec.Name, essence: e}
			n.order = append(n.order, id)
			rxIDs = append(rxIDs, id)

			flow := map[mxlEssence]string{essenceVideo: spec.Video, essenceAudio: spec.Audio, essenceData: spec.Data}[e]
			if flow != "" {
				initial = append(initial, booked{id, flow})
			}
		}
	}

	n.store.SetNode(&nmos.Node{
		ID:          nodeID,
		Label:       cfg.Label,
		Description: "switchframe live production switcher",
		Hostname:    cfg.Host,
		Tags:        map[string][]string{},
		Caps:        map[string]interface{}{},
		Href:        base + "/",
		API: nmos.NodeAPI{
			Versions:  []string{"v1.3"},
			Endpoints: []nmos.APIEndpoint{{Host: cfg.Host, Port: cfg.Port, Protocol: "http"}},
		},
		Clocks:     []nmos.Clock{{Name: "clk0", RefType: "internal"}},
		Interfaces: []nmos.Interface{},
		Services:   []interface{}{},
	})
	n.store.AddDevice(&nmos.Device{
		ID:        deviceID,
		Version:   nmos.NewVersion(),
		Label:     cfg.Label,
		Tags:      map[string][]string{},
		Type:      nmos.DeviceTypeGeneric,
		NodeID:    nodeID,
		Senders:   []string{},
		Receivers: rxIDs,
		Controls: []nmos.Control{{
			Type: nmos.ControlTypeSRCtrl,
			Href: base + "/x-nmos/connection/v1.1/",
		}},
	})

	// What the booking named is connected from the start, with no sender a
	// controller made the connection from.
	for _, b := range initial {
		dom := nonEmptyPtr(n.domainID)
		n.conn.SetReceiverActive(b.id, nmos.NewMXLReceiverEndpoint(nil, true, dom, &b.flow))
		n.store.UpdateReceiverSubscription(b.id, nil, true)
	}
	return nil
}

func groupRole(e mxlEssence) string {
	switch e {
	case essenceVideo:
		return "Video"
	case essenceAudio:
		return "Audio"
	}
	return "Data"
}

func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// activateMXLReceiver moves an input's reader to the activated flow, or off
// it, and publishes the new subscription.
func (a *App) activateMXLReceiver(ctx context.Context, id string, active nmos.MXLEndpoint) error {
	n := a.mxlNMOS
	ref, ok := n.receivers[id]
	if !ok {
		return fmt.Errorf("unknown receiver %s", id)
	}
	if err := a.applyMXLReceiver(ref, active); err != nil {
		return err
	}
	n.store.UpdateReceiverSubscription(id, active.SenderID, active.MasterEnable)
	go a.publishMXLReceiver(id)
	if !fromPeer(ctx) && n.peerBase != "" {
		go a.forwardMXLActivation(id, active)
	}
	return nil
}

// applyMXLReceiver retargets the input's reader and, for video, sets the
// input's label to what the connection carries.
func (a *App) applyMXLReceiver(ref mxlReceiverRef, active nmos.MXLEndpoint) error {
	flow := ""
	if active.MasterEnable {
		flow = active.FlowID()
	}
	var err error
	switch ref.essence {
	case essenceVideo:
		err = a.retargetMXLInput(ref.input, &flow, nil, nil)
	case essenceAudio:
		err = a.retargetMXLInput(ref.input, nil, &flow, nil)
	case essenceData:
		err = a.retargetMXLInput(ref.input, nil, nil, &flow)
	}
	if err != nil {
		return err
	}
	if ref.essence == essenceVideo {
		go a.labelMXLInput(ref.input, active)
	}
	return nil
}

// labelMXLInput shows the operator what a video connection carries: the
// connected sender's source label from the Query API, or the input's name
// when it is disconnected or the connection names no sender.
func (a *App) labelMXLInput(input string, active nmos.MXLEndpoint) {
	n := a.mxlNMOS
	label := input
	if active.MasterEnable && active.SenderID != nil && n.query != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		l, err := n.query.SenderLabel(ctx, *active.SenderID)
		if err != nil {
			slog.Warn("mxl nmos: label of connected sender not found", "input", input, "sender", *active.SenderID, "error", err)
		} else if l != "" {
			label = l
		}
	}
	if err := a.SetSourceLabel(context.Background(), "mxl:"+input, label); err != nil {
		slog.Warn("mxl nmos: could not label input", "input", input, "error", err)
	}
}

// forwardMXLActivation passes an activation to the peer engine, which
// applies it without passing it back. A peer that is down catches up when
// it starts.
func (a *App) forwardMXLActivation(id string, active nmos.MXLEndpoint) {
	n := a.mxlNMOS
	dom := "auto"
	body := map[string]any{
		"sender_id":        active.SenderID,
		"master_enable":    active.MasterEnable,
		"activation":       map[string]any{"mode": "activate_immediate"},
		"transport_params": []map[string]any{{nmos.ParamMXLDomainID: &dom, nmos.ParamMXLFlowID: active.TransportParams[0].FlowID}},
	}
	if active.TransportParams[0].FlowID == nil {
		body["transport_params"] = []map[string]any{{nmos.ParamMXLFlowID: nil}}
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPatch, n.peerBase+"/x-nmos/connection/v1.1/single/receivers/"+id+"/staged", bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(peerForwardHeader, "1")
	resp, err := n.http.Do(req)
	if err != nil {
		slog.Warn("mxl nmos: activation not passed to peer", "receiver", id, "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("mxl nmos: peer refused activation", "receiver", id, "status", resp.StatusCode)
	}
}

// catchUpMXLNMOS adopts the connections a running peer's controller made,
// for an engine that starts while its peer already runs. Only receivers a
// controller activated on the peer and none on this engine are taken over;
// the flows a booking names at start are the same on both.
func (a *App) catchUpMXLNMOS(ctx context.Context) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		done, err := a.catchUpMXLNMOSOnce(ctx)
		if done {
			return
		}
		slog.Debug("mxl nmos: peer not readable yet", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (a *App) catchUpMXLNMOSOnce(ctx context.Context) (bool, error) {
	n := a.mxlNMOS
	for _, id := range n.order {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.peerBase+"/x-nmos/connection/v1.1/single/receivers/"+id+"/active", nil)
		if err != nil {
			return false, err
		}
		resp, err := n.http.Do(req)
		if err != nil {
			return false, err
		}
		var peerActive nmos.MXLEndpoint
		err = json.NewDecoder(resp.Body).Decode(&peerActive)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || err != nil {
			return false, fmt.Errorf("peer receiver %s: status %d: %v", id, resp.StatusCode, err)
		}
		mine, _ := n.conn.ReceiverActive(id)
		if peerActive.Activation.ActivationTime == nil || mine.Activation.ActivationTime != nil {
			continue
		}
		peerActive.TransportParams[0].DomainID = nonEmptyPtr(n.domainID)
		if err := a.applyMXLReceiver(n.receivers[id], peerActive); err != nil {
			slog.Warn("mxl nmos: peer connection not adopted", "receiver", id, "error", err)
			continue
		}
		n.conn.SetReceiverActive(id, peerActive)
		n.store.UpdateReceiverSubscription(id, peerActive.SenderID, peerActive.MasterEnable)
		slog.Info("mxl nmos: adopted peer connection", "receiver", id, "flow", peerActive.FlowID())
	}
	return true, nil
}

// leadsMXLNMOS reports whether this engine registers the node: a single
// engine always, a pair's leader only.
func (a *App) leadsMXLNMOS() bool {
	switch a.peerPoller.Role() {
	case peer.RoleStandalone, peer.RoleLeader:
		return true
	}
	return false
}

// runMXLNMOSRegistration keeps the node registered while this engine leads,
// and leaves it to the peer otherwise. Both register the same ids, so a new
// leader's registration replaces the old one's, href included.
func (a *App) runMXLNMOSRegistration(ctx context.Context) {
	n := a.mxlNMOS
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		n.mu.Lock()
		registered := n.registered
		n.mu.Unlock()
		switch {
		case !a.leadsMXLNMOS():
			n.setRegistered(false)
		case !registered:
			if err := a.registerMXLNMOS(ctx); err != nil {
				slog.Warn("mxl nmos: registration failed", "registry", n.cfg.RegistryURL, "error", err)
			} else {
				n.setRegistered(true)
				slog.Info("mxl nmos: registered", "registry", n.cfg.RegistryURL)
			}
		default:
			if err := n.reg.Heartbeat(ctx); err != nil {
				slog.Warn("mxl nmos: heartbeat failed", "error", err)
				if errors.Is(err, nmos.ErrNodeNotFound) {
					n.setRegistered(false)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (n *mxlNMOS) setRegistered(v bool) {
	n.mu.Lock()
	n.registered = v
	n.mu.Unlock()
}

func (a *App) registerMXLNMOS(ctx context.Context) error {
	n := a.mxlNMOS
	node := n.store.GetNode()
	n.store.SetNode(node) // a new version, so the registry takes this href
	node = n.store.GetNode()
	devices := n.store.ListDevices()
	if len(devices) != 1 {
		return fmt.Errorf("%d devices, want 1", len(devices))
	}
	var rx []*nmos.Receiver
	for _, id := range n.order {
		rx = append(rx, n.store.GetReceiver(id))
	}
	return n.reg.RegisterAll(ctx, node, devices[0], nil, nil, nil, rx)
}

// publishMXLReceiver re-registers a receiver whose subscription changed.
func (a *App) publishMXLReceiver(id string) {
	n := a.mxlNMOS
	if n.reg == nil {
		return
	}
	n.mu.Lock()
	registered := n.registered
	n.mu.Unlock()
	if !registered {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := n.reg.RegisterResource(ctx, "receiver", n.store.GetReceiver(id)); err != nil {
		slog.Warn("mxl nmos: receiver update not registered", "receiver", id, "error", err)
	}
}
