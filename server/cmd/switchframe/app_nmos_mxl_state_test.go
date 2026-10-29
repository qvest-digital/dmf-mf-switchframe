package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/nmos"
)

const rebookedV = "d4d00000-0000-4000-8000-0000000000b1"

// newEngine is an engine that keeps its state in dir and was booked with
// sources; each call on the same dir is a restart of that engine.
func newEngine(t *testing.T, dir string, sources ...string) *nmosTestApp {
	t.Helper()
	tt := newNMOSTestApp(t, nil, func(a *App) {
		a.cfg.StateDir = dir
		if len(sources) > 0 {
			a.cfg.MXLSources = sources
		}
	})
	return tt
}

func (tt *nmosTestApp) active(t *testing.T, rx string) nmos.MXLEndpoint {
	t.Helper()
	var e nmos.MXLEndpoint
	tt.getJSON(t, "/x-nmos/connection/v1.1/single/receivers/"+rx+"/active", &e)
	return e
}

func strVal(s *string) string {
	if s == nil {
		return "<null>"
	}
	return *s
}

// A restart reconnects an input to what a controller connected it to, not
// to its booked flow, and the Connection API shows that connection, sender
// and activation included, so a controller sees what the input reads.
func TestMXLNMOS_RestartRestoresControllerConnection(t *testing.T) {
	dir := t.TempDir()
	tt := newEngine(t, dir)
	if code := tt.patch(t, rxID("cam1", "video"), activationBody(nmosFlow), false); code != 200 {
		t.Fatalf("PATCH: %d", code)
	}
	before := tt.active(t, rxID("cam1", "video"))

	re := newEngine(t, dir)
	re.app.restoreMXLNMOS(t.Context())
	if got := re.cam1V.FlowID(); got != nmosFlow {
		t.Fatalf("after a restart cam1 reads %q, want the controller's %s", got, nmosFlow)
	}
	got := re.active(t, rxID("cam1", "video"))
	if !got.MasterEnable || got.FlowID() != nmosFlow || strVal(got.SenderID) != nmosSender {
		t.Errorf("active after a restart: enable %v flow %s sender %s, want the controller's connection",
			got.MasterEnable, got.FlowID(), strVal(got.SenderID))
	}
	if strVal(got.Activation.ActivationTime) != strVal(before.Activation.ActivationTime) {
		t.Errorf("activation time %s, want the original %s", strVal(got.Activation.ActivationTime), strVal(before.Activation.ActivationTime))
	}
	// A flow mirrored from another node can take longer to open than a
	// controller's activation waits; given up on, it would be replaced by
	// the booking on both engines.
	if b := re.cam1V.budget; b <= connectTimeout {
		t.Errorf("restore waited at most %v for the flow, want longer than an activation's %v", b, connectTimeout)
	}
	if r := re.n.store.GetReceiver(rxID("cam1", "video")); r.Subscription.SenderID == nil || *r.Subscription.SenderID != nmosSender {
		t.Errorf("IS-04 subscription %+v, want the restored sender", r.Subscription)
	}
	// What no controller changed stays as booked.
	if a := re.active(t, rxID("cam1", "audio")); a.FlowID() != specA || a.SenderID != nil {
		t.Errorf("cam1 audio active %s sender %s, want the booked flow", a.FlowID(), strVal(a.SenderID))
	}
	// A restored connection is no new activation for the peer.
	re.app.sendForwards(t.Context())
	if n := re.peer.count(); n != 0 {
		t.Errorf("restored connection passed to the peer %d times", n)
	}
}

// A disconnection is a controller's connection like any other and survives
// a restart.
func TestMXLNMOS_RestartKeepsADisconnection(t *testing.T) {
	dir := t.TempDir()
	tt := newEngine(t, dir)
	if code := tt.patch(t, rxID("cam1", "video"), `{"master_enable": false, "activation": {"mode": "activate_immediate"}}`, false); code != 200 {
		t.Fatalf("PATCH: %d", code)
	}
	re := newEngine(t, dir)
	re.cam1V.Retarget(specV) // what initMXL does with the booking
	re.app.restoreMXLNMOS(t.Context())
	if got := re.cam1V.FlowID(); got != "" {
		t.Errorf("after a restart cam1 reads %q, want nothing", got)
	}
	if a := re.active(t, rxID("cam1", "video")); a.MasterEnable {
		t.Errorf("active after a restart %+v, want disabled", a)
	}
}

