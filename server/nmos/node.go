package nmos

import (
	"encoding/json"
	"net/http"
	"strings"
)

const nodeAPIBase = "/x-nmos/node/v1.3/"

// NodeServer serves the NMOS IS-04 Node API (read-only).
type NodeServer struct {
	store *ResourceStore
	mux   *http.ServeMux
}

// NewNodeServer creates a NodeServer and registers all routes.
func NewNodeServer(store *ResourceStore) *NodeServer {
	s := &NodeServer{
		store: store,
		mux:   http.NewServeMux(),
	}
	s.mux.HandleFunc(nodeAPIBase, s.handleRoot)
	s.mux.HandleFunc(nodeAPIBase+"self", s.handleSelf)
	s.mux.HandleFunc(nodeAPIBase+"devices/", s.handleDevices)
	s.mux.HandleFunc(nodeAPIBase+"devices", s.handleDevices)
	s.mux.HandleFunc(nodeAPIBase+"sources/", s.handleSources)
	s.mux.HandleFunc(nodeAPIBase+"sources", s.handleSources)
	s.mux.HandleFunc(nodeAPIBase+"flows/", s.handleFlows)
	s.mux.HandleFunc(nodeAPIBase+"flows", s.handleFlows)
	s.mux.HandleFunc(nodeAPIBase+"senders/", s.handleSenders)
	s.mux.HandleFunc(nodeAPIBase+"senders", s.handleSenders)
	s.mux.HandleFunc(nodeAPIBase+"receivers/", s.handleReceivers)
	s.mux.HandleFunc(nodeAPIBase+"receivers", s.handleReceivers)
	return s
}

// Handler returns the HTTP handler for the Node API.
func (s *NodeServer) Handler() http.Handler {
	return s.mux
}

func (s *NodeServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []string{
		"self/",
		"sources/",
		"flows/",
		"devices/",
		"senders/",
		"receivers/",
	})
}

func (s *NodeServer) handleSelf(w http.ResponseWriter, r *http.Request) {
	node := s.store.GetNode()
	if node == nil {
		writeNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (s *NodeServer) handleDevices(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "devices")
	if id == "" {
		writeJSON(w, http.StatusOK, s.store.ListDevices())
		return
	}
	dev := s.store.GetDevice(id)
	if dev == nil {
		writeNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, dev)
}

func (s *NodeServer) handleSources(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "sources")
	if id == "" {
		writeJSON(w, http.StatusOK, s.store.ListSources())
		return
	}
	src := s.store.GetSource(id)
	if src == nil {
		writeNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, src)
}

func (s *NodeServer) handleFlows(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "flows")
	if id == "" {
		writeJSON(w, http.StatusOK, s.store.ListFlows())
		return
	}
	flow := s.store.GetFlow(id)
	if flow == nil {
		writeNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, flow)
}

func (s *NodeServer) handleSenders(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "senders")
	if id == "" {
		writeJSON(w, http.StatusOK, s.store.ListSenders())
		return
	}
	snd := s.store.GetSender(id)
	if snd == nil {
		writeNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, snd)
}

func (s *NodeServer) handleReceivers(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "receivers")
	if id == "" {
		writeJSON(w, http.StatusOK, s.store.ListReceivers())
		return
	}
	recv := s.store.GetReceiver(id)
	if recv == nil {
		writeNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, recv)
}

// extractID extracts a resource ID from the URL path. Given a path like
// "/x-nmos/node/v1.3/devices/dev-001" and resource "devices", it returns
// "dev-001". Returns "" for collection requests (no trailing ID).
func extractID(path, resource string) string {
	prefix := nodeAPIBase + resource + "/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.TrimPrefix(path, prefix)
	// Strip any trailing slash.
	id = strings.TrimSuffix(id, "/")
	return id
}

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// writeNotFound writes a 404 JSON response.
func writeNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]interface{}{
		"code":  404,
		"error": "Not Found",
		"debug": "resource not found",
	})
}
