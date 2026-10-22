package nmos

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testDomain = "1ac254d9-a5eb-475f-a2b6-3d02a5cfbc82"
	testFlow   = "4c02e7c0-ffeb-4595-8673-a265d103123a"
	testFlow2  = "3494dc19-22c2-4f00-865b-cdbeee7909f0"
	testSender = "f68987c7-80fa-4b72-b299-00e66e1b1a41"
	rxID       = "61aa4757-7494-5e35-a99f-f6e2a7c25145"
	txID       = "7c3b1a50-2f1e-4a8e-9a77-0b6f6b6f1a01"
)

type activation struct {
	id     string
	active MXLEndpoint
}

func newMXLServer(t *testing.T, fail error) (*MXLConnectionServer, *[]activation) {
	t.Helper()
	var got []activation
	s := NewMXLConnectionServer(MXLConnectionServerOpts{
		DomainID: testDomain,
		OnActivateReceiver: func(_ context.Context, id string, a MXLEndpoint) error {
			if fail != nil {
				return fail
			}
			got = append(got, activation{id, a})
			return nil
		},
	})
	s.AddReceiver(rxID)
	s.AddSender(txID, testFlow2)
	return s, &got
}

func do(t *testing.T, s *MXLConnectionServer, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, connectionAPIBase+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func doAny(t *testing.T, s *MXLConnectionServer, path string) (int, any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, connectionAPIBase+path, nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	var out any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// BCP-007-03: both parameters present and null while unconfigured, no
// transport file, and the MXL transport type.
func TestMXLReceiver_UnconfiguredState(t *testing.T) {
	s, _ := newMXLServer(t, nil)
	for _, ep := range []string{"staged", "active"} {
		code, body := do(t, s, http.MethodGet, "single/receivers/"+rxID+"/"+ep, "")
		if code != http.StatusOK {
			t.Fatalf("%s: %d", ep, code)
		}
		for _, k := range []string{"sender_id", "master_enable", "activation", "transport_file", "transport_params"} {
			if _, ok := body[k]; !ok {
				t.Errorf("%s lacks %s", ep, k)
			}
		}
		leg := body["transport_params"].([]any)[0].(map[string]any)
		if v, ok := leg[ParamMXLFlowID]; !ok || v != nil {
			t.Errorf("%s: mxl_flow_id = %v, want present and null", ep, v)
		}
		if v, ok := leg[ParamMXLDomainID]; !ok || v != nil {
			t.Errorf("%s: mxl_domain_id = %v, want present and null", ep, v)
		}
		if body["master_enable"] != false {
			t.Errorf("%s: an unconnected receiver must not be enabled", ep)
		}
	}
	if _, tt := doAny(t, s, "single/receivers/"+rxID+"/transporttype"); tt != TransportMXL {
		t.Errorf("transporttype = %v", tt)
	}
	_, cons := doAny(t, s, "single/receivers/"+rxID+"/constraints")
	set := cons.([]any)
	if len(set) != 1 {
		t.Fatalf("constraints = %v, want one set", cons)
	}
	c := set[0].(map[string]any)
	if _, ok := c[ParamMXLFlowID]; !ok {
		t.Error("constraints lack mxl_flow_id")
	}
	if dom := c[ParamMXLDomainID].(map[string]any); dom["enum"].([]any)[0] != testDomain {
		t.Errorf("mxl_domain_id constraint = %v, want this domain", dom)
	}
}

// Connecting resolves "auto" to this node's domain, applies the flow and
// publishes what was activated.
func TestMXLReceiver_ImmediateActivation(t *testing.T) {
	s, got := newMXLServer(t, nil)
	code, body := do(t, s, http.MethodPatch, "single/receivers/"+rxID+"/staged", `{
		"sender_id": "`+testSender+`",
		"master_enable": true,
		"activation": {"mode": "activate_immediate"},
		"transport_params": [{"mxl_domain_id": "auto", "mxl_flow_id": "`+testFlow+`"}]
	}`)
	if code != http.StatusOK {
		t.Fatalf("PATCH: %d %v", code, body)
	}
	if body["activation"].(map[string]any)["activation_time"] == nil {
		t.Error("response lacks the activation time")
	}
	if len(*got) != 1 {
		t.Fatalf("callback ran %d times", len(*got))
	}
	a := (*got)[0].active
	if a.FlowID() != testFlow || *a.TransportParams[0].DomainID != testDomain || *a.SenderID != testSender {
		t.Errorf("activated %+v", a)
	}

	_, active := do(t, s, http.MethodGet, "single/receivers/"+rxID+"/active", "")
	leg := active["transport_params"].([]any)[0].(map[string]any)
	if leg[ParamMXLDomainID] != testDomain {
		t.Errorf("active mxl_domain_id = %v, want auto resolved to %s", leg[ParamMXLDomainID], testDomain)
	}
	_, staged := do(t, s, http.MethodGet, "single/receivers/"+rxID+"/staged", "")
	if staged["activation"].(map[string]any)["mode"] != nil {
		t.Error("staged activation was not reset after the immediate activation")
	}
}

// Staging alone changes nothing that is running.
func TestMXLReceiver_StagingDoesNotActivate(t *testing.T) {
	s, got := newMXLServer(t, nil)
	code, _ := do(t, s, http.MethodPatch, "single/receivers/"+rxID+"/staged",
		`{"master_enable": true, "transport_params": [{"mxl_flow_id": "`+testFlow+`"}]}`)
	if code != http.StatusOK || len(*got) != 0 {
		t.Fatalf("code %d, %d activations", code, len(*got))
	}
	a, _ := s.ReceiverActive(rxID)
	if a.MasterEnable || a.FlowID() != "" {
		t.Errorf("active changed by staging: %+v", a)
	}
}

func TestMXLReceiver_Rejections(t *testing.T) {
	cases := map[string]struct {
		body string
		code int
	}{
		"flow id auto":           {`{"transport_params":[{"mxl_flow_id":"auto"}]}`, 400},
		"foreign domain":         {`{"transport_params":[{"mxl_domain_id":"3310f209-9351-47c0-b9a2-14c59b6a4c23"}]}`, 400},
		"not a uuid":             {`{"transport_params":[{"mxl_flow_id":"cam1"}]}`, 400},
		"two legs":               {`{"transport_params":[{},{}]}`, 400},
		"unknown param":          {`{"transport_params":[{"source_ip":"10.0.0.1"}]}`, 400},
		"transport file":         {`{"transport_file":{"data":"v=0","type":"application/sdp"}}`, 400},
		"scheduled":              {`{"activation":{"mode":"activate_scheduled_relative","requested_time":"1:0"}}`, 501},
		"enabled without a flow": {`{"master_enable":true,"activation":{"mode":"activate_immediate"}}`, 400},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s, got := newMXLServer(t, nil)
			code, body := do(t, s, http.MethodPatch, "single/receivers/"+rxID+"/staged", c.body)
			if code != c.code {
				t.Fatalf("code %d %v, want %d", code, body, c.code)
			}
			if len(*got) != 0 {
				t.Error("a rejected request reached the receiver")
			}
		})
	}
}

