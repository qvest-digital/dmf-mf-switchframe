package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/nmos"
	"github.com/zsiec/switchframe/server/switcher"
)

const (
	nmosDomain = "1ac254d9-a5eb-475f-a2b6-3d02a5cfbc82"
	nmosFlow   = "4c02e7c0-ffeb-4595-8673-a265d103123a"
	nmosSender = "f68987c7-80fa-4b72-b299-00e66e1b1a41"
	nmosSeed   = "p-show/switcher"
)

type lockedRetargetable struct {
	mu  sync.Mutex
	ids []string
}

func (f *lockedRetargetable) Retarget(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, id)
}

func (f *lockedRetargetable) FlowID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ids) == 0 {
		return ""
	}
	return f.ids[len(f.ids)-1]
}

// peerRecorder stands in for the other engine of a pair.
type peerRecorder struct {
	mu      sync.Mutex
	patches []*http.Request
	bodies  []string
	active  map[string]string // receiver id -> /active body
}

func (p *peerRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Method == http.MethodPatch {
		b, _ := io.ReadAll(r.Body)
		p.patches = append(p.patches, r)
		p.bodies = append(p.bodies, string(b))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
		return
	}
	for id, body := range p.active {
		if strings.Contains(r.URL.Path, id) {
			_, _ = w.Write([]byte(body))
			return
		}
	}
	_, _ = w.Write([]byte(`{"sender_id":null,"master_enable":false,"activation":{"mode":null,"requested_time":null,"activation_time":null},"transport_file":{"data":null,"type":null},"transport_params":[{"mxl_domain_id":null,"mxl_flow_id":null}]}`))
}

func (p *peerRecorder) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.patches)
}

type nmosTestApp struct {
	app   *App
	n     *mxlNMOS
	srv   *httptest.Server
	peer  *peerRecorder
	cam1V *lockedRetargetable
	cam2V *lockedRetargetable
}

