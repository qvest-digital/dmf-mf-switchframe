package bxf

import (
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/playout"
)

func TestImportPlaylist(t *testing.T) {
	msg := BxfMessage{
		BxfData: BxfData{
			Schedule: &Schedule{
				Details: []ScheduleDetail{
					{
						Content: Content{
							ContentId: ContentId{HouseNumber: "SHOW-100"},
							Name:      "Evening News",
							Duration:  "PT1800.000S",
						},
						ScheduledTime:     "2026-03-25T18:00:00.000",
						ScheduledDuration: "PT1800.000S",
						Type:              "Program",
					},
					{
						Content: Content{
							ContentId: ContentId{HouseNumber: "SPOT-050"},
							Name:      "Car Ad",
							Duration:  "PT30.500S",
						},
						ScheduledTime:     "2026-03-25T18:30:00.000",
						ScheduledDuration: "PT30.500S",
						Type:              "Commercial",
					},
				},
			},
		},
	}

	pl, err := ImportPlaylist(msg, "Test Schedule")
	if err != nil {
		t.Fatalf("ImportPlaylist error: %v", err)
	}

	if pl.Name != "Test Schedule" {
		t.Errorf("Name = %q, want %q", pl.Name, "Test Schedule")
	}

	if len(pl.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(pl.Items))
	}

	// First item.
	item0 := pl.Items[0]
	if item0.ID != "SHOW-100" {
		t.Errorf("item[0] ID = %q, want %q", item0.ID, "SHOW-100")
	}
	if item0.Title != "Evening News" {
		t.Errorf("item[0] Title = %q, want %q", item0.Title, "Evening News")
	}
	if item0.DurationMs != 1800000 {
		t.Errorf("item[0] DurationMs = %d, want %d", item0.DurationMs, 1800000)
	}
	if item0.Type != playout.ItemClip {
		t.Errorf("item[0] Type = %q, want %q", item0.Type, playout.ItemClip)
	}
	if item0.ScheduledTime == nil {
		t.Fatal("item[0] ScheduledTime is nil")
	}
	expectedTime := time.Date(2026, 3, 25, 18, 0, 0, 0, time.UTC)
	if !item0.ScheduledTime.Equal(expectedTime) {
		t.Errorf("item[0] ScheduledTime = %v, want %v", item0.ScheduledTime, expectedTime)
	}

	// Second item.
	item1 := pl.Items[1]
	if item1.ID != "SPOT-050" {
		t.Errorf("item[1] ID = %q, want %q", item1.ID, "SPOT-050")
	}
	if item1.Title != "Car Ad" {
		t.Errorf("item[1] Title = %q, want %q", item1.Title, "Car Ad")
	}
	if item1.DurationMs != 30500 {
		t.Errorf("item[1] DurationMs = %d, want %d", item1.DurationMs, 30500)
	}
}

func TestImportPlaylistNoSchedule(t *testing.T) {
	// No schedule at all.
	msg := BxfMessage{}
	_, err := ImportPlaylist(msg, "Empty")
	if err == nil {
		t.Fatal("expected error for missing schedule")
	}

	// Schedule present but empty details.
	msg2 := BxfMessage{
		BxfData: BxfData{
			Schedule: &Schedule{
				Details: []ScheduleDetail{},
			},
		},
	}
	_, err = ImportPlaylist(msg2, "Empty Details")
	if err == nil {
		t.Fatal("expected error for empty schedule details")
	}
}