// A null transport file is how a controller says "none", and is accepted.
func TestMXLReceiver_NullTransportFileAccepted(t *testing.T) {
	s, _ := newMXLServer(t, nil)
	code, body := do(t, s, http.MethodPatch, "single/receivers/"+rxID+"/staged",
		`{"transport_file":{"data":null,"type":null},"transport_params":[{"mxl_flow_id":"`+testFlow+`"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
}

// An activation the input cannot apply leaves the published state as it was.
func TestMXLReceiver_FailedActivationKeepsActive(t *testing.T) {
	s, _ := newMXLServer(t, errors.New("flow has another shape"))
	code, _ := do(t, s, http.MethodPatch, "single/receivers/"+rxID+"/staged", `{
		"master_enable": true, "activation": {"mode": "activate_immediate"},
		"transport_params": [{"mxl_flow_id": "`+testFlow+`"}]}`)
	if code != http.StatusInternalServerError {
		t.Fatalf("code %d, want 500", code)
	}
	if a, _ := s.ReceiverActive(rxID); a.MasterEnable || a.FlowID() != "" {
		t.Errorf("active changed by a failed activation: %+v", a)
	}
}

// A sender's flow is the booking's; it has no transport file, and a change
// to what it writes is refused while a no-op activation is accepted.
func TestMXLSender_FixedByConfiguration(t *testing.T) {
	s, _ := newMXLServer(t, nil)
	if code, _ := do(t, s, http.MethodGet, "single/senders/"+txID+"/transportfile", ""); code != http.StatusNotFound {
		t.Errorf("transportfile: %d, want 404", code)
	}
	_, active := do(t, s, http.MethodGet, "single/senders/"+txID+"/active", "")
	leg := active["transport_params"].([]any)[0].(map[string]any)
	if leg[ParamMXLFlowID] != testFlow2 || active["master_enable"] != true {
		t.Errorf("sender active = %v", active)
	}
	if _, ok := active["receiver_id"]; !ok {
		t.Error("sender lacks receiver_id")
	}
	code, _ := do(t, s, http.MethodPatch, "single/senders/"+txID+"/staged",
		`{"transport_params":[{"mxl_flow_id":"`+testFlow+`"}],"activation":{"mode":"activate_immediate"}}`)
	if code != http.StatusBadRequest {
		t.Errorf("changing the sender's flow: %d, want 400", code)
	}
	code, _ = do(t, s, http.MethodPatch, "single/senders/"+txID+"/staged",
		`{"master_enable":true,"activation":{"mode":"activate_immediate"}}`)
	if code != http.StatusOK {
		t.Errorf("no-op activation: %d, want 200", code)
	}
}

func TestMXLConnection_Listing(t *testing.T) {
	s, _ := newMXLServer(t, nil)
	_, rx := doAny(t, s, "single/receivers/")
	if l := rx.([]any); len(l) != 1 || l[0] != rxID+"/" {
		t.Errorf("receivers = %v", rx)
	}
	if code, _ := do(t, s, http.MethodGet, "single/receivers/nope/staged", ""); code != http.StatusNotFound {
		t.Errorf("unknown receiver: %d", code)
	}
}

// A connection naming only the flow reads it in the node's one domain, and
// active says so.
func TestMXLReceiver_NullDomainResolvedWhenEnabled(t *testing.T) {
	s, got := newMXLServer(t, nil)
	code, _ := do(t, s, http.MethodPatch, "single/receivers/"+rxID+"/staged", `{
		"master_enable": true, "activation": {"mode": "activate_immediate"},
		"transport_params": [{"mxl_flow_id": "`+testFlow+`"}]}`)
	if code != http.StatusOK || len(*got) != 1 {
		t.Fatalf("code %d, %d activations", code, len(*got))
	}
	if d := (*got)[0].active.TransportParams[0].DomainID; d == nil || *d != testDomain {
		t.Errorf("active domain %v, want %s", d, testDomain)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"10:5", "10:5", 0}, {"9:999999999", "10:0", -1}, {"10:10", "10:9", 1}, {"x", "10:0", 0},
	} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
