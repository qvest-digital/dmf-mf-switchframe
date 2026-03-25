package bxf

import (
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/playout"
)

func TestExportAsRun(t *testing.T) {
	baseTime := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	scheduled := baseTime.Add(-1 * time.Second)

	events := []playout.AsRunEvent{
		{
			Timestamp:   baseTime,
			Type:        playout.AsRunItemStart,
			ChannelID:   1,
			ItemID:      "CLIP-001",
			Title:       "Opening",
			ScheduledTS: &scheduled,
		},
		{
			Timestamp:        baseTime.Add(30 * time.Second),
			Type:             playout.AsRunItemEnd,
			ChannelID:        1,
			ItemID:           "CLIP-001",
			Title:            "Opening",
			ActualDurationMs: 30000,
		},
		{
			Timestamp:   baseTime.Add(30 * time.Second),
			Type:        playout.AsRunItemStart,
			ChannelID:   1,
			ItemID:      "CLIP-002",
			Title:       "Interview",
			ScheduledTS: nil,
		},
		{
			Timestamp:        baseTime.Add(90 * time.Second),
			Type:             playout.AsRunItemEnd,
			ChannelID:        1,
			ItemID:           "CLIP-002",
			Title:            "Interview",
			ActualDurationMs: 60000,
		},
	}

	msg := ExportAsRun(events)

	if msg.BxfData.AsRun == nil {
		t.Fatal("AsRun is nil")
	}

	details := msg.BxfData.AsRun.Details
	if len(details) != 2 {
		t.Fatalf("expected 2 details, got %d", len(details))
	}

	// First detail.
	d0 := details[0]
	if d0.Content.ContentId.HouseNumber != "CLIP-001" {
		t.Errorf("detail[0] HouseNumber = %q, want %q", d0.Content.ContentId.HouseNumber, "CLIP-001")
	}
	if d0.Content.Name != "Opening" {
		t.Errorf("detail[0] Name = %q, want %q", d0.Content.Name, "Opening")
	}
	if d0.Status != "Aired" {
		t.Errorf("detail[0] Status = %q, want %q", d0.Status, "Aired")
	}
	if d0.ActualStartTime != "2026-03-25T10:00:00.000" {
		t.Errorf("detail[0] ActualStartTime = %q, want %q", d0.ActualStartTime, "2026-03-25T10:00:00.000")
	}
	if d0.ActualEndTime != "2026-03-25T10:00:30.000" {
		t.Errorf("detail[0] ActualEndTime = %q, want %q", d0.ActualEndTime, "2026-03-25T10:00:30.000")
	}
	if d0.ActualDuration != "PT30.000S" {
		t.Errorf("detail[0] ActualDuration = %q, want %q", d0.ActualDuration, "PT30.000S")
	}
	if d0.ScheduledTime != "2026-03-25T09:59:59.000" {
		t.Errorf("detail[0] ScheduledTime = %q, want %q", d0.ScheduledTime, "2026-03-25T09:59:59.000")
	}

	// Second detail.
	d1 := details[1]
	if d1.Content.ContentId.HouseNumber != "CLIP-002" {
		t.Errorf("detail[1] HouseNumber = %q, want %q", d1.Content.ContentId.HouseNumber, "CLIP-002")
	}
	if d1.Content.Name != "Interview" {
		t.Errorf("detail[1] Name = %q, want %q", d1.Content.Name, "Interview")
	}
	if d1.ActualDuration != "PT60.000S" {
		t.Errorf("detail[1] ActualDuration = %q, want %q", d1.ActualDuration, "PT60.000S")
	}
	// No scheduled time for second item.
	if d1.ScheduledTime != "" {
		t.Errorf("detail[1] ScheduledTime = %q, want empty", d1.ScheduledTime)
	}
}

func TestExportAsRunEmptyEvents(t *testing.T) {
	msg := ExportAsRun(nil)

	if msg.BxfData.AsRun == nil {
		t.Fatal("AsRun should not be nil even for empty events")
	}
	if len(msg.BxfData.AsRun.Details) != 0 {
		t.Errorf("expected 0 details, got %d", len(msg.BxfData.AsRun.Details))
	}
}

