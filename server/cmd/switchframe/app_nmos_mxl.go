package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

	// The peer engine's program flow definitions, for the senders of the
	// flows it writes.
	PeerOutputVideoDef string
	PeerOutputAudioDef string
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
// derived from the same seed. The leader registers it, so a controller sees
// one switcher, and so does an engine that cannot reach its peer, so the
// switcher stays registered while the leader is down. Each engine passes the
// activations it receives to the other, so a failover keeps the inputs as
// they were connected.
type mxlNMOS struct {
	cfg      mxlNMOSConfig
	domainID string
	peerBase string

	store     *nmos.ResourceStore
	conn      *nmos.MXLConnectionServer
	receivers map[string]mxlReceiverRef
	order     []string
	// bookings holds each named input's booked flows, by input name.
	bookings map[string]mxlBooking
	state    mxlNMOSState
	reg      *nmos.RegistrationClient
	query    *nmos.QueryClient
	srv      *http.Server
	http     *http.Client

	mu         sync.Mutex
	registered bool
	// forward holds the latest activation of each receiver not yet passed
	// to the peer engine; forwardKick wakes the sender.
	forward     map[string]nmos.MXLEndpoint
	forwardKick chan struct{}
	// labelSeq counts the video connections of each input, so only the
	// latest one's label lookup sets the label. labelMu orders the setting.
	labelSeq map[string]uint64
	labelMu  sync.Mutex
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
	a.restoreMXLNMOS(context.Background())

	addr := fmt.Sprintf(":%d", n.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("mxl nmos: listen %s: %w", addr, err)
	}
	n.srv = &http.Server{Handler: n.handler(a.cfg.APIToken), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := n.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("mxl nmos: server", "error", err)
		}
	}()

	slog.Info("mxl nmos: node up", "port", n.cfg.Port, "receivers", len(n.receivers),
		"registry", n.cfg.RegistryURL, "domain", n.domainID)
	return nil
}

// startMXLNMOS starts registration and the peer exchange. It runs after
// initAPI, which creates the peer poller registration asks for the role.
func (a *App) startMXLNMOS() {
	n := a.mxlNMOS
	if n == nil {
		return
	}
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
		go func() { defer a.bgWG.Done(); a.runMXLNMOSPeer(ctx) }()
	}
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
	if (cfg.PeerOutputVideoDef != "" || cfg.PeerOutputAudioDef != "") && a.cfg.EngineLabel == "" {
		return nil, errors.New("--mxl-nmos-peer-output-*-def needs --engine-label: only an engine of a pair has a peer")
	}
	domainID, err := readMXLDomainID(a.cfg.MXLDomain)
	if err != nil {
		// Without the id, auto cannot resolve and every connection that
		// names a domain is refused, but one naming only a flow works.
		slog.Warn("mxl nmos: MXL domain id unknown", "domain", a.cfg.MXLDomain, "error", err)
	}

	n := &mxlNMOS{
		cfg:         cfg,
		domainID:    domainID,
		store:       nmos.NewResourceStore(),
		receivers:   map[string]mxlReceiverRef{},
		bookings:    map[string]mxlBooking{},
		http:        &http.Client{Timeout: 5 * time.Second},
		forwardKick: make(chan struct{}, 1),
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
	if a.cfg.StateDir != "" {
		n.state.path = filepath.Join(a.cfg.StateDir, mxlNMOSStateFile)
	}
	n.state.load(n.receivers)
	return n, nil
}

// handler serves the Node API and the Connection API, marking requests the
// peer engine passed on.
func (n *mxlNMOS) handler(apiToken string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/x-nmos/node/", nmos.NewNodeServer(n.store).Handler())
	conn := n.conn.Handler()
	mux.Handle("/x-nmos/connection/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(peerForwardHeader) != "" && peerAuthorized(r, apiToken) {
			r = r.WithContext(context.WithValue(r.Context(), peerForwardKey{}, true))
		}
		conn.ServeHTTP(w, r)
	}))
	return mux
}

