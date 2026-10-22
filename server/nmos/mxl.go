package nmos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// TransportMXL is the transport of an MXL Sender or Receiver (AMWA
// BCP-007-03). Nothing travels on a network: a sender writes grains into an
// MXL domain and a receiver reads a flow from one, so connecting a receiver
// names a flow rather than an address.
const TransportMXL = "urn:x-nmos:transport:mxl"

// MXL transport parameter names.
const (
	ParamMXLDomainID = "mxl_domain_id"
	ParamMXLFlowID   = "mxl_flow_id"
)

// Media types MXL flows carry.
const (
	MediaTypeV210      = "video/v210"
	MediaTypeFloat32   = "audio/float32"
	MediaTypeSMPTE291  = "video/smpte291"
	paramAuto          = "auto"
	activateImmediate  = "activate_immediate"
	activateScheduledA = "activate_scheduled_absolute"
	activateScheduledR = "activate_scheduled_relative"
)

// mxlUUIDRe is the MXL UUID form both MXL transport parameters take.
var mxlUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// MXLParams is one leg of MXL transport parameters. A nil field is JSON
// null, which BCP-007-03 uses for a value not determined yet.
type MXLParams struct {
	DomainID *string `json:"mxl_domain_id"`
	FlowID   *string `json:"mxl_flow_id"`
}

// MXLTransportFile is the transport_file of an MXL receiver, which never
// carries one.
type MXLTransportFile struct {
	Data *string `json:"data"`
	Type *string `json:"type"`
}

// MXLEndpoint is the staged or active state of an MXL sender or receiver.
// SenderID and TransportFile belong to receivers, ReceiverID to senders.
type MXLEndpoint struct {
	SenderID        *string
	ReceiverID      *string
	MasterEnable    bool
	Activation      Activation
	TransportFile   *MXLTransportFile
	TransportParams []MXLParams

	// receiver selects the receiver form of the JSON.
	receiver bool
}

// MarshalJSON writes every attribute IS-05 requires of the endpoint's kind,
// null where unset.
func (e MXLEndpoint) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"master_enable": e.MasterEnable,
		"activation": map[string]any{
			"mode":            e.Activation.Mode,
			"requested_time":  e.Activation.RequestedTime,
			"activation_time": e.Activation.ActivationTime,
		},
		"transport_params": e.TransportParams,
	}
	if e.receiver {
		m["sender_id"] = e.SenderID
		tf := e.TransportFile
		if tf == nil {
			tf = &MXLTransportFile{}
		}
		m["transport_file"] = tf
	} else {
		m["receiver_id"] = e.ReceiverID
	}
	return json.Marshal(m)
}

// NewMXLReceiverEndpoint is a receiver state, e.g. for SetReceiverActive.
func NewMXLReceiverEndpoint(senderID *string, enable bool, domainID, flowID *string) MXLEndpoint {
	return MXLEndpoint{
		SenderID:        cloneStr(senderID),
		MasterEnable:    enable,
		TransportFile:   &MXLTransportFile{},
		TransportParams: []MXLParams{{DomainID: cloneStr(domainID), FlowID: cloneStr(flowID)}},
		receiver:        true,
	}
}