// An input booked anew since a controller connected it reads its new
// booking: the booking was made later. The booked flow becomes an activation
// the peer engine is given, and the saved connection is forgotten.
func TestMXLNMOS_RebookedInputDropsSavedConnection(t *testing.T) {
	dir := t.TempDir()
	tt := newEngine(t, dir)
	if code := tt.patch(t, rxID("cam1", "video"), activationBody(nmosFlow), false); code != 200 {
		t.Fatalf("PATCH: %d", code)
	}

	rebooked := []string{"cam1=" + rebookedV + ":" + specA, "cam2="}
	re := newEngine(t, dir, rebooked...)
	re.app.restoreMXLNMOS(t.Context())
	if slices.Contains(re.cam1V.ids, nmosFlow) {
		t.Errorf("re-booked cam1 was connected to the saved flow: %v", re.cam1V.ids)
	}
	got := re.active(t, rxID("cam1", "video"))
	if got.FlowID() != rebookedV || got.SenderID != nil {
		t.Errorf("active %s sender %s, want the new booking %s", got.FlowID(), strVal(got.SenderID), rebookedV)
	}
	if got.Activation.ActivationTime == nil {
		t.Error("the new booking shows no activation, so a peer still reading the saved connection keeps it")
	}
	if re.app.sendForwards(t.Context()) {
		t.Fatal("forward failed")
	}
	if re.peer.count() != 1 || !strings.Contains(re.peer.bodies[0], rebookedV) {
		t.Errorf("peer got %v, want the new booking", re.peer.bodies)
	}

	again := newEngine(t, dir, rebooked...)
	again.app.restoreMXLNMOS(t.Context())
	if n := len(again.cam1V.ids); n != 0 {
		t.Errorf("the dropped connection was restored on the next restart: %v", again.cam1V.ids)
	}
}

// A saved connection whose flow does not open, its producer gone, leaves the
// input on the booked flow, and the Connection API says so.
func TestMXLNMOS_UnopenableSavedFlowFallsBackToBooking(t *testing.T) {
	dir := t.TempDir()
	tt := newEngine(t, dir)
	if code := tt.patch(t, rxID("cam1", "video"), activationBody(nmosFlow), false); code != 200 {
		t.Fatalf("PATCH: %d", code)
	}

	re := newEngine(t, dir)
	re.cam1V.refuse = errors.New("flow not open: context deadline exceeded")
	re.app.restoreMXLNMOS(t.Context())
	if got := re.cam1V.FlowID(); got != specV {
		t.Errorf("cam1 reads %q, want the booked %s", got, specV)
	}
	got := re.active(t, rxID("cam1", "video"))
	if !got.MasterEnable || got.FlowID() != specV || got.SenderID != nil || got.Activation.ActivationTime == nil {
		t.Errorf("active enable %v flow %s sender %s activated %s, want the booked flow activated now",
			got.MasterEnable, got.FlowID(), strVal(got.SenderID), strVal(got.Activation.ActivationTime))
	}
	if r := re.n.store.GetReceiver(rxID("cam1", "video")); r.Subscription.SenderID != nil {
		t.Errorf("IS-04 subscription still names the saved sender: %+v", r.Subscription)
	}
	if re.app.sendForwards(t.Context()); re.peer.count() != 1 || !strings.Contains(re.peer.bodies[0], specV) {
		t.Errorf("peer got %v, want the booked flow", re.peer.bodies)
	}

	again := newEngine(t, dir)
	again.app.restoreMXLNMOS(t.Context())
	if n := len(again.cam1V.ids); n != 0 {
		t.Errorf("the dropped connection was tried again on the next restart: %v", again.cam1V.ids)
	}
}

