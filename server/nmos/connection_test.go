package nmos

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestConnectionRoot(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var paths []string
	if err := json.NewDecoder(resp.Body).Decode(&paths); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(paths) != 1 || paths[0] != "single/" {
		t.Errorf("paths: got %v, want [\"single/\"]", paths)
	}
}

func TestConnectionSingleRoot(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/")
	if err != nil {
		t.Fatalf("GET /single/: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var paths []string
	if err := json.NewDecoder(resp.Body).Decode(&paths); err != nil {
		t.Fatalf("decode: %v", err)
	}

	expected := map[string]bool{"senders/": true, "receivers/": true}
	if len(paths) != 2 {
		t.Fatalf("paths length: got %d, want 2", len(paths))
	}
	for _, p := range paths {
		if !expected[p] {
			t.Errorf("unexpected path %q", p)
		}
	}
}

func TestConnectionListSenders(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddSender("sender-001", "v=0\r\n")
	cs.AddSender("sender-002", "v=0\r\n")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/senders/")
	if err != nil {
		t.Fatalf("GET senders: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var ids []string
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(ids) != 2 {
		t.Fatalf("sender count: got %d, want 2", len(ids))
	}

	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}
	if !idSet["sender-001"] || !idSet["sender-002"] {
		t.Errorf("expected sender-001 and sender-002 in %v", ids)
	}
}

func TestConnectionListReceivers(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddReceiver("recv-001")
	cs.AddReceiver("recv-002")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/")
	if err != nil {
		t.Fatalf("GET receivers: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var ids []string
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(ids) != 2 {
		t.Fatalf("receiver count: got %d, want 2", len(ids))
	}

	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}
	if !idSet["recv-001"] || !idSet["recv-002"] {
		t.Errorf("expected recv-001 and recv-002 in %v", ids)
	}
}

func TestConnectionSenderStaged(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddSender("sender-001", "v=0\r\n")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/senders/sender-001/staged")
	if err != nil {
		t.Fatalf("GET staged: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var staged StagedParams
	if err := json.NewDecoder(resp.Body).Decode(&staged); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Default staged should have empty transport_params.
	if staged.TransportParams == nil {
		t.Error("transport_params should not be nil")
	}
}

func TestConnectionReceiverStaged(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/recv-001/staged")
	if err != nil {
		t.Fatalf("GET staged: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var staged StagedParams
	if err := json.NewDecoder(resp.Body).Decode(&staged); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if staged.TransportParams == nil {
		t.Error("transport_params should not be nil")
	}
}

func TestConnectionPatchReceiverStaged(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	destIP := "239.1.1.1"
	destPort := 5004
	patch := StagedParams{
		TransportParams: []TransportParams{
			{
				DestinationIP:   &destIP,
				DestinationPort: &destPort,
			},
		},
	}

	body, _ := json.Marshal(patch)
	req, err := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/receivers/recv-001/staged", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var staged StagedParams
	if err := json.NewDecoder(resp.Body).Decode(&staged); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(staged.TransportParams) == 0 {
		t.Fatal("transport_params should have at least one entry")
	}

	tp := staged.TransportParams[0]
	if tp.DestinationIP == nil || *tp.DestinationIP != destIP {
		t.Errorf("destination_ip: got %v, want %q", tp.DestinationIP, destIP)
	}
	if tp.DestinationPort == nil || *tp.DestinationPort != destPort {
		t.Errorf("destination_port: got %v, want %d", tp.DestinationPort, destPort)
	}

	// Verify subsequent GET returns merged result.
	resp2, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/recv-001/staged")
	if err != nil {
		t.Fatalf("GET staged: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	var staged2 StagedParams
	if err := json.NewDecoder(resp2.Body).Decode(&staged2); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(staged2.TransportParams) == 0 || staged2.TransportParams[0].DestinationIP == nil {
		t.Error("merge not persisted")
	}
	if *staged2.TransportParams[0].DestinationIP != destIP {
		t.Errorf("persisted destination_ip: got %q, want %q", *staged2.TransportParams[0].DestinationIP, destIP)
	}
}

func TestConnectionActivateImmediate(t *testing.T) {
	var activatedMu sync.Mutex
	var activatedID string
	var activatedParams StagedParams

	cs := NewConnectionServer(ConnectionServerOpts{
		OnActivateReceiver: func(id string, params StagedParams) error {
			activatedMu.Lock()
			defer activatedMu.Unlock()
			activatedID = id
			activatedParams = params
			return nil
		},
	})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	destIP := "239.1.1.1"
	destPort := 5004
	rtpEnabled := true
	masterEnable := true
	mode := "activate_immediate"
	patch := StagedParams{
		MasterEnable: &masterEnable,
		TransportParams: []TransportParams{
			{
				DestinationIP:   &destIP,
				DestinationPort: &destPort,
				RTPEnabled:      &rtpEnabled,
			},
		},
		Activation: &Activation{
			Mode: &mode,
		},
	}

	body, _ := json.Marshal(patch)
	req, err := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/receivers/recv-001/staged", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: got %d, want 200 or 202", resp.StatusCode)
	}

	// Verify callback was invoked.
	activatedMu.Lock()
	if activatedID != "recv-001" {
		t.Errorf("activated ID: got %q, want %q", activatedID, "recv-001")
	}
	if len(activatedParams.TransportParams) == 0 {
		t.Error("activated params should have transport_params")
	}
	activatedMu.Unlock()

	// Verify active now equals staged.
	resp2, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/recv-001/active")
	if err != nil {
		t.Fatalf("GET active: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	var active StagedParams
	if err := json.NewDecoder(resp2.Body).Decode(&active); err != nil {
		t.Fatalf("decode active: %v", err)
	}

	if len(active.TransportParams) == 0 {
		t.Fatal("active transport_params should not be empty after activation")
	}
	if active.TransportParams[0].DestinationIP == nil || *active.TransportParams[0].DestinationIP != destIP {
		t.Errorf("active destination_ip: got %v, want %q", active.TransportParams[0].DestinationIP, destIP)
	}

	// Verify activation_time was set.
	if active.Activation == nil {
		t.Fatal("active activation should not be nil")
	}
	if active.Activation.ActivationTime == nil || *active.Activation.ActivationTime == "" {
		t.Error("activation_time should be set after activate_immediate")
	}
}

func TestConnectionActivateImmediateDeactivate(t *testing.T) {
	var deactivatedID string

	cs := NewConnectionServer(ConnectionServerOpts{
		OnDeactivateReceiver: func(id string) error {
			deactivatedID = id
			return nil
		},
	})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	masterEnable := false
	mode := "activate_immediate"
	patch := StagedParams{
		MasterEnable: &masterEnable,
		TransportParams: []TransportParams{
			{},
		},
		Activation: &Activation{
			Mode: &mode,
		},
	}

	body, _ := json.Marshal(patch)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/receivers/recv-001/staged", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if deactivatedID != "recv-001" {
		t.Errorf("deactivated ID: got %q, want %q", deactivatedID, "recv-001")
	}
}

func TestConnectionSenderTransportFile(t *testing.T) {
	sdp := "v=0\r\no=- 0 0 IN IP4 192.168.1.1\r\ns=Test\r\n"
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddSender("sender-001", sdp)
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/senders/sender-001/transportfile")
	if err != nil {
		t.Fatalf("GET transportfile: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/sdp" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/sdp")
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(data) != sdp {
		t.Errorf("SDP body: got %q, want %q", string(data), sdp)
	}
}

func TestConnectionSenderTransportType(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddSender("sender-001", "v=0\r\n")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/senders/sender-001/transporttype")
	if err != nil {
		t.Fatalf("GET transporttype: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var tt string
	if err := json.NewDecoder(resp.Body).Decode(&tt); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if tt != TransportRTPMcast {
		t.Errorf("transport type: got %q, want %q", tt, TransportRTPMcast)
	}
}

func TestConnection404(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	endpoints := []string{
		"/x-nmos/connection/v1.1/single/senders/unknown/staged",
		"/x-nmos/connection/v1.1/single/senders/unknown/active",
		"/x-nmos/connection/v1.1/single/senders/unknown/transportfile",
		"/x-nmos/connection/v1.1/single/senders/unknown/transporttype",
		"/x-nmos/connection/v1.1/single/senders/unknown/constraints",
		"/x-nmos/connection/v1.1/single/receivers/unknown/staged",
		"/x-nmos/connection/v1.1/single/receivers/unknown/active",
		"/x-nmos/connection/v1.1/single/receivers/unknown/transporttype",
		"/x-nmos/connection/v1.1/single/receivers/unknown/constraints",
	}

	for _, ep := range endpoints {
		resp, err := http.Get(ts.URL + ep)
		if err != nil {
			t.Fatalf("GET %s: %v", ep, err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", ep, resp.StatusCode)
		}
	}
}

func TestConnectionMethodNotAllowed(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddSender("sender-001", "v=0\r\n")
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	stagedEndpoints := []string{
		"/x-nmos/connection/v1.1/single/senders/sender-001/staged",
		"/x-nmos/connection/v1.1/single/receivers/recv-001/staged",
	}

	disallowedMethods := []string{http.MethodPost, http.MethodPut, http.MethodDelete}

	for _, ep := range stagedEndpoints {
		for _, method := range disallowedMethods {
			req, err := http.NewRequest(method, ts.URL+ep, nil)
			if err != nil {
				t.Fatalf("new request %s %s: %v", method, ep, err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", method, ep, err)
			}
			_ = resp.Body.Close()

			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: got %d, want 405", method, ep, resp.StatusCode)
			}
		}
	}
}

func TestConnectionSenderSubresources(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddSender("sender-001", "v=0\r\n")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/senders/sender-001/")
	if err != nil {
		t.Fatalf("GET sender root: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var paths []string
	if err := json.NewDecoder(resp.Body).Decode(&paths); err != nil {
		t.Fatalf("decode: %v", err)
	}

	expected := map[string]bool{
		"constraints/":  true,
		"staged/":       true,
		"active/":       true,
		"transportfile": true,
		"transporttype": true,
	}
	if len(paths) != len(expected) {
		t.Fatalf("paths count: got %d, want %d", len(paths), len(expected))
	}
	for _, p := range paths {
		if !expected[p] {
			t.Errorf("unexpected path %q", p)
		}
	}
}

func TestConnectionReceiverSubresources(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/recv-001/")
	if err != nil {
		t.Fatalf("GET receiver root: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var paths []string
	if err := json.NewDecoder(resp.Body).Decode(&paths); err != nil {
		t.Fatalf("decode: %v", err)
	}

	expected := map[string]bool{
		"constraints/":  true,
		"staged/":       true,
		"active/":       true,
		"transporttype": true,
	}
	if len(paths) != len(expected) {
		t.Fatalf("paths count: got %d, want %d", len(paths), len(expected))
	}
	for _, p := range paths {
		if !expected[p] {
			t.Errorf("unexpected path %q", p)
		}
	}
}

func TestConnectionSenderConstraints(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddSender("sender-001", "v=0\r\n")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/senders/sender-001/constraints")
	if err != nil {
		t.Fatalf("GET constraints: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var constraints json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&constraints); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(constraints) != "[]" {
		t.Errorf("constraints: got %s, want []", string(constraints))
	}
}

func TestConnectionReceiverConstraints(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/recv-001/constraints")
	if err != nil {
		t.Fatalf("GET constraints: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var constraints json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&constraints); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(constraints) != "[]" {
		t.Errorf("constraints: got %s, want []", string(constraints))
	}
}

func TestConnectionReceiverTransportType(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/recv-001/transporttype")
	if err != nil {
		t.Fatalf("GET transporttype: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var tt string
	if err := json.NewDecoder(resp.Body).Decode(&tt); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if tt != TransportRTP {
		t.Errorf("transport type: got %q, want %q", tt, TransportRTP)
	}
}

func TestConnectionSenderActivateImmediate(t *testing.T) {
	var activatedID string
	var activatedParams StagedParams

	cs := NewConnectionServer(ConnectionServerOpts{
		OnActivateSender: func(id string, params StagedParams) error {
			activatedID = id
			activatedParams = params
			return nil
		},
	})
	cs.AddSender("sender-001", "v=0\r\n")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	masterEnable := true
	mode := "activate_immediate"
	srcIP := "192.168.1.100"
	destIP := "239.1.1.1"
	destPort := 5004
	rtpEnabled := true

	patch := StagedParams{
		MasterEnable: &masterEnable,
		TransportParams: []TransportParams{
			{
				SourceIP:        &srcIP,
				DestinationIP:   &destIP,
				DestinationPort: &destPort,
				RTPEnabled:      &rtpEnabled,
			},
		},
		Activation: &Activation{
			Mode: &mode,
		},
	}

	body, _ := json.Marshal(patch)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/senders/sender-001/staged", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: got %d, want 200 or 202", resp.StatusCode)
	}

	if activatedID != "sender-001" {
		t.Errorf("activated ID: got %q, want %q", activatedID, "sender-001")
	}
	if len(activatedParams.TransportParams) == 0 {
		t.Error("activated params should have transport_params")
	}
}

func TestConnectionSenderDeactivate(t *testing.T) {
	var deactivatedID string

	cs := NewConnectionServer(ConnectionServerOpts{
		OnDeactivateSender: func(id string) error {
			deactivatedID = id
			return nil
		},
	})
	cs.AddSender("sender-001", "v=0\r\n")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	masterEnable := false
	mode := "activate_immediate"
	patch := StagedParams{
		MasterEnable: &masterEnable,
		TransportParams: []TransportParams{
			{},
		},
		Activation: &Activation{
			Mode: &mode,
		},
	}

	body, _ := json.Marshal(patch)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/senders/sender-001/staged", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if deactivatedID != "sender-001" {
		t.Errorf("deactivated ID: got %q, want %q", deactivatedID, "sender-001")
	}
}

func TestConnectionPatchInvalidJSON(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/receivers/recv-001/staged", bytes.NewReader([]byte("{invalid}")))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestConnectionRemoveSender(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddSender("sender-001", "v=0\r\n")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	// Verify exists.
	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/senders/sender-001/staged")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("before remove: got %d, want 200", resp.StatusCode)
	}

	cs.RemoveSender("sender-001")

	// Verify gone.
	resp2, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/senders/sender-001/staged")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("after remove: got %d, want 404", resp2.StatusCode)
	}
}

func TestConnectionRemoveReceiver(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	// Verify exists.
	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/recv-001/staged")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("before remove: got %d, want 200", resp.StatusCode)
	}

	cs.RemoveReceiver("recv-001")

	// Verify gone.
	resp2, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/recv-001/staged")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("after remove: got %d, want 404", resp2.StatusCode)
	}
}

func TestConnectionPatchMerge(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	// First PATCH: set destination_ip.
	destIP := "239.1.1.1"
	patch1 := StagedParams{
		TransportParams: []TransportParams{
			{DestinationIP: &destIP},
		},
	}
	body1, _ := json.Marshal(patch1)
	req1, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/receivers/recv-001/staged", bytes.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("PATCH 1: %v", err)
	}
	_ = resp1.Body.Close()

	// Second PATCH: set destination_port only.
	destPort := 5004
	patch2 := StagedParams{
		TransportParams: []TransportParams{
			{DestinationPort: &destPort},
		},
	}
	body2, _ := json.Marshal(patch2)
	req2, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/receivers/recv-001/staged", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("PATCH 2: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	var staged StagedParams
	if err := json.NewDecoder(resp2.Body).Decode(&staged); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(staged.TransportParams) == 0 {
		t.Fatal("transport_params should not be empty")
	}

	tp := staged.TransportParams[0]
	// destination_ip from first patch should still be present.
	if tp.DestinationIP == nil || *tp.DestinationIP != destIP {
		t.Errorf("destination_ip after merge: got %v, want %q", tp.DestinationIP, destIP)
	}
	// destination_port from second patch should be present.
	if tp.DestinationPort == nil || *tp.DestinationPort != destPort {
		t.Errorf("destination_port after merge: got %v, want %d", tp.DestinationPort, destPort)
	}
}

func TestConnectionDeactivateViaRTPDisabled(t *testing.T) {
	var deactivatedID string

	cs := NewConnectionServer(ConnectionServerOpts{
		OnDeactivateReceiver: func(id string) error {
			deactivatedID = id
			return nil
		},
	})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	masterEnable := true
	rtpEnabled := false
	mode := "activate_immediate"
	patch := StagedParams{
		MasterEnable: &masterEnable,
		TransportParams: []TransportParams{
			{RTPEnabled: &rtpEnabled},
		},
		Activation: &Activation{
			Mode: &mode,
		},
	}

	body, _ := json.Marshal(patch)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/receivers/recv-001/staged", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if deactivatedID != "recv-001" {
		t.Errorf("deactivated ID: got %q, want %q", deactivatedID, "recv-001")
	}
}

func TestConnectionSenderActive(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddSender("sender-001", "v=0\r\n")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/senders/sender-001/active")
	if err != nil {
		t.Fatalf("GET active: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var active StagedParams
	if err := json.NewDecoder(resp.Body).Decode(&active); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Default active should have empty transport_params.
	if active.TransportParams == nil {
		t.Error("transport_params should not be nil")
	}
}

func TestConnectionThreadSafety(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{
		OnActivateReceiver:   func(id string, params StagedParams) error { return nil },
		OnDeactivateReceiver: func(id string) error { return nil },
		OnActivateSender:     func(id string, params StagedParams) error { return nil },
		OnDeactivateSender:   func(id string) error { return nil },
	})
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	const goroutines = 20
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				senderID := "sender-001"
				recvID := "recv-001"

				cs.AddSender(senderID, "v=0\r\n")
				cs.AddReceiver(recvID)

				// Concurrent reads.
				resp1, err1 := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/senders/" + senderID + "/staged")
				if err1 == nil {
					_ = resp1.Body.Close()
				}
				resp2, err2 := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/" + recvID + "/active")
				if err2 == nil {
					_ = resp2.Body.Close()
				}

				// Concurrent writes.
				destIP := "239.1.1.1"
				mode := "activate_immediate"
				masterEnable := true
				patch := StagedParams{
					MasterEnable:    &masterEnable,
					TransportParams: []TransportParams{{DestinationIP: &destIP}},
					Activation:      &Activation{Mode: &mode},
				}
				body, _ := json.Marshal(patch)
				req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/receivers/"+recvID+"/staged", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					_ = resp.Body.Close()
				}

				cs.RemoveSender(senderID)
				cs.RemoveReceiver(recvID)
			}
		}(g)
	}

	wg.Wait()
}

func TestConnectionActivateCallbackError(t *testing.T) {
	callbackErr := errors.New("activation failed: device busy")

	cs := NewConnectionServer(ConnectionServerOpts{
		OnActivateReceiver: func(id string, params StagedParams) error {
			return callbackErr
		},
	})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	// Get the initial active state before any activation attempt.
	resp0, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/recv-001/active")
	if err != nil {
		t.Fatalf("GET active: %v", err)
	}
	var initialActive StagedParams
	if err := json.NewDecoder(resp0.Body).Decode(&initialActive); err != nil {
		t.Fatalf("decode initial active: %v", err)
	}
	_ = resp0.Body.Close()

	// Attempt activate_immediate with a callback that returns an error.
	destIP := "239.1.1.1"
	destPort := 5004
	rtpEnabled := true
	masterEnable := true
	mode := "activate_immediate"
	patch := StagedParams{
		MasterEnable: &masterEnable,
		TransportParams: []TransportParams{
			{
				DestinationIP:   &destIP,
				DestinationPort: &destPort,
				RTPEnabled:      &rtpEnabled,
			},
		},
		Activation: &Activation{
			Mode: &mode,
		},
	}

	body, _ := json.Marshal(patch)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/receivers/recv-001/staged", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", resp.StatusCode)
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp["debug"] != callbackErr.Error() {
		t.Errorf("error message: got %q, want %q", errResp["debug"], callbackErr.Error())
	}

	// Verify active state was rolled back (should match initial state).
	resp2, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/recv-001/active")
	if err != nil {
		t.Fatalf("GET active: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	var active StagedParams
	if err := json.NewDecoder(resp2.Body).Decode(&active); err != nil {
		t.Fatalf("decode active: %v", err)
	}

	// Active should not have the destination_ip we tried to set.
	if len(active.TransportParams) > 0 && active.TransportParams[0].DestinationIP != nil {
		t.Errorf("active destination_ip should be nil after rollback, got %q", *active.TransportParams[0].DestinationIP)
	}
}

func TestConnectionActivateCallbackErrorSender(t *testing.T) {
	callbackErr := errors.New("sender activation failed")

	cs := NewConnectionServer(ConnectionServerOpts{
		OnActivateSender: func(id string, params StagedParams) error {
			return callbackErr
		},
	})
	cs.AddSender("sender-001", "v=0\r\n")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	masterEnable := true
	mode := "activate_immediate"
	srcIP := "192.168.1.100"
	destIP := "239.1.1.1"
	destPort := 5004
	rtpEnabled := true

	patch := StagedParams{
		MasterEnable: &masterEnable,
		TransportParams: []TransportParams{
			{
				SourceIP:        &srcIP,
				DestinationIP:   &destIP,
				DestinationPort: &destPort,
				RTPEnabled:      &rtpEnabled,
			},
		},
		Activation: &Activation{
			Mode: &mode,
		},
	}

	body, _ := json.Marshal(patch)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/senders/sender-001/staged", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", resp.StatusCode)
	}

	// Verify active state was rolled back.
	resp2, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/senders/sender-001/active")
	if err != nil {
		t.Fatalf("GET active: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	var active StagedParams
	if err := json.NewDecoder(resp2.Body).Decode(&active); err != nil {
		t.Fatalf("decode active: %v", err)
	}

	if len(active.TransportParams) > 0 && active.TransportParams[0].DestinationIP != nil {
		t.Errorf("active destination_ip should be nil after rollback, got %q", *active.TransportParams[0].DestinationIP)
	}
}

func TestConnectionDeactivateCallbackError(t *testing.T) {
	// First activate successfully, then fail deactivation.
	cs := NewConnectionServer(ConnectionServerOpts{
		OnActivateReceiver: func(id string, params StagedParams) error {
			return nil
		},
		OnDeactivateReceiver: func(id string) error {
			return errors.New("deactivation failed")
		},
	})
	cs.AddReceiver("recv-001")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	// First, activate successfully.
	destIP := "239.1.1.1"
	masterEnable := true
	rtpEnabled := true
	mode := "activate_immediate"
	patch := StagedParams{
		MasterEnable:    &masterEnable,
		TransportParams: []TransportParams{{DestinationIP: &destIP, RTPEnabled: &rtpEnabled}},
		Activation:      &Activation{Mode: &mode},
	}
	body, _ := json.Marshal(patch)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/receivers/recv-001/staged", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	// Now attempt deactivation, which should fail.
	masterEnable = false
	patch2 := StagedParams{
		MasterEnable:    &masterEnable,
		TransportParams: []TransportParams{{}},
		Activation:      &Activation{Mode: &mode},
	}
	body2, _ := json.Marshal(patch2)
	req2, _ := http.NewRequest(http.MethodPatch, ts.URL+"/x-nmos/connection/v1.1/single/receivers/recv-001/staged", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", resp2.StatusCode)
	}

	// Active should still have the destination_ip from the first activation.
	resp3, err := http.Get(ts.URL + "/x-nmos/connection/v1.1/single/receivers/recv-001/active")
	if err != nil {
		t.Fatalf("GET active: %v", err)
	}
	defer func() { _ = resp3.Body.Close() }()
	var active StagedParams
	if err := json.NewDecoder(resp3.Body).Decode(&active); err != nil {
		t.Fatalf("decode active: %v", err)
	}
	if len(active.TransportParams) == 0 || active.TransportParams[0].DestinationIP == nil || *active.TransportParams[0].DestinationIP != destIP {
		t.Errorf("active should still have destination_ip %q after failed deactivation", destIP)
	}
}

func TestConnectionUnsupportedActivationMode(t *testing.T) {
	cs := NewConnectionServer(ConnectionServerOpts{})
	cs.AddReceiver("recv-001")
	cs.AddSender("sender-001", "v=0\r\n")
	ts := httptest.NewServer(cs.Handler())
	defer ts.Close()

	tests := []struct {
		name       string
		endpoint   string
		mode       string
		wantStatus int
	}{
		{
			name:       "receiver scheduled absolute",
			endpoint:   "/x-nmos/connection/v1.1/single/receivers/recv-001/staged",
			mode:       "activate_scheduled_absolute",
			wantStatus: http.StatusNotImplemented,
		},
		{
			name:       "receiver scheduled relative",
			endpoint:   "/x-nmos/connection/v1.1/single/receivers/recv-001/staged",
			mode:       "activate_scheduled_relative",
			wantStatus: http.StatusNotImplemented,
		},
		{
			name:       "sender scheduled absolute",
			endpoint:   "/x-nmos/connection/v1.1/single/senders/sender-001/staged",
			mode:       "activate_scheduled_absolute",
			wantStatus: http.StatusNotImplemented,
		},
		{
			name:       "receiver unknown mode",
			endpoint:   "/x-nmos/connection/v1.1/single/receivers/recv-001/staged",
			mode:       "activate_bogus",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "sender unknown mode",
			endpoint:   "/x-nmos/connection/v1.1/single/senders/sender-001/staged",
			mode:       "activate_bogus",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := StagedParams{
				TransportParams: []TransportParams{{}},
				Activation:      &Activation{Mode: &tt.mode},
			}
			body, _ := json.Marshal(patch)
			req, _ := http.NewRequest(http.MethodPatch, ts.URL+tt.endpoint, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("PATCH: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tt.wantStatus {
				bodyBytes, _ := io.ReadAll(resp.Body)
				t.Fatalf("status: got %d, want %d; body: %s", resp.StatusCode, tt.wantStatus, string(bodyBytes))
			}
		})
	}
}