// peerAuthorized reports whether r carries the session API token both
// engines of a pair share. Without one configured no request counts as the
// peer's, and every activation is passed on; the pair still converges, as
// passing on an activation that changes nothing stops there.
func peerAuthorized(r *http.Request, apiToken string) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && apiToken != "" && subtle.ConstantTimeCompare([]byte(got), []byte(apiToken)) == 1
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
	// IS-04 has the device list its receivers as an array, an empty one
	// for a booking without named inputs; a registry refuses a null.
	rxIDs := []string{}
	for _, spec := range specs {
		n.bookings[spec.Name] = mxlBooking{Video: spec.Video, Audio: spec.Audio, Data: spec.Data}
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

	senderIDs, err := a.addMXLNMOSProgramSenders(n, deviceID)
	if err != nil {
		return err
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
		Senders:   senderIDs,
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

// addMXLNMOSProgramSenders exposes the program flows as MXL Sources, Flows
// and Senders, as BCP-007-03 requires of every MXL writer, so a controller
// can connect other receivers to the program. The flows are described by
// the same definitions the writers create them from.
//
// Each engine of a pair writes flows of its own, so both engines' programs
// are senders, each naming its own engine's flow, and either engine offers
// both: the sender a receiver was connected to keeps naming the flow that
// receiver reads whichever engine registers.
func (a *App) addMXLNMOSProgramSenders(n *mxlNMOS, deviceID string) ([]string, error) {
	type program struct {
		engine, video, audio string
	}
	var programs []program
	if a.cfg.MXLOutput != "" {
		programs = append(programs, program{a.cfg.EngineLabel, a.cfg.MXLOutputVideoDef, a.cfg.MXLOutputAudioDef})
	}
	if a.cfg.EngineLabel != "" {
		programs = append(programs, program{peerEngineLabel(a.cfg.EngineLabel), n.cfg.PeerOutputVideoDef, n.cfg.PeerOutputAudioDef})
	}
	senders := []string{}
	// Two senders of one flow would be two programs a controller cannot
	// tell apart, one of which nobody writes.
	written := map[string]string{}
	for _, p := range programs {
		for _, d := range []struct {
			essence mxlEssence
			path    string
			format  string
		}{
			{essenceVideo, p.video, nmos.FormatVideo},
			{essenceAudio, p.audio, nmos.FormatAudio},
		} {
			if d.path == "" {
				continue
			}
			key := programKey(p.engine)
			id, flowID, err := a.addMXLNMOSProgramSender(n, deviceID, key, d.essence, d.path, d.format)
			if err != nil {
				return nil, err
			}
			if other, ok := written[flowID]; ok {
				return nil, fmt.Errorf("mxl nmos: %s %s and %s name the same flow %s", key, d.essence, other, flowID)
			}
			written[flowID] = key + " " + string(d.essence)
			senders = append(senders, id)
		}
	}
	return senders, nil
}

// programKey seeds the ids of an engine's program resources. Engine a's
// are those of a single engine, which predate pairs.
func programKey(engine string) string {
	if engine == "b" {
		return "program-b"
	}
	return "program"
}

func peerEngineLabel(engine string) string {
	if engine == "a" {
		return "b"
	}
	return "a"
}

// addMXLNMOSProgramSender adds the Source, Flow and Sender of one program
// flow definition and returns the sender's id and the MXL flow it names.
func (a *App) addMXLNMOSProgramSender(n *mxlNMOS, deviceID, key string, essence mxlEssence, path, format string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("mxl nmos: %s %s flow definition: %w", key, essence, err)
	}
	var flow nmos.Flow
	if err := json.Unmarshal(data, &flow); err != nil {
		return "", "", fmt.Errorf("mxl nmos: %s %s flow definition %s: %w", key, essence, path, err)
	}
	var extra struct {
		ChannelCount int `json:"channel_count"`
	}
	_ = json.Unmarshal(data, &extra)
	// A rational's denominator defaults to 1 and is often left out.
	for _, r := range []*nmos.Rational{flow.GrainRate, flow.SampleRate} {
		if r != nil && r.Denominator == 0 {
			r.Denominator = 1
		}
	}
	mxlFlowID := flow.ID
	if mxlFlowID == "" {
		return "", "", fmt.Errorf("mxl nmos: %s %s flow definition %s has no id", key, essence, path)
	}
	// The IS-04 ids are the node's own, alike on both engines; the MXL flow
	// id is what the engine writes and what a receiver is connected to.
	sourceID := nmos.DeriveID(n.cfg.Seed, "source", key, string(essence))
	flow.ID = nmos.DeriveID(n.cfg.Seed, "flow", key, string(essence))
	flow.Version = nmos.NewVersion()
	flow.SourceID = sourceID
	flow.DeviceID = deviceID
	flow.Parents = []string{}
	flow.Format = format
	if flow.Tags == nil {
		flow.Tags = map[string][]string{}
	}
	src := &nmos.Source{
		ID:          sourceID,
		Version:     nmos.NewVersion(),
		Label:       flow.Label,
		Description: flow.Description,
		Tags:        flow.Tags,
		Caps:        map[string]interface{}{},
		Format:      format,
		Parents:     []string{},
		DeviceID:    deviceID,
		ClockName:   "clk0",
	}
	if format == nmos.FormatAudio {
		// IS-04 requires an audio source to list its channels; the
		// definition gives only their number.
		src.Channels = audioChannels(extra.ChannelCount)
	}
	n.store.AddSource(src)
	n.store.AddFlow(&flow)
	senderID := nmos.DeriveID(n.cfg.Seed, "sender", key, string(essence))
	version := nmos.NewVersion()
	n.store.AddSender(&nmos.Sender{
		ID:                senderID,
		Version:           version,
		Label:             flow.Label,
		Description:       flow.Description,
		Tags:              flow.Tags,
		FlowID:            flow.ID,
		Transport:         nmos.TransportMXL,
		DeviceID:          deviceID,
		InterfaceBindings: []string{},
		Caps:              nmos.SenderCaps(&flow, len(src.Channels), version),
		Subscription:      nmos.SenderSubscription{Active: true},
	})
	n.conn.AddSender(senderID, mxlFlowID)
	return senderID, mxlFlowID, nil
}

// audioChannels labels count channels, as a stereo pair where there are
// two; an unknown count is taken as the stereo the mixer writes.
func audioChannels(count int) []nmos.SourceChannel {
	if count <= 0 {
		count = 2
	}
	if count == 2 {
		return []nmos.SourceChannel{{Label: "Left", Symbol: "L"}, {Label: "Right", Symbol: "R"}}
	}
	ch := make([]nmos.SourceChannel, count)
	for i := range ch {
		ch[i] = nmos.SourceChannel{Label: fmt.Sprintf("Channel %d", i+1), Symbol: fmt.Sprintf("U%02d", i+1)}
	}
	return ch
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

// connectTimeout bounds how long an activation waits for a flow to open.
// A flow on another node is mirrored here first, which takes seconds.
const connectTimeout = 8 * time.Second

// activateMXLReceiver moves an input's reader to the activated flow, or off
// it, and publishes the new subscription. A flow that cannot be opened in
// time or has another shape fails the activation, which leaves the active
// state as it was.
func (a *App) activateMXLReceiver(ctx context.Context, id string, active nmos.MXLEndpoint) error {
	n := a.mxlNMOS
	ref, ok := n.receivers[id]
	if !ok {
		return fmt.Errorf("unknown receiver %s", id)
	}
	prev, _ := n.conn.ReceiverActive(id)
	if err := a.applyMXLReceiver(ctx, ref, active, connectTimeout); err != nil {
		return err
	}
	n.store.UpdateReceiverSubscription(id, active.SenderID, active.MasterEnable)
	n.saveMXLActivation(id, active)
	go a.publishMXLReceiver(id)
	// One that changes nothing is not passed on, which also ends the
	// round trip of an activation the peer could not mark as its own.
	if !fromPeer(ctx) && n.peerBase != "" && !sameConnection(prev, active) {
		n.queueForward(id, active)
	}
	return nil
}

// applyMXLReceiver connects the input's reader, waiting at most timeout for
// the flow to open, and, for video, sets the input's label to what the
// connection carries.
func (a *App) applyMXLReceiver(ctx context.Context, ref mxlReceiverRef, active nmos.MXLEndpoint, timeout time.Duration) error {
	flow := ""
	if active.MasterEnable {
		flow = active.FlowID()
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := a.connectMXLInput(cctx, ref.input, ref.essence, flow); err != nil {
		return fmt.Errorf("input %s %s: %w", ref.input, ref.essence, err)
	}
	if ref.essence == essenceVideo {
		seq := a.mxlNMOS.nextLabelSeq(ref.input)
		go a.labelMXLInput(ref.input, seq, active)
	}
	return nil
}

func (n *mxlNMOS) nextLabelSeq(input string) uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.labelSeq == nil {
		n.labelSeq = map[string]uint64{}
	}
	n.labelSeq[input]++
	return n.labelSeq[input]
}

func (n *mxlNMOS) latestLabelSeq(input string, seq uint64) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.labelSeq[input] == seq
}

// labelMXLInput shows the operator what a video connection carries: the
// connected sender's source label from the Query API, or the input's name
// when it is disconnected, names no sender or the lookup fails, so that a
// former connection's name is never shown for a new one. Of overlapping
// lookups for one input only the latest connection's sets the label.
func (a *App) labelMXLInput(input string, seq uint64, active nmos.MXLEndpoint) {
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
	n.labelMu.Lock()
	defer n.labelMu.Unlock()
	if !n.latestLabelSeq(input, seq) {
		return
	}
	if err := a.SetSourceLabel(context.Background(), "mxl:"+input, label); err != nil {
		slog.Warn("mxl nmos: could not label input", "input", input, "error", err)
	}
}

// queueForward records an activation for the peer engine. Only the latest
// activation of each receiver is sent, so the peer cannot apply two in the
// wrong order, and one that fails is retried until it lands.
func (n *mxlNMOS) queueForward(id string, active nmos.MXLEndpoint) {
	n.mu.Lock()
	if n.forward == nil {
		n.forward = map[string]nmos.MXLEndpoint{}
	}
	n.forward[id] = active
	n.mu.Unlock()
	select {
	case n.forwardKick <- struct{}{}:
	default:
	}
}

// runMXLNMOSPeer passes queued activations to the peer engine, retrying
// with backoff, and compares the two engines' receivers periodically, so a
// forward lost while both ran is caught too.
func (a *App) runMXLNMOSPeer(ctx context.Context) {
	n := a.mxlNMOS
	backoff := time.Second
	reconcile := time.NewTicker(mxlPeerReconcileInterval)
	defer reconcile.Stop()
	// An engine that starts beside a running peer takes over what was
	// connected there.
	_, _ = a.reconcileMXLNMOS(ctx)
	for {
		failed := a.sendForwards(ctx)
		wait := mxlPeerReconcileInterval
		if failed {
			wait = backoff
			backoff = min(backoff*2, 30*time.Second)
		} else {
			backoff = time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-n.forwardKick:
		case <-timer.C:
		case <-reconcile.C:
			if _, err := a.reconcileMXLNMOS(ctx); err != nil {
				slog.Debug("mxl nmos: peer not readable", "error", err)
			}
		}
		timer.Stop()
	}
}

// mxlPeerReconcileInterval is how often the engines compare receivers.
var mxlPeerReconcileInterval = 30 * time.Second

// sendForwards sends every queued activation, keeping those that fail.
func (a *App) sendForwards(ctx context.Context) (failed bool) {
	n := a.mxlNMOS
	n.mu.Lock()
	batch := make(map[string]nmos.MXLEndpoint, len(n.forward))
	for id, e := range n.forward {
		batch[id] = e
	}
	n.mu.Unlock()
	for id, e := range batch {
		if err := a.forwardMXLActivation(ctx, id, e); err != nil {
			slog.Warn("mxl nmos: activation not passed to peer yet", "receiver", id, "error", err)
			failed = true
			continue
		}
		n.mu.Lock()
		if cur, ok := n.forward[id]; ok && sameActivation(cur, e) {
			delete(n.forward, id)
		}
		n.mu.Unlock()
	}
	return failed
}

func sameActivation(a, b nmos.MXLEndpoint) bool {
	return a.MasterEnable == b.MasterEnable && a.FlowID() == b.FlowID() &&
		strPtrEq(a.SenderID, b.SenderID) && strPtrEq(a.Activation.ActivationTime, b.Activation.ActivationTime)
}

// sameConnection reports whether two receiver states connect the same
// thing, whenever they were activated.
func sameConnection(a, b nmos.MXLEndpoint) bool {
	if !a.MasterEnable && !b.MasterEnable {
		return true
	}
	return a.MasterEnable == b.MasterEnable && a.FlowID() == b.FlowID() && strPtrEq(a.SenderID, b.SenderID)
}

func strPtrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// forwardMXLActivation passes an activation to the peer engine, which
// applies it without passing it back. The session API token, which both
// engines share, vouches that the request comes from the peer.
func (a *App) forwardMXLActivation(ctx context.Context, id string, active nmos.MXLEndpoint) error {
	n := a.mxlNMOS
	leg := map[string]any{nmos.ParamMXLFlowID: active.TransportParams[0].FlowID}
	if active.TransportParams[0].FlowID != nil {
		leg[nmos.ParamMXLDomainID] = "auto"
	}
	body := map[string]any{
		"sender_id":        active.SenderID,
		"master_enable":    active.MasterEnable,
		"activation":       map[string]any{"mode": "activate_immediate"},
		"transport_params": []map[string]any{leg},
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, n.peerBase+"/x-nmos/connection/v1.1/single/receivers/"+id+"/staged", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(peerForwardHeader, "1")
	if a.cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.APIToken)
	}
	resp, err := n.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("peer answered %d: %s", resp.StatusCode, msg)
	}
	return nil
}

