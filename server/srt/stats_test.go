package srt

import (
	"testing"
	"time"
)

func TestConnStatsSnapshot(t *testing.T) {
	cs := NewConnStats("listener", "live/cam1", 120)

	// Ensure some time passes so UptimeMs > 0.
	time.Sleep(2 * time.Millisecond)

	snap := cs.Snapshot()
	if snap.Mode != "listener" {
		t.Errorf("Mode = %q, want %q", snap.Mode, "listener")
	}
	if snap.StreamID != "live/cam1" {
		t.Errorf("StreamID = %q, want %q", snap.StreamID, "live/cam1")
	}
	if snap.LatencyMs != 120 {
		t.Errorf("LatencyMs = %d, want 120", snap.LatencyMs)
	}
	if snap.State != "disconnected" {
		t.Errorf("State = %q, want %q", snap.State, "disconnected")
	}
	if snap.Connected {
		t.Error("Connected = true, want false")
	}
	if snap.UptimeMs <= 0 {
		t.Errorf("UptimeMs = %d, want > 0", snap.UptimeMs)
	}
}

func TestConnStatsSetConnected(t *testing.T) {
	cs := NewConnStats("caller", "live/cam2", 200)

	cs.SetConnected("192.168.1.10:1234", 250)

	snap := cs.Snapshot()
	if !snap.Connected {
		t.Error("Connected = false, want true")
	}
	if snap.State != "connected" {
		t.Errorf("State = %q, want %q", snap.State, "connected")
	}
	if snap.RemoteAddr != "192.168.1.10:1234" {
		t.Errorf("RemoteAddr = %q, want %q", snap.RemoteAddr, "192.168.1.10:1234")
	}
	if snap.NegotiatedLatencyMs != 250 {
		t.Errorf("NegotiatedLatencyMs = %d, want 250", snap.NegotiatedLatencyMs)
	}
}

func TestConnStatsDisconnect(t *testing.T) {
	cs := NewConnStats("listener", "live/cam3", 120)

	cs.SetConnected("10.0.0.1:5000", 120)
	snap := cs.Snapshot()
	if !snap.Connected {
		t.Fatal("expected connected after SetConnected")
	}

	cs.SetDisconnected()
	snap = cs.Snapshot()
	if snap.Connected {
		t.Error("Connected = true after SetDisconnected, want false")
	}
	if snap.State != "disconnected" {
		t.Errorf("State = %q, want %q", snap.State, "disconnected")
	}
	if snap.ReconnectCount != 1 {
		t.Errorf("ReconnectCount = %d, want 1", snap.ReconnectCount)
	}

	// Reconnect and disconnect again to verify counter increments.
	cs.SetConnected("10.0.0.2:5001", 150)
	cs.SetDisconnected()
	snap = cs.Snapshot()
	if snap.ReconnectCount != 2 {
		t.Errorf("ReconnectCount = %d, want 2", snap.ReconnectCount)
	}
}

func TestConnStatsUpdate(t *testing.T) {
	cs := NewConnStats("listener", "live/cam4", 120)
	cs.SetConnected("10.0.0.1:5000", 120)

	cs.Update(StatsUpdate{
		RTTMs:           5.2,
		RTTVarMs:        1.1,
		RecvRateMbps:    12.5,
		LossRatePct:     0.01,
		PacketsReceived: 100000,
		PacketsLost:     100,
		PacketsDropped:  5,
		PacketsRetrans:  50,
		PacketsBelated:  3,
		RecvBufMs:       45.0,
		RecvBufPackets:  32,
		FlightSize:      10,
	})

	snap := cs.Snapshot()
	if snap.RTTMs != 5.2 {
		t.Errorf("RTTMs = %f, want 5.2", snap.RTTMs)
	}
	if snap.RTTVarMs != 1.1 {
		t.Errorf("RTTVarMs = %f, want 1.1", snap.RTTVarMs)
	}
	if snap.RecvRateMbps != 12.5 {
		t.Errorf("RecvRateMbps = %f, want 12.5", snap.RecvRateMbps)
	}
	if snap.LossRatePct != 0.01 {
		t.Errorf("LossRatePct = %f, want 0.01", snap.LossRatePct)
	}
	if snap.PacketsReceived != 100000 {
		t.Errorf("PacketsReceived = %d, want 100000", snap.PacketsReceived)
	}
	if snap.PacketsLost != 100 {
		t.Errorf("PacketsLost = %d, want 100", snap.PacketsLost)
	}
	if snap.PacketsDropped != 5 {
		t.Errorf("PacketsDropped = %d, want 5", snap.PacketsDropped)
	}
	if snap.PacketsRetransmitted != 50 {
		t.Errorf("PacketsRetransmitted = %d, want 50", snap.PacketsRetransmitted)
	}
	if snap.PacketsBelated != 3 {
		t.Errorf("PacketsBelated = %d, want 3", snap.PacketsBelated)
	}
	if snap.RecvBufMs != 45.0 {
		t.Errorf("RecvBufMs = %f, want 45.0", snap.RecvBufMs)
	}
	if snap.RecvBufPackets != 32 {
		t.Errorf("RecvBufPackets = %d, want 32", snap.RecvBufPackets)
	}
	if snap.FlightSize != 10 {
		t.Errorf("FlightSize = %d, want 10", snap.FlightSize)
	}
}

