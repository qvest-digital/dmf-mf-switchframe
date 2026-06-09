package srt

import (
	"sync"
	"time"
)

// Direction indicates whether stats are for an ingest (recv) or output (send) connection.
type Direction string

const (
	DirectionRecv Direction = "recv"
	DirectionSend Direction = "send"
)

// ConnStatsSnapshot is a point-in-time snapshot of SRT connection statistics.
type ConnStatsSnapshot struct {
	Mode                 string    `json:"mode"`
	Direction            Direction `json:"direction"`
	StreamID             string    `json:"streamID"`
	RemoteAddr           string    `json:"remoteAddr,omitempty"`
	State                string    `json:"state"`
	Connected            bool      `json:"connected"`
	UptimeMs             int64     `json:"uptimeMs"`
	LatencyMs            int       `json:"latencyMs"`
	NegotiatedLatencyMs  int       `json:"negotiatedLatencyMs"`
	RTTMs                float64   `json:"rttMs"`
	RTTVarMs             float64   `json:"rttVarMs"`
	RecvRateMbps         float64   `json:"recvRateMbps"`
	LossRatePct          float64   `json:"lossRatePct"`
	PacketsReceived      int64     `json:"packetsReceived"`
	PacketsLost          int64     `json:"packetsLost"`
	PacketsDropped       int64     `json:"packetsDropped"`
	PacketsRetransmitted int64     `json:"packetsRetransmitted"`
	PacketsBelated       int64     `json:"packetsBelated"`
	RecvBufMs            float64   `json:"recvBufMs"`
	RecvBufPackets       int       `json:"recvBufPackets"`
	FlightSize           int       `json:"flightSize"`
	ReconnectCount       int       `json:"reconnectCount,omitempty"`

	// Send-side metrics (output connections)
	SendRateMbps    float64 `json:"sendRateMbps,omitempty"`
	SendLossRatePct float64 `json:"sendLossRatePct,omitempty"`
	PacketsSent     int64   `json:"packetsSent,omitempty"`
	PacketsSendLost int64   `json:"packetsSendLost,omitempty"`
	PacketsSendDrop int64   `json:"packetsSendDrop,omitempty"`
	SendBufMs       float64 `json:"sendBufMs,omitempty"`
	SendBufPackets  int     `json:"sendBufPackets,omitempty"`
}

// ConnStats tracks live SRT connection metrics. Thread-safe.
type ConnStats struct {
	mu sync.RWMutex

	mode      string
	direction Direction
	streamID  string
	latencyMs int
	startTime time.Time

	// Connection state
	connected           bool
	remoteAddr          string
	negotiatedLatencyMs int
	reconnectCount      int

	// Recv-side metrics (ingest connections)
	rttMs                float64
	rttVarMs             float64
	recvRateMbps         float64
	lossRatePct          float64
	packetsReceived      int64
	packetsLost          int64
	packetsDropped       int64
	packetsRetransmitted int64
	packetsBelated       int64
	recvBufMs            float64
	recvBufPackets       int
	flightSize           int

	// Send-side metrics (output connections)
	sendRateMbps    float64
	sendLossRatePct float64
	packetsSent     int64
	packetsSendLost int64
	packetsSendDrop int64
	sendBufMs       float64
	sendBufPackets  int
}

// NewConnStats creates a ConnStats tracker for an SRT connection.
// The direction is derived from mode: "send" maps to DirectionSend,
// everything else (listener, caller, empty) maps to DirectionRecv.
func NewConnStats(mode, streamID string, latencyMs int) *ConnStats {
	dir := DirectionRecv
	if mode == "send" {
		dir = DirectionSend
	}
	return &ConnStats{
		mode:      mode,
		direction: dir,
		streamID:  streamID,
		latencyMs: latencyMs,
		startTime: time.Now(),
	}
}

// SetConnected marks the connection as established.
func (cs *ConnStats) SetConnected(remoteAddr string, negLatencyMs int) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.connected = true
	cs.remoteAddr = remoteAddr
	cs.negotiatedLatencyMs = negLatencyMs
}

