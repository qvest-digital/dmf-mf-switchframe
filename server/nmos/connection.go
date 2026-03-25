package nmos

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

const connectionAPIBase = "/x-nmos/connection/v1.1/"

// TransportParams holds RTP transport parameters for IS-05.
type TransportParams struct {
	SourceIP        *string `json:"source_ip"`
	DestinationIP   *string `json:"destination_ip"`
	DestinationPort *int    `json:"destination_port"`
	RTPEnabled      *bool   `json:"rtp_enabled"`
	InterfaceIP     *string `json:"interface_ip,omitempty"`
}

// Activation describes when a connection should be activated.
type Activation struct {
	Mode           *string `json:"mode"`
	RequestedTime  *string `json:"requested_time"`
	ActivationTime *string `json:"activation_time,omitempty"`
}

// StagedParams holds the staged configuration for a sender or receiver.
type StagedParams struct {
	TransportParams []TransportParams `json:"transport_params"`
	Activation      *Activation       `json:"activation,omitempty"`
	TransportFile   *TransportFile    `json:"transport_file,omitempty"`
	SenderID        *string           `json:"sender_id,omitempty"`
	MasterEnable    *bool             `json:"master_enable,omitempty"`
}

// TransportFile holds SDP content for receiver connection.
type TransportFile struct {
	Data string `json:"data"`
	Type string `json:"type"`
}

// ConnectionState tracks staged and active state for a sender or receiver.
type ConnectionState struct {
	Staged StagedParams `json:"staged"`
	Active StagedParams `json:"active"`
}

// ConnectionServerOpts configures callbacks for connection lifecycle events.
type ConnectionServerOpts struct {
	OnActivateReceiver   func(id string, params StagedParams) error
	OnDeactivateReceiver func(id string) error
	OnActivateSender     func(id string, params StagedParams) error
	OnDeactivateSender   func(id string) error
}

// ConnectionServer serves the NMOS IS-05 Connection API.
type ConnectionServer struct {
	mu                   sync.RWMutex
	senderStates         map[string]*ConnectionState
	receiverStates       map[string]*ConnectionState
	senderSDP            map[string]string
	onActivateReceiver   func(id string, params StagedParams) error
	onDeactivateReceiver func(id string) error
	onActivateSender     func(id string, params StagedParams) error
	onDeactivateSender   func(id string) error
	mux                  *http.ServeMux
}

// NewConnectionServer creates a ConnectionServer and registers all routes.
func NewConnectionServer(opts ConnectionServerOpts) *ConnectionServer {
	cs := &ConnectionServer{
		senderStates:         make(map[string]*ConnectionState),
		receiverStates:       make(map[string]*ConnectionState),
		senderSDP:            make(map[string]string),
		onActivateReceiver:   opts.OnActivateReceiver,
		onDeactivateReceiver: opts.OnDeactivateReceiver,
		onActivateSender:     opts.OnActivateSender,
		onDeactivateSender:   opts.OnDeactivateSender,
		mux:                  http.NewServeMux(),
	}
	cs.mux.HandleFunc(connectionAPIBase, cs.handleRoot)
	cs.mux.HandleFunc(connectionAPIBase+"single/", cs.handleSingle)
	return cs
}

// Handler returns the HTTP handler for the Connection API.
func (cs *ConnectionServer) Handler() http.Handler {
	return cs.mux
}

// AddSender registers a sender with its SDP.
func (cs *ConnectionServer) AddSender(id string, sdp string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.senderStates[id] = &ConnectionState{
		Staged: StagedParams{TransportParams: []TransportParams{{}}},
		Active: StagedParams{TransportParams: []TransportParams{{}}},
	}
	cs.senderSDP[id] = sdp
}

// AddReceiver registers a receiver.
func (cs *ConnectionServer) AddReceiver(id string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.receiverStates[id] = &ConnectionState{
		Staged: StagedParams{TransportParams: []TransportParams{{}}},
		Active: StagedParams{TransportParams: []TransportParams{{}}},
	}
}

