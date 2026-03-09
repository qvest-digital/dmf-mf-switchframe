package internal

import (
	"encoding/json"
	"testing"
)

func TestControlRoomState_SCTE35_JSON(t *testing.T) {
	state := ControlRoomState{
		ProgramSource: "cam1",
		SCTE35: &SCTE35State{
			Enabled: true,
			ActiveEvents: map[uint32]SCTE35Active{
				1: {EventID: 1, CommandType: "splice_insert", IsOut: true},
			},
			HeartbeatOK: true,
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ControlRoomState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.SCTE35 == nil {
		t.Fatal("SCTE35 field nil after round-trip")
	}
	if !decoded.SCTE35.Enabled {
		t.Fatal("expected Enabled=true")
	}
	if len(decoded.SCTE35.ActiveEvents) != 1 {
		t.Fatalf("expected 1 active event, got %d", len(decoded.SCTE35.ActiveEvents))
	}

	// omitempty: nil SCTE35 should not appear in JSON
	state2 := ControlRoomState{ProgramSource: "cam1"}
	data2, _ := json.Marshal(state2)
	if json.Valid(data2) {
		var m map[string]interface{}
		_ = json.Unmarshal(data2, &m)
		if _, ok := m["scte35"]; ok {
			t.Fatal("nil SCTE35 should be omitted from JSON")
		}
	}
}

func TestSCTE35State_JSON_RoundTrip(t *testing.T) {
	durMs := int64(60000)
	remaining := int64(50000)
	state := SCTE35State{
		Enabled: true,
		ActiveEvents: map[uint32]SCTE35Active{
			42: {
				EventID:     42,
				CommandType: "splice_insert",
				IsOut:       true,
				DurationMs:  &durMs,
				ElapsedMs:   10000,
				RemainingMs: &remaining,
				AutoReturn:  true,
				Held:        false,
				StartedAt:   1709856000000,
				Descriptors: []SCTE35DescriptorInfo{
					{
						SegEventID:           1,
						SegmentationType:     0x34,
						SegmentationTypeName: "Provider Placement Opportunity Start",
						UPIDType:             0x0F,
						UPIDTypeName:         "URI",
						UPID:                 "https://example.com/ad/1",
					},
				},
			},
		},
		EventLog: []SCTE35Event{
			{
				EventID:     42,
				CommandType: "splice_insert",
				IsOut:       true,
				DurationMs:  &durMs,
				AutoReturn:  true,
				Timestamp:   1709856000000,
				Status:      "injected",
			},
		},
		HeartbeatOK: true,
		Config: SCTE35Config{
			HeartbeatIntervalMs: 5000,
			DefaultPreRollMs:    4000,
			PID:                 258,
			VerifyEncoding:      true,
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded SCTE35State
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !decoded.Enabled {
		t.Fatal("expected Enabled=true")
	}
	active, ok := decoded.ActiveEvents[42]
	if !ok {
		t.Fatal("event 42 missing")
	}
	if active.CommandType != "splice_insert" {
		t.Fatalf("expected splice_insert, got %s", active.CommandType)
	}
	if active.DurationMs == nil || *active.DurationMs != 60000 {
		t.Fatal("duration mismatch")
	}
	if len(active.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(active.Descriptors))
	}
	if active.Descriptors[0].UPID != "https://example.com/ad/1" {
		t.Fatalf("UPID mismatch: %s", active.Descriptors[0].UPID)
	}
	if decoded.Config.PID != 258 {
		t.Fatalf("expected PID 258, got %d", decoded.Config.PID)
	}
}