// Saved connections of an input no longer booked are dropped, and an
// unreadable file leaves every input on its booking.
func TestMXLNMOS_SavedStateOfUnknownReceiversAndBadFile(t *testing.T) {
	dir := t.TempDir()
	tt := newEngine(t, dir)
	if code := tt.patch(t, rxID("cam2", "video"), activationBody(nmosFlow), false); code != 200 {
		t.Fatalf("PATCH: %d", code)
	}
	newEngine(t, dir, "cam1="+specV+":"+specA)
	data, err := os.ReadFile(filepath.Join(dir, mxlNMOSStateFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), rxID("cam2", "video")) {
		t.Errorf("connection of an input no longer booked kept: %s", data)
	}

	if err := os.WriteFile(filepath.Join(dir, mxlNMOSStateFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	re := newEngine(t, dir)
	re.app.restoreMXLNMOS(t.Context())
	if a := re.active(t, rxID("cam1", "video")); a.FlowID() != specV {
		t.Errorf("active %s with an unreadable file, want the booked flow", a.FlowID())
	}
}

// A state file holding JSON null is no saved connection, and the engine
// still saves the next one rather than panicking on it.
func TestMXLNMOS_NullStateFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, mxlNMOSStateFile), []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	tt := newEngine(t, dir)
	tt.app.restoreMXLNMOS(t.Context())
	if a := tt.active(t, rxID("cam1", "video")); a.FlowID() != specV {
		t.Errorf("active %s with a null file, want the booked flow", a.FlowID())
	}
	if code := tt.patch(t, rxID("cam2", "video"), activationBody(nmosFlow), false); code != 200 {
		t.Fatalf("PATCH after a null file: %d", code)
	}
	re := newEngine(t, dir)
	re.app.restoreMXLNMOS(t.Context())
	if got := re.cam2V.FlowID(); got != nmosFlow {
		t.Errorf("after a restart cam2 reads %q, want the connection saved after the null file", got)
	}
}

// A controller's activation that arrives while a reconcile adoption of the
// same receiver is being applied waits for it and then replaces it, so the
// activation answered last is the one the reader, /active and the saved
// state name: answered 200 and then overwritten by the adoption, a restart
// would restore the peer's connection and /active would deny the
// controller's.
func TestMXLNMOS_ActivationDuringAdoptionWins(t *testing.T) {
	const peerFlow = "3494dc19-22c2-4f00-865b-cdbeee7909f0"
	dir := t.TempDir()
	tt := newEngine(t, dir)
	rx := rxID("cam2", "video")
	tt.peer.mu.Lock()
	tt.peer.active[rx] = `{"sender_id":"` + nmosSender + `","master_enable":true,
		"activation":{"mode":"activate_immediate","requested_time":null,"activation_time":"1755192060:38206854"},
		"transport_file":{"data":null,"type":null},
		"transport_params":[{"mxl_domain_id":"` + nmosDomain + `","mxl_flow_id":"` + peerFlow + `"}]}`
	tt.peer.mu.Unlock()
	patched := make(chan int, 1)
	tt.cam2V.onConnect = func(id string) {
		if id != peerFlow {
			return
		}
		// Forwarded, so nothing is queued for the peer and reconcile does
		// not stand back for it.
		go func() { patched <- tt.patch(t, rx, activationBody(nmosFlow), true) }()
		select {
		case <-patched:
			patched <- http.StatusOK
		case <-time.After(300 * time.Millisecond):
		}
	}
	if _, err := tt.app.reconcileMXLNMOS(t.Context()); err != nil {
		t.Fatal(err)
	}
	if code := <-patched; code != http.StatusOK {
		t.Fatalf("PATCH: %d", code)
	}
	data, err := os.ReadFile(filepath.Join(dir, mxlNMOSStateFile))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]savedActivation
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	reader, active, file := tt.cam2V.FlowID(), tt.active(t, rx).FlowID(), saved[rx].Active.FlowID()
	if reader != nmosFlow || active != nmosFlow || file != nmosFlow {
		t.Errorf("reader %s, /active %s, saved %s; want the controller's %s, activated last", reader, active, file, nmosFlow)
	}
}