// RemoveSender removes a sender. No-op if not found.
func (cs *ConnectionServer) RemoveSender(id string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.senderStates, id)
	delete(cs.senderSDP, id)
}

// RemoveReceiver removes a receiver. No-op if not found.
func (cs *ConnectionServer) RemoveReceiver(id string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.receiverStates, id)
}

func (cs *ConnectionServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, []string{"single/"})
}

func (cs *ConnectionServer) handleSingle(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, connectionAPIBase+"single/")

	// Root of /single/
	if path == "" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, []string{"senders/", "receivers/"})
		return
	}

	if strings.HasPrefix(path, "senders") {
		cs.handleSenders(w, r, strings.TrimPrefix(path, "senders"))
		return
	}
	if strings.HasPrefix(path, "receivers") {
		cs.handleReceivers(w, r, strings.TrimPrefix(path, "receivers"))
		return
	}

	writeNotFound(w)
}

func (cs *ConnectionServer) handleSenders(w http.ResponseWriter, r *http.Request, path string) {
	// List senders: /single/senders/ or /single/senders
	if path == "/" || path == "" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		cs.mu.RLock()
		ids := make([]string, 0, len(cs.senderStates))
		for id := range cs.senderStates {
			ids = append(ids, id)
		}
		cs.mu.RUnlock()
		writeJSON(w, http.StatusOK, ids)
		return
	}

	// Strip leading slash: /sender-001/staged -> sender-001/staged
	path = strings.TrimPrefix(path, "/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]

	// Check existence.
	cs.mu.RLock()
	_, ok := cs.senderStates[id]
	cs.mu.RUnlock()

	if !ok {
		writeNotFound(w)
		return
	}

	// /single/senders/{id}/ or /single/senders/{id}
	if len(parts) == 1 || parts[1] == "" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, []string{
			"constraints/",
			"staged/",
			"active/",
			"transportfile",
			"transporttype",
		})
		return
	}

	sub := strings.TrimSuffix(parts[1], "/")
	switch sub {
	case "staged":
		cs.handleSenderStaged(w, r, id)
	case "active":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		cs.mu.RLock()
		state, ok := cs.senderStates[id]
		if !ok {
			cs.mu.RUnlock()
			writeNotFound(w)
			return
		}
		active := copyStagedParams(state.Active)
		cs.mu.RUnlock()
		writeJSON(w, http.StatusOK, active)
	case "transportfile":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		cs.mu.RLock()
		sdp := cs.senderSDP[id]
		cs.mu.RUnlock()
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sdp))
	case "transporttype":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, TransportRTPMcast)
	case "constraints":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, []interface{}{})
	default:
		writeNotFound(w)
	}
}

func (cs *ConnectionServer) handleReceivers(w http.ResponseWriter, r *http.Request, path string) {
	// List receivers: /single/receivers/ or /single/receivers
	if path == "/" || path == "" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		cs.mu.RLock()
		ids := make([]string, 0, len(cs.receiverStates))
		for id := range cs.receiverStates {
			ids = append(ids, id)
		}
		cs.mu.RUnlock()
		writeJSON(w, http.StatusOK, ids)
		return
	}

	// Strip leading slash: /recv-001/staged -> recv-001/staged
	path = strings.TrimPrefix(path, "/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]

	// Check existence.
	cs.mu.RLock()
	_, ok := cs.receiverStates[id]
	cs.mu.RUnlock()

	if !ok {
		writeNotFound(w)
		return
	}

	// /single/receivers/{id}/ or /single/receivers/{id}
	if len(parts) == 1 || parts[1] == "" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, []string{
			"constraints/",
			"staged/",
			"active/",
			"transporttype",
		})
		return
	}

	sub := strings.TrimSuffix(parts[1], "/")
	switch sub {
	case "staged":
		cs.handleReceiverStaged(w, r, id)
	case "active":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		cs.mu.RLock()
		state, ok := cs.receiverStates[id]
		if !ok {
			cs.mu.RUnlock()
			writeNotFound(w)
			return
		}
		active := copyStagedParams(state.Active)
		cs.mu.RUnlock()
		writeJSON(w, http.StatusOK, active)
	case "transporttype":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, TransportRTP)
	case "constraints":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, []interface{}{})
	default:
		writeNotFound(w)
	}
}

