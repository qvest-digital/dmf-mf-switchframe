package internal

import (
	"encoding/json"
	"testing"
)

func TestCommsStateJSON(t *testing.T) {
	original := CommsState{
		Active: true,
		Participants: []CommsParticipant{
			{
				OperatorID: "op-1",
				Name:       "Director",
				Muted:      false,
				Speaking:   true,
			},
			{
				OperatorID: "op-2",
				Name:       "Audio Engineer",
				Muted:      true,
				Speaking:   false,
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded CommsState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Active != original.Active {
		t.Errorf("Active = %v, want %v", decoded.Active, original.Active)
	}
	if len(decoded.Participants) != 2 {
		t.Fatalf("Participants len = %d, want 2", len(decoded.Participants))
	}

	p0 := decoded.Participants[0]
	if p0.OperatorID != "op-1" {
		t.Errorf("Participants[0].OperatorID = %q, want %q", p0.OperatorID, "op-1")
	}
	if p0.Name != "Director" {
		t.Errorf("Participants[0].Name = %q, want %q", p0.Name, "Director")
	}
	if p0.Muted != false {
		t.Errorf("Participants[0].Muted = %v, want false", p0.Muted)
	}
	if p0.Speaking != true {
		t.Errorf("Participants[0].Speaking = %v, want true", p0.Speaking)
	}

	p1 := decoded.Participants[1]
	if p1.OperatorID != "op-2" {
		t.Errorf("Participants[1].OperatorID = %q, want %q", p1.OperatorID, "op-2")
	}
	if p1.Muted != true {
		t.Errorf("Participants[1].Muted = %v, want true", p1.Muted)
	}

	// Verify JSON field names
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if _, ok := raw["active"]; !ok {
		t.Error("JSON missing 'active' field")
	}
	if _, ok := raw["participants"]; !ok {
		t.Error("JSON missing 'participants' field")
	}

	participants := raw["participants"].([]interface{})
	p0raw := participants[0].(map[string]interface{})
	if _, ok := p0raw["operatorId"]; !ok {
		t.Error("participant JSON missing 'operatorId' field")
	}
}

func TestCommsStateOmitEmpty(t *testing.T) {
	state := ControlRoomState{
		ProgramSource: "cam1",
		PreviewSource: "cam2",
		// Comms is nil — should be omitted
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := raw["comms"]; ok {
		t.Error("comms field should be omitted when nil, but was present")
	}

	// Now set Comms and verify it's present
	state.Comms = &CommsState{
		Active:       true,
		Participants: []CommsParticipant{},
	}

	data, err = json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal with comms: %v", err)
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal with comms: %v", err)
	}

	if _, ok := raw["comms"]; !ok {
		t.Error("comms field should be present when set, but was omitted")
	}
}
