package bxf

import (
	"encoding/xml"
	"testing"
)

func TestBxfMessageMarshal(t *testing.T) {
	msg := BxfMessage{
		BxfData: BxfData{
			AsRun: &AsRun{
				Details: []AsRunDetail{
					{
						Content: Content{
							ContentId: ContentId{HouseNumber: "CLIP-001"},
							Name:      "Test Clip",
							Duration:  "PT30.000S",
						},
						ActualStartTime: "2026-03-25T10:00:00.000",
						ActualEndTime:   "2026-03-25T10:00:30.000",
						ActualDuration:  "PT30.000S",
						Status:          "Aired",
					},
				},
			},
		},
	}

	data, err := xml.MarshalIndent(msg, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	output := string(data)

	// Verify root element.
	if !containsStr(output, "<BxfMessage>") {
		t.Error("missing <BxfMessage> root element")
	}

	// Verify HouseNumber.
	if !containsStr(output, "<HouseNumber>CLIP-001</HouseNumber>") {
		t.Error("missing HouseNumber in output")
	}

	// Verify Status.
	if !containsStr(output, "<Status>Aired</Status>") {
		t.Error("missing Status in output")
	}

	// Verify Name.
	if !containsStr(output, "<Name>Test Clip</Name>") {
		t.Error("missing Name in output")
	}

	// Verify Duration.
	if !containsStr(output, "<Duration>PT30.000S</Duration>") {
		t.Error("missing Duration in output")
	}
}

func TestBxfMessageUnmarshalSchedule(t *testing.T) {
	input := `<BxfMessage>
  <BxfData>
    <Schedule>
      <ScheduleDetail>
        <Content>
          <ContentId>
            <HouseNumber>SHOW-100</HouseNumber>
          </ContentId>
          <Name>Evening News</Name>
          <Duration>PT1800.000S</Duration>
        </Content>
        <ScheduledTime>2026-03-25T18:00:00.000</ScheduledTime>
        <ScheduledDuration>PT1800.000S</ScheduledDuration>
        <Type>Program</Type>
      </ScheduleDetail>
      <ScheduleDetail>
        <Content>
          <ContentId>
            <HouseNumber>SPOT-050</HouseNumber>
          </ContentId>
          <Name>Car Ad</Name>
          <Duration>PT30.000S</Duration>
        </Content>
        <ScheduledTime>2026-03-25T18:30:00.000</ScheduledTime>
        <ScheduledDuration>PT30.000S</ScheduledDuration>
        <Type>Commercial</Type>
      </ScheduleDetail>
    </Schedule>
  </BxfData>
</BxfMessage>`

	var msg BxfMessage
	if err := xml.Unmarshal([]byte(input), &msg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if msg.BxfData.Schedule == nil {
		t.Fatal("Schedule is nil after unmarshal")
	}

	details := msg.BxfData.Schedule.Details
	if len(details) != 2 {
		t.Fatalf("expected 2 schedule details, got %d", len(details))
	}

	// First detail.
	d0 := details[0]
	if d0.Content.ContentId.HouseNumber != "SHOW-100" {
		t.Errorf("detail[0] HouseNumber = %q, want %q", d0.Content.ContentId.HouseNumber, "SHOW-100")
	}
	if d0.Content.Name != "Evening News" {
		t.Errorf("detail[0] Name = %q, want %q", d0.Content.Name, "Evening News")
	}
	if d0.ScheduledTime != "2026-03-25T18:00:00.000" {
		t.Errorf("detail[0] ScheduledTime = %q, want %q", d0.ScheduledTime, "2026-03-25T18:00:00.000")
	}
	if d0.ScheduledDuration != "PT1800.000S" {
		t.Errorf("detail[0] ScheduledDuration = %q, want %q", d0.ScheduledDuration, "PT1800.000S")
	}
	if d0.Type != "Program" {
		t.Errorf("detail[0] Type = %q, want %q", d0.Type, "Program")
	}

	// Second detail.
	d1 := details[1]
	if d1.Content.ContentId.HouseNumber != "SPOT-050" {
		t.Errorf("detail[1] HouseNumber = %q, want %q", d1.Content.ContentId.HouseNumber, "SPOT-050")
	}
	if d1.Content.Name != "Car Ad" {
		t.Errorf("detail[1] Name = %q, want %q", d1.Content.Name, "Car Ad")
	}
	if d1.Type != "Commercial" {
		t.Errorf("detail[1] Type = %q, want %q", d1.Type, "Commercial")
	}
}

func TestBxfMessageMarshalOmitsEmptySections(t *testing.T) {
	// A message with only AsRun should omit Schedule.
	msg := BxfMessage{
		BxfData: BxfData{
			AsRun: &AsRun{
				Details: []AsRunDetail{
					{
						Content: Content{
							ContentId: ContentId{HouseNumber: "X"},
							Name:      "Y",
						},
						Status: "Aired",
					},
				},
			},
		},
	}

	data, err := xml.MarshalIndent(msg, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	output := string(data)
	if containsStr(output, "<Schedule>") {
		t.Error("Schedule element should be omitted when nil")
	}
}

func TestBxfMessageRoundTrip(t *testing.T) {
	original := BxfMessage{
		BxfData: BxfData{
			Schedule: &Schedule{
				Details: []ScheduleDetail{
					{
						Content: Content{
							ContentId: ContentId{HouseNumber: "RT-001"},
							Name:      "Round Trip Test",
							Duration:  "PT60.000S",
						},
						ScheduledTime:     "2026-03-25T12:00:00.000",
						ScheduledDuration: "PT60.000S",
						Type:              "Live",
					},
				},
			},
		},
	}

	data, err := xml.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded BxfMessage
	if err := xml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.BxfData.Schedule == nil {
		t.Fatal("Schedule is nil after round trip")
	}
	if len(decoded.BxfData.Schedule.Details) != 1 {
		t.Fatalf("expected 1 detail, got %d", len(decoded.BxfData.Schedule.Details))
	}

	d := decoded.BxfData.Schedule.Details[0]
	if d.Content.ContentId.HouseNumber != "RT-001" {
		t.Errorf("HouseNumber = %q, want %q", d.Content.ContentId.HouseNumber, "RT-001")
	}
	if d.Content.Name != "Round Trip Test" {
		t.Errorf("Name = %q, want %q", d.Content.Name, "Round Trip Test")
	}
	if d.Type != "Live" {
		t.Errorf("Type = %q, want %q", d.Type, "Live")
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