func TestConnStatsToSRTSourceInfo(t *testing.T) {
	cs := NewConnStats("listener", "live/cam5", 120)
	cs.SetConnected("10.0.0.1:5000", 120)
	cs.Update(StatsUpdate{
		RTTMs:           5.2,
		RTTVarMs:        1.1,
		RecvRateMbps:    12.5,
		LossRatePct:     0.5,
		PacketsReceived: 100000,
		PacketsLost:     500,
		PacketsDropped:  5,
		PacketsRetrans:  50,
		PacketsBelated:  3,
		RecvBufMs:       45.0,
		RecvBufPackets:  32,
		FlightSize:      10,
	})

	info := cs.ToSRTSourceInfo()
	if info.Mode != "listener" {
		t.Errorf("Mode = %q, want %q", info.Mode, "listener")
	}
	if info.StreamID != "live/cam5" {
		t.Errorf("StreamID = %q, want %q", info.StreamID, "live/cam5")
	}
	if info.RemoteAddr != "10.0.0.1:5000" {
		t.Errorf("RemoteAddr = %q, want %q", info.RemoteAddr, "10.0.0.1:5000")
	}
	if info.LatencyMs != 120 {
		t.Errorf("LatencyMs = %d, want 120", info.LatencyMs)
	}
	if info.RTTMs != 5.2 {
		t.Errorf("RTTMs = %f, want 5.2", info.RTTMs)
	}
	if info.LossRate != 0.5 {
		t.Errorf("LossRate = %f, want 0.5", info.LossRate)
	}
	// BitrateKbps = recvRateMbps * 1000
	if info.BitrateKbps != 12500 {
		t.Errorf("BitrateKbps = %f, want 12500", info.BitrateKbps)
	}
	if info.RecvBufMs != 45.0 {
		t.Errorf("RecvBufMs = %f, want 45.0", info.RecvBufMs)
	}
	if !info.Connected {
		t.Error("Connected = false, want true")
	}
}

func TestStatsManagerGetOrCreate(t *testing.T) {
	sm := NewStatsManager()

	cs1 := sm.GetOrCreate("srt:cam1")
	if cs1 == nil {
		t.Fatal("GetOrCreate returned nil")
	}

	// Same key returns same instance.
	cs2 := sm.GetOrCreate("srt:cam1")
	if cs1 != cs2 {
		t.Error("GetOrCreate returned different instance for same key")
	}
}

func TestStatsManagerDebugSnapshot(t *testing.T) {
	sm := NewStatsManager()

	cs := sm.GetOrCreate("srt:cam1")
	cs.SetConnected("10.0.0.1:5000", 120)

	snap := sm.DebugSnapshot()
	sources, ok := snap["srt_sources"]
	if !ok {
		t.Fatal("DebugSnapshot missing srt_sources key")
	}

	sourcesMap, ok := sources.(map[string]ConnStatsSnapshot)
	if !ok {
		t.Fatalf("srt_sources is %T, want map[string]ConnStatsSnapshot", sources)
	}

	camSnap, ok := sourcesMap["srt:cam1"]
	if !ok {
		t.Fatal("srt_sources missing srt:cam1")
	}
	if !camSnap.Connected {
		t.Error("srt:cam1 Connected = false, want true")
	}
}

func TestStatsManagerRemove(t *testing.T) {
	sm := NewStatsManager()

	sm.GetOrCreate("srt:cam1")
	sm.Remove("srt:cam1")

	snap := sm.DebugSnapshot()
	sources := snap["srt_sources"].(map[string]ConnStatsSnapshot)
	if _, ok := sources["srt:cam1"]; ok {
		t.Error("srt:cam1 still present after Remove")
	}
}

func TestConnStatsConcurrency(t *testing.T) {
	cs := NewConnStats("listener", "live/cam1", 120)

	done := make(chan struct{})

	// Writer goroutine.
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			cs.SetConnected("10.0.0.1:5000", 120)
			cs.Update(StatsUpdate{RTTMs: 5.2, RTTVarMs: 1.1, RecvRateMbps: 12.5, LossRatePct: 0.01, PacketsReceived: int64(i), RecvBufMs: 45.0, RecvBufPackets: 32, FlightSize: 10})
			cs.SetDisconnected()
		}
	}()

	// Reader goroutine.
	for i := 0; i < 1000; i++ {
		_ = cs.Snapshot()
		_ = cs.ToSRTSourceInfo()
	}

	<-done
}

func TestStatsManagerConcurrency(t *testing.T) {
	sm := NewStatsManager()

	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			cs := sm.GetOrCreate("srt:cam1")
			cs.SetConnected("10.0.0.1:5000", 120)
			_ = sm.DebugSnapshot()
		}
	}()

	for i := 0; i < 1000; i++ {
		sm.GetOrCreate("srt:cam2")
		sm.Remove("srt:cam2")
	}

	<-done
}

func TestConnStatsUptimeGrowsOverTime(t *testing.T) {
	cs := NewConnStats("listener", "live/cam1", 120)
	snap1 := cs.Snapshot()
	time.Sleep(5 * time.Millisecond)
	snap2 := cs.Snapshot()
	if snap2.UptimeMs <= snap1.UptimeMs {
		t.Errorf("UptimeMs did not increase: %d -> %d", snap1.UptimeMs, snap2.UptimeMs)
	}
}