func newNMOSTestApp(t *testing.T, query http.Handler, setup ...func(a *App)) *nmosTestApp {
	t.Helper()
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)

	domain := t.TempDir()
	if err := os.WriteFile(filepath.Join(domain, "domain_def.json"), []byte(`{"id":"`+nmosDomain+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	peer := &peerRecorder{active: map[string]string{}}
	peerSrv := httptest.NewServer(peer)
	t.Cleanup(peerSrv.Close)

	a := &App{sw: sw}
	a.cfg.MXLDomain = domain
	a.cfg.MXLSources = []string{"cam1=" + specV + ":" + specA, "cam2="}
	a.cfg.MXLNMOS = mxlNMOSConfig{Port: 1, Seed: nmosSeed, Label: "switcher", Host: "switcher-a.p-show"}
	if query != nil {
		qs := httptest.NewServer(query)
		t.Cleanup(qs.Close)
		a.cfg.MXLNMOS.QueryURL = qs.URL
	}
	tt := &nmosTestApp{app: a, peer: peer, cam1V: &lockedRetargetable{}, cam2V: &lockedRetargetable{}}
	a.namedMXLInputs.add("cam1", &mxlInput{Video: tt.cam1V, Audio: &lockedRetargetable{}})
	a.namedMXLInputs.add("cam2", &mxlInput{Video: tt.cam2V, Audio: &lockedRetargetable{}})
	sw.RegisterMXLSourceLabeled("mxl:cam1", "cam1")
	sw.RegisterMXLSourceLabeled("mxl:cam2", "cam2")

	for _, f := range setup {
		f(a)
	}
	n, err := a.newMXLNMOS()
	if err != nil {
		t.Fatal(err)
	}
	// The peer listens on the node port; point at the recorder instead.
	n.peerBase = peerSrv.URL
	a.mxlNMOS = n
	tt.n = n
	tt.srv = httptest.NewServer(n.handler())
	t.Cleanup(tt.srv.Close)
	return tt
}

func (tt *nmosTestApp) getJSON(t *testing.T, path string, out any) {
	t.Helper()
	resp, err := http.Get(tt.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func (tt *nmosTestApp) patch(t *testing.T, rx, body string, forwarded bool) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, tt.srv.URL+"/x-nmos/connection/v1.1/single/receivers/"+rx+"/staged", strings.NewReader(body))
	if forwarded {
		req.Header.Set(peerForwardHeader, "1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func rxID(input, essence string) string { return nmos.DeriveID(nmosSeed, "receiver", input, essence) }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Each named input is a group of MXL receivers a controller can find and
// filter by what the switcher can take; an input booked with flows shows
// them as connected.
func TestMXLNMOS_ReceiversPerNamedInput(t *testing.T) {
	tt := newNMOSTestApp(t, nil)
	var rx []nmos.Receiver
	tt.getJSON(t, "/x-nmos/node/v1.3/receivers", &rx)
	if len(rx) != 4 {
		t.Fatalf("%d receivers, want video and audio for cam1 and cam2", len(rx))
	}
	byID := map[string]nmos.Receiver{}
	for _, r := range rx {
		byID[r.ID] = r
		if r.Transport != nmos.TransportMXL || len(r.InterfaceBindings) != 0 {
			t.Errorf("%s: transport %s, bindings %v", r.Label, r.Transport, r.InterfaceBindings)
		}
	}
	v, ok := byID[rxID("cam2", "video")]
	if !ok {
		t.Fatal("no cam2 video receiver under the derived id")
	}
	if got := v.Tags[nmos.GroupHintTag]; len(got) != 1 || got[0] != "cam2:Video" {
		t.Errorf("group hint %v", got)
	}
	pf := tt.app.sw.PipelineFormat()
	cs := v.Caps.ConstraintSets[0]
	if w := cs[nmos.CapFrameWidth].(map[string]any)["enum"].([]any)[0]; w != float64(pf.Width) {
		t.Errorf("frame_width %v, want the pipeline's %d", w, pf.Width)
	}

	var active map[string]any
	tt.getJSON(t, "/x-nmos/connection/v1.1/single/receivers/"+rxID("cam1", "video")+"/active", &active)
	leg := active["transport_params"].([]any)[0].(map[string]any)
	if active["master_enable"] != true || leg["mxl_flow_id"] != specV || leg["mxl_domain_id"] != nmosDomain {
		t.Errorf("cam1 video active %v, want the booked flow", active)
	}
	tt.getJSON(t, "/x-nmos/connection/v1.1/single/receivers/"+rxID("cam2", "video")+"/active", &active)
	if active["master_enable"] != false {
		t.Errorf("cam2 was booked without flows but reads as connected: %v", active)
	}
}

// A controller's connection moves the input's reader, labels the input
// after what is connected, publishes the subscription and reaches the peer
// engine.
func TestMXLNMOS_ControllerConnectsInput(t *testing.T) {
	const q = "/x-nmos/query/v1.3/"
	query := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case q + "senders/" + nmosSender:
			_, _ = w.Write([]byte(`{"label":"writer video","flow_id":"fl1"}`))
		case q + "flows/fl1":
			_, _ = w.Write([]byte(`{"label":"","source_id":"so1"}`))
		case q + "sources/so1":
			_, _ = w.Write([]byte(`{"label":"Studio Camera 3"}`))
		default:
			http.NotFound(w, r)
		}
	})
	tt := newNMOSTestApp(t, query)
	code := tt.patch(t, rxID("cam2", "video"), `{
		"sender_id": "`+nmosSender+`", "master_enable": true,
		"activation": {"mode": "activate_immediate"},
		"transport_params": [{"mxl_domain_id": "auto", "mxl_flow_id": "`+nmosFlow+`"}]}`, false)
	if code != http.StatusOK {
		t.Fatalf("PATCH: %d", code)
	}
	if got := tt.cam2V.FlowID(); got != nmosFlow {
		t.Errorf("cam2 reads %q, want %s", got, nmosFlow)
	}
	if n := len(tt.cam1V.ids); n != 0 {
		t.Errorf("cam1 was retargeted %d times by a connection to cam2", n)
	}
	waitFor(t, "input not labelled after the connected source", func() bool {
		for _, s := range tt.app.sw.State().Sources {
			if s.Key == "mxl:cam2" {
				return s.Label == "Studio Camera 3"
			}
		}
		return false
	})
	if r := tt.n.store.GetReceiver(rxID("cam2", "video")); !r.Subscription.Active || r.Subscription.SenderID == nil || *r.Subscription.SenderID != nmosSender {
		t.Errorf("IS-04 subscription %+v", r.Subscription)
	}
	waitFor(t, "activation not passed to the peer", func() bool { return tt.peer.count() == 1 })
	if tt.peer.patches[0].Header.Get(peerForwardHeader) == "" {
		t.Error("forwarded activation is not marked as such")
	}
	if !strings.Contains(tt.peer.bodies[0], nmosFlow) {
		t.Errorf("forwarded body %s lacks the flow", tt.peer.bodies[0])
	}

	// Disconnecting reads nothing and gives the input its name back.
	if code := tt.patch(t, rxID("cam2", "video"), `{"master_enable": false, "activation": {"mode": "activate_immediate"}}`, false); code != http.StatusOK {
		t.Fatalf("disconnect: %d", code)
	}
	if got := tt.cam2V.FlowID(); got != "" {
		t.Errorf("cam2 still reads %q", got)
	}
	waitFor(t, "label not reset on disconnect", func() bool {
		for _, s := range tt.app.sw.State().Sources {
			if s.Key == "mxl:cam2" {
				return s.Label == "cam2"
			}
		}
		return false
	})
}

// An activation the peer passed on is applied and not passed back, or the
// two engines would bounce it between them.
func TestMXLNMOS_ForwardedActivationIsNotForwardedBack(t *testing.T) {
	tt := newNMOSTestApp(t, nil)
	code := tt.patch(t, rxID("cam2", "video"), `{"master_enable": true,
		"activation": {"mode": "activate_immediate"},
		"transport_params": [{"mxl_domain_id": "auto", "mxl_flow_id": "`+nmosFlow+`"}]}`, true)
	if code != http.StatusOK {
		t.Fatalf("PATCH: %d", code)
	}
	if got := tt.cam2V.FlowID(); got != nmosFlow {
		t.Errorf("forwarded activation not applied: %q", got)
	}
	time.Sleep(100 * time.Millisecond)
	if n := tt.peer.count(); n != 0 {
		t.Errorf("forwarded activation passed back %d times", n)
	}
}

// An engine starting beside a running peer takes over what a controller
// connected there, and only that.
func TestMXLNMOS_CatchUpAdoptsPeerConnections(t *testing.T) {
	tt := newNMOSTestApp(t, nil)
	tt.peer.mu.Lock()
	tt.peer.active[rxID("cam2", "video")] = `{"sender_id":"` + nmosSender + `","master_enable":true,
		"activation":{"mode":"activate_immediate","requested_time":null,"activation_time":"1755192060:38206854"},
		"transport_file":{"data":null,"type":null},
		"transport_params":[{"mxl_domain_id":"` + nmosDomain + `","mxl_flow_id":"` + nmosFlow + `"}]}`
	tt.peer.mu.Unlock()

	done, err := tt.app.catchUpMXLNMOSOnce(t.Context())
	if !done || err != nil {
		t.Fatalf("catch-up: %v %v", done, err)
	}
	if got := tt.cam2V.FlowID(); got != nmosFlow {
		t.Errorf("cam2 reads %q, want the peer's connection", got)
	}
	if n := len(tt.cam1V.ids); n != 0 {
		t.Errorf("cam1, not connected by a controller on the peer, was retargeted %d times", n)
	}
	a, _ := tt.n.conn.ReceiverActive(rxID("cam2", "video"))
	if a.FlowID() != nmosFlow || a.SenderID == nil {
		t.Errorf("active state not adopted: %+v", a)
	}
}

// The program a booking writes is an MXL Sender a controller can connect
// other receivers to, with the flow this engine writes and no transport
// file.
func TestMXLNMOS_ProgramSenders(t *testing.T) {
	const pgmVideo = "5f000000-0000-4000-8000-0000000000a1"
	const pgmAudio = "5f000000-0000-4000-8000-0000000000a2"
	dir := t.TempDir()
	videoDef := filepath.Join(dir, "program-video.json")
	audioDef := filepath.Join(dir, "program-audio.json")
	_ = os.WriteFile(videoDef, []byte(`{"id":"`+pgmVideo+`","label":"switcher program video",
		"format":"urn:x-nmos:format:video","media_type":"video/v210",
		"tags":{"urn:x-nmos:tag:grouphint/v1.0":["switcher program:Video"]},
		"grain_rate":{"numerator":50,"denominator":1},"frame_width":1920,"frame_height":1080}`), 0o644)
	_ = os.WriteFile(audioDef, []byte(`{"id":"`+pgmAudio+`","label":"switcher program audio",
		"format":"urn:x-nmos:format:audio","media_type":"audio/float32",
		"sample_rate":{"numerator":48000,"denominator":1}}`), 0o644)
	tt := newNMOSTestApp(t, nil, func(a *App) {
		a.cfg.MXLOutput = "program"
		a.cfg.MXLOutputVideoDef = videoDef
		a.cfg.MXLOutputAudioDef = audioDef
	})

	var senders []map[string]any
	tt.getJSON(t, "/x-nmos/node/v1.3/senders", &senders)
	if len(senders) != 2 {
		t.Fatalf("%d senders, want program video and audio", len(senders))
	}
	var flows []nmos.Flow
	tt.getJSON(t, "/x-nmos/node/v1.3/flows", &flows)
	flowByID := map[string]nmos.Flow{}
	for _, f := range flows {
		flowByID[f.ID] = f
	}
	for _, snd := range senders {
		if snd["transport"] != nmos.TransportMXL {
			t.Errorf("sender transport %v", snd["transport"])
		}
		if v, ok := snd["manifest_href"]; !ok || v != nil {
			t.Errorf("manifest_href %v, want present and null", v)
		}
		f, ok := flowByID[snd["flow_id"].(string)]
		if !ok {
			t.Fatalf("sender's flow %v is not on the node", snd["flow_id"])
		}
		if f.ID == pgmVideo || f.ID == pgmAudio {
			t.Error("IS-04 flow id is the MXL flow id, which differs between engines")
		}
		var active map[string]any
		tt.getJSON(t, "/x-nmos/connection/v1.1/single/senders/"+snd["id"].(string)+"/active", &active)
		leg := active["transport_params"].([]any)[0].(map[string]any)
		want := pgmAudio
		if f.Format == nmos.FormatVideo {
			want = pgmVideo
			if f.FrameWidth != 1920 || f.GrainRate == nil || f.GrainRate.Numerator != 50 {
				t.Errorf("video flow %+v lost the definition's format", f)
			}
		}
		if leg["mxl_flow_id"] != want {
			t.Errorf("sender writes %v, want %s", leg["mxl_flow_id"], want)
		}
	}
	resp, err := http.Get(tt.srv.URL + "/x-nmos/connection/v1.1/single/senders/" + senders[0]["id"].(string) + "/transportfile")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("transportfile: %d, want 404", resp.StatusCode)
	}
}

// A named input reads as unconnected while no video flow is connected, so
// the UI does not offer it.
func TestMXLInputUnconnected(t *testing.T) {
	a := &App{}
	connected, idle := &lockedRetargetable{}, &lockedRetargetable{}
	connected.Retarget(specV)
	a.namedMXLInputs.add("cam1", &mxlInput{Video: connected, Audio: &lockedRetargetable{}})
	a.namedMXLInputs.add("cam2", &mxlInput{Video: idle, Audio: &lockedRetargetable{}})

	for key, want := range map[string]bool{
		"mxl:cam1":     false,
		"mxl:cam2":     true,
		"mxl:" + specA: false, // keyed by its flow: always connected
		"srt:cam2":     false,
	} {
		if got := a.mxlInputUnconnected(key); got != want {
			t.Errorf("%s: unconnected %v, want %v", key, got, want)
		}
	}
	connected.Retarget("")
	if !a.mxlInputUnconnected("mxl:cam1") {
		t.Error("cam1 disconnected but not marked unconnected")
	}
}