// reconcileMXLNMOS compares each receiver with the peer's and adopts the
// peer's where it connects something else and was activated later. The
// peer does the same, so the two converge on the latest activation either
// made. done is false when the peer could not be read.
func (a *App) reconcileMXLNMOS(ctx context.Context) (done bool, err error) {
	n := a.mxlNMOS
	for _, id := range n.order {
		peerActive, err := n.peerReceiverActive(ctx, id)
		if err != nil {
			return false, err
		}
		mine, _ := n.conn.ReceiverActive(id)
		if sameConnection(mine, peerActive) || !laterActivation(peerActive, mine) {
			continue
		}
		n.mu.Lock()
		_, queued := n.forward[id]
		n.mu.Unlock()
		if queued {
			// This engine's own activation is on its way to the peer.
			continue
		}
		peerActive.TransportParams[0].DomainID = nonEmptyPtr(n.domainID)
		if !peerActive.MasterEnable {
			peerActive.TransportParams[0].DomainID = nil
		}
		if err := a.applyMXLReceiver(ctx, n.receivers[id], peerActive, connectTimeout); err != nil {
			slog.Warn("mxl nmos: peer connection not adopted", "receiver", id, "error", err)
			continue
		}
		n.conn.SetReceiverActive(id, peerActive)
		n.store.UpdateReceiverSubscription(id, peerActive.SenderID, peerActive.MasterEnable)
		n.saveMXLActivation(id, peerActive)
		go a.publishMXLReceiver(id)
		slog.Info("mxl nmos: adopted peer connection", "receiver", id, "flow", peerActive.FlowID())
	}
	return true, nil
}