// SetDisconnected marks the connection as lost and increments the reconnect counter.
func (cs *ConnStats) SetDisconnected() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.connected = false
	cs.reconnectCount++
}

// StatsUpdate holds a batch of SRT connection metrics for ConnStats.Update.
// The Packets* fields are per-poll INTERVAL DELTAS (srtgo Stats(true)
// semantics); ConnStats accumulates them into lifetime totals. Rate and
// buffer fields are window/gauge values that replace the previous reading.
type StatsUpdate struct {
	RTTMs           float64
	RTTVarMs        float64
	RecvRateMbps    float64
	LossRatePct     float64
	PacketsReceived int64
	PacketsLost     int64
	PacketsDropped  int64
	PacketsRetrans  int64
	PacketsBelated  int64
	RecvBufMs       float64
	RecvBufPackets  int
	FlightSize      int
}

// Update applies a bulk stats update from srtgo. Packet counters are interval
// deltas accumulated into lifetime totals (the snapshot fields are exposed as
// cumulative packetsReceived/packetsLost/... totals); rates and gauges
// replace the previous window value.
func (cs *ConnStats) Update(u StatsUpdate) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.rttMs = u.RTTMs
	cs.rttVarMs = u.RTTVarMs
	cs.recvRateMbps = u.RecvRateMbps
	cs.lossRatePct = u.LossRatePct
	cs.packetsReceived += u.PacketsReceived
	cs.packetsLost += u.PacketsLost
	cs.packetsDropped += u.PacketsDropped
	cs.packetsRetransmitted += u.PacketsRetrans
	cs.packetsBelated += u.PacketsBelated
	cs.recvBufMs = u.RecvBufMs
	cs.recvBufPackets = u.RecvBufPackets
	cs.flightSize = u.FlightSize
}

// SendStatsUpdate holds a batch of SRT send-side metrics for ConnStats.UpdateSend.
type SendStatsUpdate struct {
	RTTMs           float64
	RTTVarMs        float64
	SendRateMbps    float64
	SendLossRatePct float64
	PacketsSent     int64
	PacketsSendLost int64
	PacketsSendDrop int64
	SendBufMs       float64
	SendBufPackets  int
}

// UpdateSend applies a bulk send-side stats update from srtgo.
func (cs *ConnStats) UpdateSend(u SendStatsUpdate) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.rttMs = u.RTTMs       // RTT is shared, used by both recv and send
	cs.rttVarMs = u.RTTVarMs // RTTVar is also shared
	cs.sendRateMbps = u.SendRateMbps
	cs.sendLossRatePct = u.SendLossRatePct
	cs.packetsSent = u.PacketsSent
	cs.packetsSendLost = u.PacketsSendLost
	cs.packetsSendDrop = u.PacketsSendDrop
	cs.sendBufMs = u.SendBufMs
	cs.sendBufPackets = u.SendBufPackets
}

// Snapshot returns a point-in-time copy of connection statistics.
func (cs *ConnStats) Snapshot() ConnStatsSnapshot {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	state := "disconnected"
	if cs.connected {
		state = "connected"
	}

	return ConnStatsSnapshot{
		Mode:                 cs.mode,
		Direction:            cs.direction,
		StreamID:             cs.streamID,
		RemoteAddr:           cs.remoteAddr,
		State:                state,
		Connected:            cs.connected,
		UptimeMs:             time.Since(cs.startTime).Milliseconds(),
		LatencyMs:            cs.latencyMs,
		NegotiatedLatencyMs:  cs.negotiatedLatencyMs,
		RTTMs:                cs.rttMs,
		RTTVarMs:             cs.rttVarMs,
		RecvRateMbps:         cs.recvRateMbps,
		LossRatePct:          cs.lossRatePct,
		PacketsReceived:      cs.packetsReceived,
		PacketsLost:          cs.packetsLost,
		PacketsDropped:       cs.packetsDropped,
		PacketsRetransmitted: cs.packetsRetransmitted,
		PacketsBelated:       cs.packetsBelated,
		RecvBufMs:            cs.recvBufMs,
		RecvBufPackets:       cs.recvBufPackets,
		FlightSize:           cs.flightSize,
		ReconnectCount:       cs.reconnectCount,
		SendRateMbps:         cs.sendRateMbps,
		SendLossRatePct:      cs.sendLossRatePct,
		PacketsSent:          cs.packetsSent,
		PacketsSendLost:      cs.packetsSendLost,
		PacketsSendDrop:      cs.packetsSendDrop,
		SendBufMs:            cs.sendBufMs,
		SendBufPackets:       cs.sendBufPackets,
	}
}