func (cs *ConnectionServer) handleSenderStaged(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		cs.mu.RLock()
		state, ok := cs.senderStates[id]
		if !ok {
			cs.mu.RUnlock()
			writeNotFound(w)
			return
		}
		staged := copyStagedParams(state.Staged)
		cs.mu.RUnlock()
		writeJSON(w, http.StatusOK, staged)

	case http.MethodPatch:
		var patch StagedParams
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"code":  400,
				"error": "Bad Request",
				"debug": fmt.Sprintf("invalid JSON: %v", err),
			})
			return
		}

		// Reject unsupported or unknown activation modes before mutating state.
		if patch.Activation != nil && patch.Activation.Mode != nil {
			switch *patch.Activation.Mode {
			case "activate_immediate":
				// supported — handled below
			case "activate_scheduled_absolute", "activate_scheduled_relative":
				writeJSON(w, http.StatusNotImplemented, map[string]interface{}{
					"code":  501,
					"error": "Not Implemented",
					"debug": fmt.Sprintf("activation mode %q is not implemented", *patch.Activation.Mode),
				})
				return
			default:
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{
					"code":  400,
					"error": "Bad Request",
					"debug": fmt.Sprintf("unknown activation mode %q", *patch.Activation.Mode),
				})
				return
			}
		}

		cs.mu.Lock()
		state, ok := cs.senderStates[id]
		if !ok {
			cs.mu.Unlock()
			writeNotFound(w)
			return
		}

		mergeStagedParams(&state.Staged, &patch)

		// Determine if activation is needed before releasing lock.
		var (
			doActivate   bool
			doDeactivate bool
			activeCopy   StagedParams
			prevActive   StagedParams
		)

		if patch.Activation != nil && patch.Activation.Mode != nil && *patch.Activation.Mode == "activate_immediate" {
			prevActive = copyStagedParams(state.Active)
			state.Active = copyStagedParams(state.Staged)
			taiTime := string(NewVersion())
			state.Active.Activation = &Activation{
				Mode:           patch.Activation.Mode,
				RequestedTime:  patch.Activation.RequestedTime,
				ActivationTime: &taiTime,
			}
			state.Staged.Activation = &Activation{
				Mode:           patch.Activation.Mode,
				RequestedTime:  patch.Activation.RequestedTime,
				ActivationTime: &taiTime,
			}

			if shouldDeactivate(state.Active) {
				doDeactivate = true
			} else {
				doActivate = true
				activeCopy = copyStagedParams(state.Active)
			}
		}

		staged := copyStagedParams(state.Staged)
		cs.mu.Unlock()

		// Fire callbacks outside the lock.
		if doDeactivate {
			if cs.onDeactivateSender != nil {
				if err := cs.onDeactivateSender(id); err != nil {
					cs.mu.Lock()
					if s, ok := cs.senderStates[id]; ok {
						s.Active = prevActive
					}
					cs.mu.Unlock()
					writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
						"code":  500,
						"error": "Internal Server Error",
						"debug": err.Error(),
					})
					return
				}
			}
		} else if doActivate {
			if cs.onActivateSender != nil {
				if err := cs.onActivateSender(id, activeCopy); err != nil {
					cs.mu.Lock()
					if s, ok := cs.senderStates[id]; ok {
						s.Active = prevActive
					}
					cs.mu.Unlock()
					writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
						"code":  500,
						"error": "Internal Server Error",
						"debug": err.Error(),
					})
					return
				}
			}
		}

		writeJSON(w, http.StatusOK, staged)

	default:
		w.Header().Set("Allow", "GET, PATCH")
		http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (cs *ConnectionServer) handleReceiverStaged(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		cs.mu.RLock()
		state, ok := cs.receiverStates[id]
		if !ok {
			cs.mu.RUnlock()
			writeNotFound(w)
			return
		}
		staged := copyStagedParams(state.Staged)
		cs.mu.RUnlock()
		writeJSON(w, http.StatusOK, staged)

	case http.MethodPatch:
		var patch StagedParams
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"code":  400,
				"error": "Bad Request",
				"debug": fmt.Sprintf("invalid JSON: %v", err),
			})
			return
		}

		// Reject unsupported or unknown activation modes before mutating state.
		if patch.Activation != nil && patch.Activation.Mode != nil {
			switch *patch.Activation.Mode {
			case "activate_immediate":
				// supported — handled below
			case "activate_scheduled_absolute", "activate_scheduled_relative":
				writeJSON(w, http.StatusNotImplemented, map[string]interface{}{
					"code":  501,
					"error": "Not Implemented",
					"debug": fmt.Sprintf("activation mode %q is not implemented", *patch.Activation.Mode),
				})
				return
			default:
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{
					"code":  400,
					"error": "Bad Request",
					"debug": fmt.Sprintf("unknown activation mode %q", *patch.Activation.Mode),
				})
				return
			}
		}

		cs.mu.Lock()
		state, ok := cs.receiverStates[id]
		if !ok {
			cs.mu.Unlock()
			writeNotFound(w)
			return
		}

		mergeStagedParams(&state.Staged, &patch)

		// Determine if activation is needed before releasing lock.
		var (
			doActivate   bool
			doDeactivate bool
			activeCopy   StagedParams
			prevActive   StagedParams
		)

		if patch.Activation != nil && patch.Activation.Mode != nil && *patch.Activation.Mode == "activate_immediate" {
			prevActive = copyStagedParams(state.Active)
			state.Active = copyStagedParams(state.Staged)
			taiTime := string(NewVersion())
			state.Active.Activation = &Activation{
				Mode:           patch.Activation.Mode,
				RequestedTime:  patch.Activation.RequestedTime,
				ActivationTime: &taiTime,
			}
			state.Staged.Activation = &Activation{
				Mode:           patch.Activation.Mode,
				RequestedTime:  patch.Activation.RequestedTime,
				ActivationTime: &taiTime,
			}

			if shouldDeactivate(state.Active) {
				doDeactivate = true
			} else {
				doActivate = true
				activeCopy = copyStagedParams(state.Active)
			}
		}

		staged := copyStagedParams(state.Staged)
		cs.mu.Unlock()

		// Fire callbacks outside the lock.
		if doDeactivate {
			if cs.onDeactivateReceiver != nil {
				if err := cs.onDeactivateReceiver(id); err != nil {
					cs.mu.Lock()
					if s, ok := cs.receiverStates[id]; ok {
						s.Active = prevActive
					}
					cs.mu.Unlock()
					writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
						"code":  500,
						"error": "Internal Server Error",
						"debug": err.Error(),
					})
					return
				}
			}
		} else if doActivate {
			if cs.onActivateReceiver != nil {
				if err := cs.onActivateReceiver(id, activeCopy); err != nil {
					cs.mu.Lock()
					if s, ok := cs.receiverStates[id]; ok {
						s.Active = prevActive
					}
					cs.mu.Unlock()
					writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
						"code":  500,
						"error": "Internal Server Error",
						"debug": err.Error(),
					})
					return
				}
			}
		}

		writeJSON(w, http.StatusOK, staged)

	default:
		w.Header().Set("Allow", "GET, PATCH")
		http.Error(w, `{"code":405,"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
	}
}

// mergeStagedParams merges non-nil fields from patch into dst.
func mergeStagedParams(dst, patch *StagedParams) {
	if patch.MasterEnable != nil {
		dst.MasterEnable = patch.MasterEnable
	}
	if patch.SenderID != nil {
		dst.SenderID = patch.SenderID
	}
	if patch.TransportFile != nil {
		dst.TransportFile = patch.TransportFile
	}
	if patch.Activation != nil {
		dst.Activation = patch.Activation
	}

	// Merge transport_params per-leg (by index).
	if patch.TransportParams != nil {
		for len(dst.TransportParams) < len(patch.TransportParams) {
			dst.TransportParams = append(dst.TransportParams, TransportParams{})
		}
		for i, ptp := range patch.TransportParams {
			mergeTransportParams(&dst.TransportParams[i], &ptp)
		}
	}
}

// mergeTransportParams merges non-nil fields from patch into dst.
func mergeTransportParams(dst *TransportParams, patch *TransportParams) {
	if patch.SourceIP != nil {
		dst.SourceIP = patch.SourceIP
	}
	if patch.DestinationIP != nil {
		dst.DestinationIP = patch.DestinationIP
	}
	if patch.DestinationPort != nil {
		dst.DestinationPort = patch.DestinationPort
	}
	if patch.RTPEnabled != nil {
		dst.RTPEnabled = patch.RTPEnabled
	}
	if patch.InterfaceIP != nil {
		dst.InterfaceIP = patch.InterfaceIP
	}
}

// shouldDeactivate returns true if the connection should be deactivated
// (master_enable is false, or any transport leg has rtp_enabled false).
func shouldDeactivate(params StagedParams) bool {
	if params.MasterEnable != nil && !*params.MasterEnable {
		return true
	}
	for _, tp := range params.TransportParams {
		if tp.RTPEnabled != nil && !*tp.RTPEnabled {
			return true
		}
	}
	return false
}

// copyStagedParams returns a deep copy of StagedParams.
func copyStagedParams(src StagedParams) StagedParams {
	dst := StagedParams{
		TransportParams: make([]TransportParams, len(src.TransportParams)),
	}
	for i, tp := range src.TransportParams {
		dst.TransportParams[i] = copyTransportParams(tp)
	}
	if src.Activation != nil {
		act := Activation{}
		if src.Activation.Mode != nil {
			m := *src.Activation.Mode
			act.Mode = &m
		}
		if src.Activation.RequestedTime != nil {
			rt := *src.Activation.RequestedTime
			act.RequestedTime = &rt
		}
		if src.Activation.ActivationTime != nil {
			at := *src.Activation.ActivationTime
			act.ActivationTime = &at
		}
		dst.Activation = &act
	}
	if src.TransportFile != nil {
		tf := *src.TransportFile
		dst.TransportFile = &tf
	}
	if src.SenderID != nil {
		sid := *src.SenderID
		dst.SenderID = &sid
	}
	if src.MasterEnable != nil {
		me := *src.MasterEnable
		dst.MasterEnable = &me
	}
	return dst
}

// copyTransportParams returns a deep copy of TransportParams.
func copyTransportParams(src TransportParams) TransportParams {
	dst := TransportParams{}
	if src.SourceIP != nil {
		v := *src.SourceIP
		dst.SourceIP = &v
	}
	if src.DestinationIP != nil {
		v := *src.DestinationIP
		dst.DestinationIP = &v
	}
	if src.DestinationPort != nil {
		v := *src.DestinationPort
		dst.DestinationPort = &v
	}
	if src.RTPEnabled != nil {
		v := *src.RTPEnabled
		dst.RTPEnabled = &v
	}
	if src.InterfaceIP != nil {
		v := *src.InterfaceIP
		dst.InterfaceIP = &v
	}
	return dst
}