// UnmarshalJSON reads either form MarshalJSON writes.
func (e *MXLEndpoint) UnmarshalJSON(data []byte) error {
	var raw struct {
		SenderID        *string           `json:"sender_id"`
		ReceiverID      *string           `json:"receiver_id"`
		MasterEnable    bool              `json:"master_enable"`
		Activation      Activation        `json:"activation"`
		TransportFile   *MXLTransportFile `json:"transport_file"`
		TransportParams []MXLParams       `json:"transport_params"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	_, isReceiver := fields["sender_id"]
	*e = MXLEndpoint{
		SenderID:        raw.SenderID,
		ReceiverID:      raw.ReceiverID,
		MasterEnable:    raw.MasterEnable,
		Activation:      raw.Activation,
		TransportFile:   raw.TransportFile,
		TransportParams: raw.TransportParams,
		receiver:        isReceiver,
	}
	if len(e.TransportParams) == 0 {
		e.TransportParams = []MXLParams{{}}
	}
	return nil
}

func (e MXLEndpoint) clone() MXLEndpoint {
	c := e
	c.SenderID = cloneStr(e.SenderID)
	c.ReceiverID = cloneStr(e.ReceiverID)
	c.Activation = Activation{
		Mode:           cloneStr(e.Activation.Mode),
		RequestedTime:  cloneStr(e.Activation.RequestedTime),
		ActivationTime: cloneStr(e.Activation.ActivationTime),
	}
	if e.TransportFile != nil {
		c.TransportFile = &MXLTransportFile{Data: cloneStr(e.TransportFile.Data), Type: cloneStr(e.TransportFile.Type)}
	}
	c.TransportParams = make([]MXLParams, len(e.TransportParams))
	for i, p := range e.TransportParams {
		c.TransportParams[i] = MXLParams{DomainID: cloneStr(p.DomainID), FlowID: cloneStr(p.FlowID)}
	}
	return c
}

func cloneStr(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}

// FlowID is the endpoint's mxl_flow_id, or empty when null.
func (e MXLEndpoint) FlowID() string {
	if len(e.TransportParams) == 0 || e.TransportParams[0].FlowID == nil {
		return ""
	}
	return *e.TransportParams[0].FlowID
}

// MXLActivateFunc applies an activated receiver or sender state. An error
// rejects the activation and leaves the active state as it was.
type MXLActivateFunc func(ctx context.Context, id string, active MXLEndpoint) error

// MXLConnectionServerOpts configures an MXLConnectionServer.
type MXLConnectionServerOpts struct {
	// DomainID is the id of the MXL domain this node reads and writes, from
	// the domain's domain_def.json. It is what "auto" resolves to and the
	// only value mxl_domain_id may take. Empty refuses to resolve "auto".
	DomainID string
	// OnActivateReceiver applies a receiver activation.
	OnActivateReceiver MXLActivateFunc
	// OnActivateSender applies a sender activation. Nil refuses every
	// change to a sender: what it writes is fixed by configuration.
	OnActivateSender MXLActivateFunc
}

type mxlState struct {
	staged MXLEndpoint
	active MXLEndpoint
	lock   sync.Mutex // serialises PATCH processing for this resource
}

// MXLConnectionServer serves the IS-05 Connection API for MXL senders and
// receivers as BCP-007-03 specifies it: a single leg of mxl_domain_id and
// mxl_flow_id, no transport file, "auto" only where the node resolves it,
// and only immediate activations.
type MXLConnectionServer struct {
	opts MXLConnectionServerOpts

	mu        sync.RWMutex
	receivers map[string]*mxlState
	senders   map[string]*mxlState
	mux       *http.ServeMux
}

// NewMXLConnectionServer creates the server; resources are added with
// AddReceiver and AddSender.
func NewMXLConnectionServer(opts MXLConnectionServerOpts) *MXLConnectionServer {
	s := &MXLConnectionServer{
		opts:      opts,
		receivers: map[string]*mxlState{},
		senders:   map[string]*mxlState{},
		mux:       http.NewServeMux(),
	}
	s.mux.HandleFunc(connectionAPIBase, s.handle)
	return s
}

// Handler returns the HTTP handler for the Connection API.
func (s *MXLConnectionServer) Handler() http.Handler { return s.mux }

// AddReceiver adds an unconnected receiver: master_enable false and both
// parameters null.
func (s *MXLConnectionServer) AddReceiver(id string) {
	e := MXLEndpoint{
		TransportFile:   &MXLTransportFile{},
		TransportParams: []MXLParams{{}},
		receiver:        true,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receivers[id] = &mxlState{staged: e.clone(), active: e.clone()}
}

// AddSender adds a sender writing flowID, enabled.
func (s *MXLConnectionServer) AddSender(id, flowID string) {
	dom := cloneStr(nonEmpty(s.opts.DomainID))
	e := MXLEndpoint{
		MasterEnable:    true,
		TransportParams: []MXLParams{{DomainID: dom, FlowID: &flowID}},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.senders[id] = &mxlState{staged: e.clone(), active: e.clone()}
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ReceiverActive returns a receiver's active state.
func (s *MXLConnectionServer) ReceiverActive(id string) (MXLEndpoint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.receivers[id]
	if !ok {
		return MXLEndpoint{}, false
	}
	return st.active.clone(), true
}

// SetReceiverActive records a receiver's active state without an
// activation callback, for state applied by other means, e.g. taken over
// from a peer.
func (s *MXLConnectionServer) SetReceiverActive(id string, active MXLEndpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.receivers[id]; ok {
		st.active = active.clone()
		st.staged = active.clone()
		st.staged.Activation = Activation{}
	}
}

func (s *MXLConnectionServer) lookup(kind, id string) *mxlState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if kind == "receivers" {
		return s.receivers[id]
	}
	return s.senders[id]
}

func (s *MXLConnectionServer) ids(kind string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.senders
	if kind == "receivers" {
		m = s.receivers
	}
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id+"/")
	}
	return out
}

func (s *MXLConnectionServer) handle(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, connectionAPIBase), "/")
	parts := strings.Split(rest, "/")
	get := func(v any) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
	switch {
	case rest == "":
		get([]string{"single/"})
		return
	case len(parts) == 1 && parts[0] == "single":
		get([]string{"senders/", "receivers/"})
		return
	case len(parts) < 2 || parts[0] != "single" || (parts[1] != "senders" && parts[1] != "receivers"):
		writeNotFound(w)
		return
	}
	kind := parts[1]
	if len(parts) == 2 {
		get(s.ids(kind))
		return
	}
	id := parts[2]
	st := s.lookup(kind, id)
	if st == nil {
		writeNotFound(w)
		return
	}
	if len(parts) == 3 {
		sub := []string{"constraints/", "staged/", "active/", "transporttype/"}
		if kind == "senders" {
			sub = append(sub, "transportfile/")
		}
		get(sub)
		return
	}
	if len(parts) != 4 {
		writeNotFound(w)
		return
	}
	switch parts[3] {
	case "transporttype":
		get(TransportMXL)
	case "transportfile":
		// BCP-007-03: an MXL sender has no transport file, always.
		writeNotFound(w)
	case "constraints":
		get(s.constraints())
	case "active":
		st.lock.Lock()
		v := st.active.clone()
		st.lock.Unlock()
		get(v)
	case "staged":
		switch r.Method {
		case http.MethodGet:
			st.lock.Lock()
			v := st.staged.clone()
			st.lock.Unlock()
			writeJSON(w, http.StatusOK, v)
		case http.MethodPatch:
			s.patch(w, r, kind, id, st)
		default:
			w.Header().Set("Allow", "GET, PATCH")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	default:
		writeNotFound(w)
	}
}

// constraints lists the domain as the one value mxl_domain_id may take and
// leaves mxl_flow_id open; both have to be present.
func (s *MXLConnectionServer) constraints() []map[string]any {
	dom := map[string]any{}
	if s.opts.DomainID != "" {
		dom["enum"] = []string{s.opts.DomainID}
	}
	return []map[string]any{{ParamMXLDomainID: dom, ParamMXLFlowID: map[string]any{}}}
}

// patchError is a rejected PATCH with its status code.
type patchError struct {
	code int
	msg  string
}

func (e *patchError) Error() string { return e.msg }

func badRequest(format string, a ...any) error {
	return &patchError{http.StatusBadRequest, fmt.Sprintf(format, a...)}
}

func (s *MXLConnectionServer) patch(w http.ResponseWriter, r *http.Request, kind, id string, st *mxlState) {
	r.Body = http.MaxBytesReader(w, r.Body, maxStagedBodyBytes)
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	st.lock.Lock()
	defer st.lock.Unlock()

	staged := st.staged.clone()
	mode, err := s.merge(&staged, body, kind)
	if err != nil {
		writePatchError(w, err)
		return
	}
	if mode == nil {
		st.staged = staged
		writeJSON(w, http.StatusOK, staged)
		return
	}

	active := staged.clone()
	if err := s.resolve(&active, kind); err != nil {
		writePatchError(w, err)
		return
	}
	now := string(NewVersion())
	active.Activation = Activation{Mode: cloneStr(mode), RequestedTime: cloneStr(staged.Activation.RequestedTime), ActivationTime: &now}

	apply := s.opts.OnActivateReceiver
	if kind == "senders" {
		apply = s.opts.OnActivateSender
		if apply == nil {
			if !sameMXL(active, st.active) {
				writeError(w, http.StatusBadRequest, "this sender's flow is set by configuration and cannot be changed")
				return
			}
			apply = func(context.Context, string, MXLEndpoint) error { return nil }
		}
	}
	if apply != nil {
		if err := apply(r.Context(), id, active.clone()); err != nil {
			writePatchError(w, &patchError{http.StatusInternalServerError, err.Error()})
			return
		}
	}
	st.active = active
	// The activation in staged returns to null once the transaction is done;
	// the response carries what was activated.
	resp := staged.clone()
	resp.Activation = active.Activation
	staged.Activation = Activation{}
	st.staged = staged
	writeJSON(w, http.StatusOK, resp)
}

// merge applies a PATCH body to staged and returns the activation mode
// requested, or nil for none.
func (s *MXLConnectionServer) merge(staged *MXLEndpoint, body map[string]json.RawMessage, kind string) (*string, error) {
	for k := range body {
		switch k {
		case "master_enable", "activation", "transport_params":
		case "sender_id":
			if kind != "receivers" {
				return nil, badRequest("sender_id is a receiver attribute")
			}
		case "receiver_id":
			if kind != "senders" {
				return nil, badRequest("receiver_id is a sender attribute")
			}
		case "transport_file":
			if kind != "receivers" {
				return nil, badRequest("transport_file is a receiver attribute")
			}
		default:
			return nil, badRequest("unknown attribute %q", k)
		}
	}
	if raw, ok := body["master_enable"]; ok {
		if err := json.Unmarshal(raw, &staged.MasterEnable); err != nil {
			return nil, badRequest("master_enable must be a boolean")
		}
	}
	if raw, ok := body["sender_id"]; ok {
		id, err := nullableUUID(raw, "sender_id", false)
		if err != nil {
			return nil, err
		}
		staged.SenderID = id
	}
	if raw, ok := body["receiver_id"]; ok {
		id, err := nullableUUID(raw, "receiver_id", false)
		if err != nil {
			return nil, err
		}
		staged.ReceiverID = id
	}
	if raw, ok := body["transport_file"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var tf MXLTransportFile
		if err := json.Unmarshal(raw, &tf); err != nil || tf.Data != nil || tf.Type != nil {
			return nil, badRequest("an MXL receiver takes no transport file; data and type must be null")
		}
	}
	if raw, ok := body["transport_params"]; ok {
		var legs []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &legs); err != nil {
			return nil, badRequest("transport_params must be an array of objects")
		}
		if len(legs) != 1 {
			return nil, badRequest("MXL takes exactly one set of transport parameters")
		}
		if err := s.mergeParams(&staged.TransportParams[0], legs[0], kind); err != nil {
			return nil, err
		}
	}
	var mode *string
	if raw, ok := body["activation"]; ok {
		var act struct {
			Mode          *string `json:"mode"`
			RequestedTime *string `json:"requested_time"`
		}
		if err := json.Unmarshal(raw, &act); err != nil {
			return nil, badRequest("activation must be an object")
		}
		if act.Mode != nil {
			switch *act.Mode {
			case activateImmediate:
				mode = act.Mode
			case activateScheduledA, activateScheduledR:
				return nil, &patchError{http.StatusNotImplemented, fmt.Sprintf("activation mode %q is not implemented", *act.Mode)}
			default:
				return nil, badRequest("unknown activation mode %q", *act.Mode)
			}
		}
		staged.Activation = Activation{Mode: act.Mode, RequestedTime: act.RequestedTime}
	}
	return mode, nil
}

func (s *MXLConnectionServer) mergeParams(p *MXLParams, leg map[string]json.RawMessage, kind string) error {
	for k := range leg {
		if k != ParamMXLDomainID && k != ParamMXLFlowID {
			return badRequest("unknown MXL transport parameter %q", k)
		}
	}
	if raw, ok := leg[ParamMXLDomainID]; ok {
		v, err := nullableUUID(raw, ParamMXLDomainID, true)
		if err != nil {
			return err
		}
		if v != nil && *v != paramAuto && *v != s.opts.DomainID {
			return badRequest("mxl_domain_id %s is not the MXL domain this node can access", *v)
		}
		p.DomainID = v
	}
	if raw, ok := leg[ParamMXLFlowID]; ok {
		// A sender may resolve its flow itself; a receiver has to be told.
		v, err := nullableUUID(raw, ParamMXLFlowID, kind == "senders")
		if err != nil {
			return err
		}
		p.FlowID = v
	}
	return nil
}

// nullableUUID parses null, an MXL UUID, or "auto" where allowed.
func nullableUUID(raw json.RawMessage, name string, allowAuto bool) (*string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, badRequest("%s must be a string or null", name)
	}
	if v == paramAuto {
		if !allowAuto {
			return nil, badRequest("%s does not accept auto", name)
		}
		return &v, nil
	}
	if !mxlUUIDRe.MatchString(v) {
		return nil, badRequest("%s %q is not a lowercase UUID", name, v)
	}
	return &v, nil
}

// resolve turns "auto" into the value used, as the active state has to
// show it, and refuses an enabled endpoint that would read or write nothing.
func (s *MXLConnectionServer) resolve(e *MXLEndpoint, kind string) error {
	p := &e.TransportParams[0]
	if p.DomainID != nil && *p.DomainID == paramAuto {
		if s.opts.DomainID == "" {
			return &patchError{http.StatusInternalServerError, "mxl_domain_id auto: no MXL domain is known to this node"}
		}
		p.DomainID = cloneStr(&s.opts.DomainID)
	}
	if p.FlowID != nil && *p.FlowID == paramAuto {
		return &patchError{http.StatusInternalServerError, "mxl_flow_id auto: this sender does not choose its flow"}
	}
	if e.MasterEnable && kind == "receivers" && p.FlowID == nil {
		return &patchError{http.StatusInternalServerError, "master_enable needs an mxl_flow_id to read"}
	}
	return nil
}

func sameMXL(a, b MXLEndpoint) bool {
	return a.MasterEnable == b.MasterEnable && a.FlowID() == b.FlowID() &&
		ptrEq(a.ReceiverID, b.ReceiverID)
}

func ptrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func writePatchError(w http.ResponseWriter, err error) {
	var pe *patchError
	if errors.As(err, &pe) {
		writeError(w, pe.code, pe.msg)
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func writeError(w http.ResponseWriter, code int, debug string) {
	writeJSON(w, code, map[string]any{"code": code, "error": http.StatusText(code), "debug": debug})
}