// ToSRTSourceInfo returns a broadcast-friendly subset for ControlRoomState.
func (cs *ConnStats) ToSRTSourceInfo() SRTSourceInfo {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	return SRTSourceInfo{
		Mode:                 cs.mode,
		StreamID:             cs.streamID,
		RemoteAddr:           cs.remoteAddr,
		LatencyMs:            cs.latencyMs,
		NegotiatedLatencyMs:  cs.negotiatedLatencyMs,
		RTTMs:                cs.rttMs,
		RTTVarMs:             cs.rttVarMs,
		LossRate:             cs.lossRatePct,
		BitrateKbps:          cs.recvRateMbps * 1000,
		RecvBufMs:            cs.recvBufMs,
		RecvBufPackets:       cs.recvBufPackets,
		FlightSize:           cs.flightSize,
		Connected:            cs.connected,
		UptimeMs:             time.Since(cs.startTime).Milliseconds(),
		PacketsReceived:      cs.packetsReceived,
		PacketsLost:          cs.packetsLost,
		PacketsDropped:       cs.packetsDropped,
		PacketsRetransmitted: cs.packetsRetransmitted,
		PacketsBelated:       cs.packetsBelated,
		ReconnectCount:       cs.reconnectCount,
	}
}

// StatsManager tracks stats for all SRT sources. Thread-safe.
// Implements debug.SnapshotProvider.
type StatsManager struct {
	mu    sync.RWMutex
	stats map[string]*ConnStats
}

// NewStatsManager creates a StatsManager.
func NewStatsManager() *StatsManager {
	return &StatsManager{
		stats: make(map[string]*ConnStats),
	}
}

// Get returns the ConnStats for the given key, or nil and false if not found.
// Unlike GetOrCreate, this does not create phantom entries for unknown keys.
func (sm *StatsManager) Get(key string) (*ConnStats, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	cs, ok := sm.stats[key]
	return cs, ok
}

// GetOrCreate returns the ConnStats for the given key, creating one if needed.
// Newly created ConnStats are initialized with zero-value mode/streamID/latency;
// the caller should configure them via SetConnected/Update.
func (sm *StatsManager) GetOrCreate(key string) *ConnStats {
	sm.mu.RLock()
	cs, ok := sm.stats[key]
	sm.mu.RUnlock()
	if ok {
		return cs
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()
	// Double-check after acquiring write lock.
	if cs, ok = sm.stats[key]; ok {
		return cs
	}
	cs = NewConnStats("", "", 0)
	sm.stats[key] = cs
	return cs
}

// Create creates a ConnStats entry with the given configuration.
// If an entry already exists for the key, it is replaced.
// Used to pre-populate stats for SRT callers before the connection succeeds,
// so the UI can display mode/streamID/latency immediately.
func (sm *StatsManager) Create(key, mode, streamID string, latencyMs int) *ConnStats {
	cs := NewConnStats(mode, streamID, latencyMs)
	sm.mu.Lock()
	sm.stats[key] = cs
	sm.mu.Unlock()
	return cs
}

// Remove deletes stats for the given key.
func (sm *StatsManager) Remove(key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.stats, key)
}

// DebugSnapshot returns a snapshot of all SRT source stats.
func (sm *StatsManager) DebugSnapshot() map[string]any {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sources := make(map[string]ConnStatsSnapshot, len(sm.stats))
	for key, cs := range sm.stats {
		sources[key] = cs.Snapshot()
	}

	return map[string]any{
		"srt_sources": sources,
	}
}
