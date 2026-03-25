package st2110

import (
	"testing"
)

func TestST2110Snapshot_ImplementsSnapshotProvider(t *testing.T) {
	snap := NewST2110Snapshot()
	result := snap.DebugSnapshot()

	if _, ok := result["ptp"]; !ok {
		t.Fatal("DebugSnapshot missing 'ptp' key")
	}
	if _, ok := result["sessions"]; !ok {
		t.Fatal("DebugSnapshot missing 'sessions' key")
	}
}

func TestST2110Snapshot_AddSession(t *testing.T) {
	snap := NewST2110Snapshot()
	snap.AddSession("cam1", "video_rx", SessionStats{
		FramesReceived: 100,
		FramesDropped:  2,
		FramesCorrupt:  1,
		PacketsLost:    5,
	})

	result := snap.DebugSnapshot()
	sessions, ok := result["sessions"].([]map[string]any)
	if !ok {
		t.Fatalf("sessions is not []map[string]any, got %T", result["sessions"])
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	s := sessions[0]
	if s["name"] != "cam1" {
		t.Errorf("expected name 'cam1', got %v", s["name"])
	}
	if s["type"] != "video_rx" {
		t.Errorf("expected type 'video_rx', got %v", s["type"])
	}
	if s["frames_received"] != uint64(100) {
		t.Errorf("expected frames_received 100, got %v", s["frames_received"])
	}
	if s["frames_dropped"] != uint64(2) {
		t.Errorf("expected frames_dropped 2, got %v", s["frames_dropped"])
	}
	if s["frames_corrupt"] != uint64(1) {
		t.Errorf("expected frames_corrupt 1, got %v", s["frames_corrupt"])
	}
	if s["packets_lost"] != uint64(5) {
		t.Errorf("expected packets_lost 5, got %v", s["packets_lost"])
	}
}

func TestST2110Snapshot_RemoveSession(t *testing.T) {
	snap := NewST2110Snapshot()
	snap.AddSession("cam1", "video_rx", SessionStats{FramesReceived: 10})
	snap.AddSession("cam2", "audio_rx", SessionStats{FramesReceived: 20})

	snap.RemoveSession("cam1")

	result := snap.DebugSnapshot()
	sessions := result["sessions"].([]map[string]any)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after removal, got %d", len(sessions))
	}
	if sessions[0]["name"] != "cam2" {
		t.Errorf("expected remaining session 'cam2', got %v", sessions[0]["name"])
	}
}

func TestST2110Snapshot_UpdateSession(t *testing.T) {
	snap := NewST2110Snapshot()
	snap.AddSession("cam1", "video_rx", SessionStats{FramesReceived: 10})
	snap.AddSession("cam1", "video_rx", SessionStats{FramesReceived: 50})

	result := snap.DebugSnapshot()
	sessions := result["sessions"].([]map[string]any)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session (not duplicated), got %d", len(sessions))
	}
	if sessions[0]["frames_received"] != uint64(50) {
		t.Errorf("expected updated frames_received 50, got %v", sessions[0]["frames_received"])
	}
}

func TestST2110Snapshot_PTPStatus(t *testing.T) {
	snap := NewST2110Snapshot()
	snap.SetPTP(PTPStatus{
		Locked:        true,
		GrandmasterID: "00:11:22:33:44:55:66:77",
		OffsetNs:      -42,
		Domain:        127,
	})

	result := snap.DebugSnapshot()
	ptp, ok := result["ptp"].(map[string]any)
	if !ok {
		t.Fatalf("ptp is not map[string]any, got %T", result["ptp"])
	}
	if ptp["locked"] != true {
		t.Errorf("expected locked true, got %v", ptp["locked"])
	}
	if ptp["grandmaster_id"] != "00:11:22:33:44:55:66:77" {
		t.Errorf("expected grandmaster_id, got %v", ptp["grandmaster_id"])
	}
	if ptp["offset_ns"] != int64(-42) {
		t.Errorf("expected offset_ns -42, got %v", ptp["offset_ns"])
	}
	if ptp["domain"] != 127 {
		t.Errorf("expected domain 127, got %v", ptp["domain"])
	}
}