// laterActivation reports whether a was activated after b; a state never
// activated is the oldest.
func laterActivation(a, b nmos.MXLEndpoint) bool {
	ta, tb := a.Activation.ActivationTime, b.Activation.ActivationTime
	if ta == nil {
		return false
	}
	if tb == nil {
		return true
	}
	return nmos.CompareVersions(*ta, *tb) > 0
}

func (n *mxlNMOS) peerReceiverActive(ctx context.Context, id string) (nmos.MXLEndpoint, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.peerBase+"/x-nmos/connection/v1.1/single/receivers/"+id+"/active", nil)
	if err != nil {
		return nmos.MXLEndpoint{}, err
	}
	resp, err := n.http.Do(req)
	if err != nil {
		return nmos.MXLEndpoint{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var e nmos.MXLEndpoint
	if resp.StatusCode != http.StatusOK {
		return e, fmt.Errorf("peer receiver %s: status %d", id, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		return e, fmt.Errorf("peer receiver %s: %w", id, err)
	}
	return e, nil
}

// registersMXLNMOS reports whether this engine registers the node: a single
// engine always; of a pair the leader, and a follower that cannot reach the
// leader. The peer role gates commands a partition must not run twice, which
// a registration is not: both engines register the same ids, so a
// partition between two live engines leaves one node in the registry whose
// href is whichever engine registered last, while a follower that waited for
// a leader that is down would leave none.
func (a *App) registersMXLNMOS() bool {
	// An engine of a pair whose poller does not exist yet does not know its
	// role; a nil poller would read as standalone. One with no peer has no
	// pair and registers.
	if a.cfg.EngineLabel != "" && a.cfg.PeerEngineURL != "" && a.peerPoller == nil {
		return false
	}
	switch a.peerPoller.Role() {
	case peer.RoleStandalone, peer.RoleLeader, peer.RoleFollowerPartitioned:
		return true
	}
	return false
}

// mxlNMOSHeartbeatInterval is how often the registering engine heartbeats,
// and how often an engine checks whether it should register.
var mxlNMOSHeartbeatInterval = 5 * time.Second

// runMXLNMOSRegistration keeps the node registered while this engine
// registers, and leaves it to the peer otherwise. Both register the same
// ids, so one's registration replaces the other's, href included.
func (a *App) runMXLNMOSRegistration(ctx context.Context) {
	n := a.mxlNMOS
	tick := time.NewTicker(mxlNMOSHeartbeatInterval)
	defer tick.Stop()
	peerWasReachable := true
	queryFailing := false
	for {
		// A peer that was unreachable may have registered the node at its
		// own href meanwhile, and a heartbeat keeps the href as it is, so
		// the node is registered again once the peer is back; a follower
		// that reaches the leader then stays silent.
		if a.peerPoller != nil {
			reachable := a.peerPoller.PeerReachable()
			if reachable && !peerWasReachable {
				n.setRegistered(false)
			}
			peerWasReachable = reachable
		}
		n.mu.Lock()
		registered := n.registered
		n.mu.Unlock()
		switch {
		case !a.registersMXLNMOS():
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
				break
			}
			if !a.readsMXLNMOSBack() {
				break
			}
			held, err := a.mxlNMOSHeldByPeer(ctx)
			switch {
			case err != nil && !queryFailing:
				// Without the Query API a follower's registration is not
				// taken back, which nothing else shows.
				slog.Warn("mxl nmos: registered node not readable", "error", err)
			case err == nil && queryFailing:
				slog.Info("mxl nmos: registered node readable again")
			}
			queryFailing = err != nil
			if held {
				slog.Info("mxl nmos: registry holds the node at the peer's href, registering anew")
				n.setRegistered(false)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// readsMXLNMOSBack reports whether the engine reads its node back after a
// heartbeat. A follower that could not reach the leader registers at its
// own href, and its view of the peer need not change when the leader's
// does: a leader that restarts reaches the follower and registers before
// the follower notices it is back, and the follower's registration then
// lands last. The leader's heartbeats would keep that record alive, href
// and device controls naming the follower, for as long as both run. Only a
// leader that reaches its peer reads, so two engines that cannot reach
// each other and both take themselves for leader do not take the node from
// each other on every heartbeat.
func (a *App) readsMXLNMOSBack() bool {
	return a.mxlNMOS.query != nil && a.peerPoller.Role() == peer.RoleLeader && a.peerPoller.PeerReachable()
}

// mxlNMOSHeldByPeer reports whether the registry holds the node at another
// href than this engine's. The read has a budget of its own, as it runs
// between heartbeats: a Query API that does not answer would otherwise add
// the client's whole timeout to the cycle.
func (a *App) mxlNMOSHeldByPeer(ctx context.Context) (bool, error) {
	n := a.mxlNMOS
	ctx, cancel := context.WithTimeout(ctx, mxlNMOSQueryTimeout)
	defer cancel()
	node := n.store.GetNode()
	href, err := n.query.NodeHref(ctx, node.ID)
	if err != nil {
		return false, err
	}
	return href != node.Href, nil
}

// mxlNMOSQueryTimeout bounds the leader's read of its node, which runs
// between heartbeats.
var mxlNMOSQueryTimeout = 2 * time.Second

func (n *mxlNMOS) setRegistered(v bool) {
	n.mu.Lock()
	n.registered = v
	n.mu.Unlock()
}

func (a *App) registerMXLNMOS(ctx context.Context) error {
	n := a.mxlNMOS
	// New versions, so the registry takes these resources over the peer
	// engine's, href and controls included, whichever engine started first.
	n.store.Touch()
	node := n.store.GetNode()
	devices := n.store.ListDevices()
	if len(devices) != 1 {
		return fmt.Errorf("%d devices, want 1", len(devices))
	}
	var rx []*nmos.Receiver
	for _, id := range n.order {
		rx = append(rx, n.store.GetReceiver(id))
	}
	return n.reg.RegisterAll(ctx, node, devices[0], n.store.ListSources(), n.store.ListFlows(),
		n.store.ListSenders(), rx)
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