func TestExportAsRunSkipsNonItemEvents(t *testing.T) {
	baseTime := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)

	events := []playout.AsRunEvent{
		{
			Timestamp: baseTime,
			Type:      playout.AsRunItemStart,
			ChannelID: 1,
			ItemID:    "CLIP-001",
			Title:     "Opening",
		},
		{
			Timestamp: baseTime.Add(5 * time.Second),
			Type:      playout.AsRunPodOperation,
			ChannelID: 1,
			PodID:     "pod-1",
			Operation: "fire",
		},
		{
			Timestamp: baseTime.Add(10 * time.Second),
			Type:      playout.AsRunSpliceActive,
			ChannelID: 1,
			ItemID:    "splice-1",
		},
		{
			Timestamp: baseTime.Add(15 * time.Second),
			Type:      playout.AsRunManualOverride,
			ChannelID: 1,
			Operator:  "admin",
		},
		{
			Timestamp:        baseTime.Add(30 * time.Second),
			Type:             playout.AsRunItemEnd,
			ChannelID:        1,
			ItemID:           "CLIP-001",
			Title:            "Opening",
			ActualDurationMs: 30000,
		},
	}

	msg := ExportAsRun(events)

	details := msg.BxfData.AsRun.Details
	if len(details) != 1 {
		t.Fatalf("expected 1 detail (non-item events skipped), got %d", len(details))
	}
	if details[0].Content.ContentId.HouseNumber != "CLIP-001" {
		t.Errorf("HouseNumber = %q, want %q", details[0].Content.ContentId.HouseNumber, "CLIP-001")
	}
}

func TestExportAsRunStartWithoutEnd(t *testing.T) {
	baseTime := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)

	events := []playout.AsRunEvent{
		{
			Timestamp: baseTime,
			Type:      playout.AsRunItemStart,
			ChannelID: 1,
			ItemID:    "CLIP-001",
			Title:     "Orphan Start",
		},
	}

	msg := ExportAsRun(events)

	// Start without end should still produce a detail entry.
	details := msg.BxfData.AsRun.Details
	if len(details) != 1 {
		t.Fatalf("expected 1 detail for start-without-end, got %d", len(details))
	}
	if details[0].Status != "DidNotAir" {
		t.Errorf("Status = %q, want %q", details[0].Status, "DidNotAir")
	}
	if details[0].Comment == "" {
		t.Error("expected Comment to be set for DidNotAir item")
	}
	if details[0].ActualStartTime != "2026-03-25T10:00:00.000" {
		t.Errorf("ActualStartTime = %q, want %q", details[0].ActualStartTime, "2026-03-25T10:00:00.000")
	}
	// No end time or duration for orphan start.
	if details[0].ActualEndTime != "" {
		t.Errorf("ActualEndTime = %q, want empty", details[0].ActualEndTime)
	}
}

func TestExportAsRunDidNotAir(t *testing.T) {
	events := []playout.AsRunEvent{
		{Type: playout.AsRunItemStart, ItemID: "aired-1", Title: "Completed"},
		{Type: playout.AsRunItemEnd, ItemID: "aired-1", ActualDurationMs: 30000},
		{Type: playout.AsRunItemStart, ItemID: "skipped-1", Title: "Skipped"},
		// No item_end for skipped-1
	}

	msg := ExportAsRun(events)
	if len(msg.BxfData.AsRun.Details) != 2 {
		t.Fatalf("got %d details, want 2", len(msg.BxfData.AsRun.Details))
	}

	// First should be Aired.
	if msg.BxfData.AsRun.Details[0].Status != "Aired" {
		t.Errorf("detail[0] status = %q, want Aired", msg.BxfData.AsRun.Details[0].Status)
	}
	// Second should be DidNotAir.
	if msg.BxfData.AsRun.Details[1].Status != "DidNotAir" {
		t.Errorf("detail[1] status = %q, want DidNotAir", msg.BxfData.AsRun.Details[1].Status)
	}
}