// pair wires two engines to each other, as the peer base of each.
func pair(a, b *nmosTestApp) {
	a.n.peerBase = b.srv.URL
	b.n.peerBase = a.srv.URL
}

const unreachablePeer = "http://127.0.0.1:1"

// Both engines of a pair read the controller's connection after each was
// restarted while the other was down, so neither could take it over from
// the other.
func TestMXLNMOS_PairKeepsTheConnectionAcrossRestarts(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	a, b := newEngine(t, dirA), newEngine(t, dirB)
	pair(a, b)
	if code := a.patch(t, rxID("cam2", "video"), activationBody(nmosFlow), false); code != 200 {
		t.Fatalf("PATCH: %d", code)
	}
	if a.app.sendForwards(t.Context()) {
		t.Fatal("forward to engine b failed")
	}
	if got := b.cam2V.FlowID(); got != nmosFlow {
		t.Fatalf("engine b reads %q before any restart", got)
	}

	// Engine b restarts while a is down.
	b2 := newEngine(t, dirB)
	b2.n.peerBase = unreachablePeer
	b2.app.restoreMXLNMOS(t.Context())
	if done, _ := b2.app.reconcileMXLNMOS(t.Context()); done {
		t.Fatal("reconciled with a peer that is down")
	}
	// Engine a restarts beside b.
	a2 := newEngine(t, dirA)
	pair(a2, b2)
	a2.app.restoreMXLNMOS(t.Context())
	if done, err := a2.app.reconcileMXLNMOS(t.Context()); !done {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := b2.app.reconcileMXLNMOS(t.Context()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for name, e := range map[string]*nmosTestApp{"a": a2, "b": b2} {
		if got := e.cam2V.FlowID(); got != nmosFlow {
			t.Errorf("engine %s reads %q, want %s", name, got, nmosFlow)
		}
		if act := e.active(t, rxID("cam2", "video")); act.FlowID() != nmosFlow || strVal(act.SenderID) != nmosSender {
			t.Errorf("engine %s active flow %s sender %s", name, act.FlowID(), strVal(act.SenderID))
		}
	}
}

// An engine restarted with a new booking beside a peer still reading the
// controller's former connection does not take that connection over; the
// peer takes the booking, and both read the booked flow.
func TestMXLNMOS_PairFollowsANewBooking(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	a, b := newEngine(t, dirA), newEngine(t, dirB)
	pair(a, b)
	if code := a.patch(t, rxID("cam1", "video"), activationBody(nmosFlow), false); code != 200 {
		t.Fatalf("PATCH: %d", code)
	}
	if a.app.sendForwards(t.Context()) {
		t.Fatal("forward to engine b failed")
	}

	a2 := newEngine(t, dirA, "cam1="+rebookedV+":"+specA, "cam2=")
	a2.cam1V.Retarget(rebookedV) // what initMXL does with the booking
	pair(a2, b)
	a2.app.restoreMXLNMOS(t.Context())
	// runMXLNMOSPeer reconciles first, then sends what is queued.
	if done, err := a2.app.reconcileMXLNMOS(t.Context()); !done {
		t.Fatalf("reconcile: %v", err)
	}
	if a2.app.sendForwards(t.Context()) {
		t.Fatal("forward to engine b failed")
	}
	if _, err := b.app.reconcileMXLNMOS(t.Context()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if done, _ := a2.app.reconcileMXLNMOS(t.Context()); !done {
		t.Fatal("reconcile failed")
	}
	for name, e := range map[string]*nmosTestApp{"a": a2, "b": b} {
		if got := e.cam1V.FlowID(); got != rebookedV {
			t.Errorf("engine %s reads %q, want the new booking %s", name, got, rebookedV)
		}
	}
}