func TestImportPlaylistRoundTrip(t *testing.T) {
	msg := BxfMessage{
		BxfData: BxfData{
			Schedule: &Schedule{
				Details: []ScheduleDetail{
					{
						Content: Content{
							ContentId: ContentId{HouseNumber: "RT-001"},
							Name:      "Round Trip",
							Duration:  "PT60.000S",
						},
						ScheduledTime:     "2026-03-25T12:00:00.000",
						ScheduledDuration: "PT60.000S",
						Type:              "Program",
					},
					{
						Content: Content{
							ContentId: ContentId{HouseNumber: "RT-002"},
							Name:      "Live Feed",
							Duration:  "PT120.000S",
						},
						ScheduledTime:     "2026-03-25T12:01:00.000",
						ScheduledDuration: "PT120.000S",
						Type:              "Live",
					},
				},
			},
		},
	}

	pl, err := ImportPlaylist(msg, "Round Trip Test")
	if err != nil {
		t.Fatalf("ImportPlaylist error: %v", err)
	}

	// Verify IDs are preserved.
	if pl.Items[0].ID != "RT-001" {
		t.Errorf("item[0] ID = %q, want %q", pl.Items[0].ID, "RT-001")
	}
	if pl.Items[1].ID != "RT-002" {
		t.Errorf("item[1] ID = %q, want %q", pl.Items[1].ID, "RT-002")
	}

	// Verify Live type mapping.
	if pl.Items[1].Type != playout.ItemLive {
		t.Errorf("item[1] Type = %q, want %q", pl.Items[1].Type, playout.ItemLive)
	}
}

func TestImportPlaylistTypeMapping(t *testing.T) {
	tests := []struct {
		bxfType  string
		wantType playout.ItemType
	}{
		{"Program", playout.ItemClip},
		{"program", playout.ItemClip},
		{"Show", playout.ItemClip},
		{"SHOW", playout.ItemClip},
		{"Spot", playout.ItemClip},
		{"spot", playout.ItemClip},
		{"Commercial", playout.ItemClip},
		{"commercial", playout.ItemClip},
		{"Live", playout.ItemLive},
		{"live", playout.ItemLive},
		{"LIVE", playout.ItemLive},
		{"", playout.ItemClip},
		{"Unknown", playout.ItemClip},
	}

	for _, tc := range tests {
		t.Run(tc.bxfType, func(t *testing.T) {
			msg := BxfMessage{
				BxfData: BxfData{
					Schedule: &Schedule{
						Details: []ScheduleDetail{
							{
								Content: Content{
									ContentId: ContentId{HouseNumber: "X"},
									Name:      "Test",
									Duration:  "PT10S",
								},
								Type: tc.bxfType,
							},
						},
					},
				},
			}
			pl, err := ImportPlaylist(msg, "type-test")
			if err != nil {
				t.Fatalf("ImportPlaylist error: %v", err)
			}
			if pl.Items[0].Type != tc.wantType {
				t.Errorf("bxfType %q → Type %q, want %q", tc.bxfType, pl.Items[0].Type, tc.wantType)
			}
		})
	}
}

func TestParseBxfDuration(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"PT30.000S", 30000},
		{"PT30S", 30000},
		{"PT1800.000S", 1800000},
		{"PT0.500S", 500},
		{"PT60S", 60000},
		{"PT30.500S", 30500},
		{"", 0},
		{"invalid", 0},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := parseBxfDuration(tc.input)
			if got != tc.want {
				t.Errorf("parseBxfDuration(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseBxfTime(t *testing.T) {
	// Milliseconds format.
	ts, err := parseBxfTime("2026-03-25T18:00:00.000")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	expected := time.Date(2026, 3, 25, 18, 0, 0, 0, time.UTC)
	if !ts.Equal(expected) {
		t.Errorf("got %v, want %v", ts, expected)
	}

	// No milliseconds format.
	ts2, err := parseBxfTime("2026-03-25T18:00:00")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !ts2.Equal(expected) {
		t.Errorf("got %v, want %v", ts2, expected)
	}

	// Z suffix format.
	ts3, err := parseBxfTime("2026-03-25T18:00:00Z")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !ts3.Equal(expected) {
		t.Errorf("got %v, want %v", ts3, expected)
	}

	// Empty string.
	_, err = parseBxfTime("")
	if err == nil {
		t.Error("expected error for empty string")
	}

	// Garbage.
	_, err = parseBxfTime("not-a-time")
	if err == nil {
		t.Error("expected error for invalid time")
	}
}
