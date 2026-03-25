package playout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Task 1: As-Run Event Type Tests ---

func TestAsRunEventType_Constants(t *testing.T) {
	tests := []struct {
		typ  AsRunEventType
		want string
	}{
		{AsRunItemStart, "item_start"},
		{AsRunItemEnd, "item_end"},
		{AsRunSecondaryFire, "secondary_event_fire"},
		{AsRunSpliceActive, "splice_active"},
		{AsRunPodOperation, "pod_operation"},
		{AsRunManualOverride, "manual_override"},
		{AsRunCacheEvent, "cache_event"},
		{AsRunError, "error"},
	}
	for _, tt := range tests {
		if string(tt.typ) != tt.want {
			t.Errorf("AsRunEventType = %q, want %q", tt.typ, tt.want)
		}
	}
}

func TestAsRunEvent_JSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	scheduled := now.Add(-100 * time.Millisecond)
	ev := AsRunEvent{
		Timestamp:   now,
		Type:        AsRunItemStart,
		ChannelID:   1,
		ItemID:      "item-001",
		Title:       "Opening Bumper",
		ScheduledTS: &scheduled,
		DeltaMs:     100.5,
		Source:      "clip://bumper.ts",
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got AsRunEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Type != AsRunItemStart {
		t.Errorf("Type = %q, want %q", got.Type, AsRunItemStart)
	}
	if got.ChannelID != 1 {
		t.Errorf("ChannelID = %d, want 1", got.ChannelID)
	}
	if got.ItemID != "item-001" {
		t.Errorf("ItemID = %q, want %q", got.ItemID, "item-001")
	}
	if got.Title != "Opening Bumper" {
		t.Errorf("Title = %q, want %q", got.Title, "Opening Bumper")
	}
	if got.ScheduledTS == nil {
		t.Fatal("ScheduledTS is nil")
	}
	if !got.ScheduledTS.Equal(scheduled) {
		t.Errorf("ScheduledTS = %v, want %v", got.ScheduledTS, scheduled)
	}
	if got.DeltaMs != 100.5 {
		t.Errorf("DeltaMs = %f, want 100.5", got.DeltaMs)
	}
	if got.Source != "clip://bumper.ts" {
		t.Errorf("Source = %q, want %q", got.Source, "clip://bumper.ts")
	}
}

func TestAsRunEvent_PodFields(t *testing.T) {
	ev := AsRunEvent{
		Timestamp:           time.Now().UTC(),
		Type:                AsRunPodOperation,
		ChannelID:           1,
		PodID:               "pod-001",
		PodType:             "commercial",
		Operation:           "fire",
		Operator:            "automation",
		ScheduledDurationMs: 30000,
		ActualDurationMs:    30150,
		BlackDetect: &BlackDetectInfo{
			Found:    true,
			LumaMean: 2.3,
			OffsetMs: -50.0,
		},
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got AsRunEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.PodID != "pod-001" {
		t.Errorf("PodID = %q, want %q", got.PodID, "pod-001")
	}
	if got.PodType != "commercial" {
		t.Errorf("PodType = %q, want %q", got.PodType, "commercial")
	}
	if got.Operation != "fire" {
		t.Errorf("Operation = %q, want %q", got.Operation, "fire")
	}
	if got.ScheduledDurationMs != 30000 {
		t.Errorf("ScheduledDurationMs = %d, want 30000", got.ScheduledDurationMs)
	}
	if got.ActualDurationMs != 30150 {
		t.Errorf("ActualDurationMs = %d, want 30150", got.ActualDurationMs)
	}
	if got.BlackDetect == nil {
		t.Fatal("BlackDetect is nil")
	}
	if !got.BlackDetect.Found {
		t.Error("BlackDetect.Found = false, want true")
	}
	if got.BlackDetect.LumaMean != 2.3 {
		t.Errorf("BlackDetect.LumaMean = %f, want 2.3", got.BlackDetect.LumaMean)
	}
	if got.BlackDetect.OffsetMs != -50.0 {
		t.Errorf("BlackDetect.OffsetMs = %f, want -50.0", got.BlackDetect.OffsetMs)
	}
}

func TestAsRunEvent_OmitsEmptyFields(t *testing.T) {
	ev := AsRunEvent{
		Timestamp: time.Now().UTC(),
		Type:      AsRunError,
		ChannelID: 0,
		Message:   "source timeout",
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}

	// These fields should be omitted due to omitempty.
	for _, key := range []string{
		"item_id", "title", "scheduled_ts", "source",
		"event", "pod_id", "pod_type", "operation", "operator",
		"reason", "black_detect",
	} {
		if _, ok := raw[key]; ok {
			t.Errorf("expected key %q to be omitted, but it was present", key)
		}
	}

	// These fields should be present.
	for _, key := range []string{"ts", "type", "message"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected key %q to be present, but it was missing", key)
		}
	}
}

func TestBlackDetectInfo_JSONRoundTrip(t *testing.T) {
	info := BlackDetectInfo{
		Found:    true,
		LumaMean: 1.5,
		OffsetMs: -25.0,
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got BlackDetectInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Found != true {
		t.Error("Found = false, want true")
	}
	if got.LumaMean != 1.5 {
		t.Errorf("LumaMean = %f, want 1.5", got.LumaMean)
	}
	if got.OffsetMs != -25.0 {
		t.Errorf("OffsetMs = %f, want -25.0", got.OffsetMs)
	}
}

// --- Task 2: AsRunLogger Core Tests ---

func TestAsRunLogger_NewAndClose(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	// Verify the JSONL file was created with today's date.
	expected := filepath.Join(dir, time.Now().UTC().Format("asrun-2006-01-02")+".jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("expected JSONL file %q to exist: %v", expected, err)
	}
}

func TestAsRunLogger_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir", "asrun")
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 10,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("expected directory %q to exist: %v", dir, err)
	}
}

func TestAsRunLogger_LogAndAll(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	now := time.Now().UTC()
	ev1 := AsRunEvent{
		Timestamp: now,
		Type:      AsRunItemStart,
		ChannelID: 1,
		ItemID:    "item-001",
		Title:     "Opening",
	}
	ev2 := AsRunEvent{
		Timestamp: now.Add(time.Second),
		Type:      AsRunItemEnd,
		ChannelID: 1,
		ItemID:    "item-001",
		Title:     "Opening",
	}

	logger.Log(ev1)
	logger.Log(ev2)

	events := logger.All()
	if len(events) != 2 {
		t.Fatalf("All() len = %d, want 2", len(events))
	}
	if events[0].Type != AsRunItemStart {
		t.Errorf("events[0].Type = %q, want %q", events[0].Type, AsRunItemStart)
	}
	if events[1].Type != AsRunItemEnd {
		t.Errorf("events[1].Type = %q, want %q", events[1].Type, AsRunItemEnd)
	}
}

func TestAsRunLogger_RingOverflow(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 3,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		logger.Log(AsRunEvent{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Type:      AsRunItemStart,
			ChannelID: 1,
			ItemID:    "item-" + string(rune('A'+i)),
			Title:     "Item " + string(rune('A'+i)),
		})
	}

	events := logger.All()
	if len(events) != 3 {
		t.Fatalf("All() len = %d, want 3 (ring capacity)", len(events))
	}

	// Should be the last 3 events in chronological order.
	if events[0].ItemID != "item-C" {
		t.Errorf("events[0].ItemID = %q, want %q", events[0].ItemID, "item-C")
	}
	if events[1].ItemID != "item-D" {
		t.Errorf("events[1].ItemID = %q, want %q", events[1].ItemID, "item-D")
	}
	if events[2].ItemID != "item-E" {
		t.Errorf("events[2].ItemID = %q, want %q", events[2].ItemID, "item-E")
	}
}

func TestAsRunLogger_JSONLPersistence(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	logger.Log(AsRunEvent{
		Timestamp: now,
		Type:      AsRunItemStart,
		ChannelID: 1,
		ItemID:    "item-001",
		Title:     "Test Item",
	})
	logger.Log(AsRunEvent{
		Timestamp: now.Add(time.Second),
		Type:      AsRunError,
		ChannelID: 1,
		Message:   "test error",
	})

	logger.Close()

	// Read the JSONL file and verify contents.
	filename := filepath.Join(dir, now.Format("asrun-2006-01-02")+".jsonl")
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("JSONL lines = %d, want 2", len(lines))
	}

	var ev1 AsRunEvent
	if err := json.Unmarshal([]byte(lines[0]), &ev1); err != nil {
		t.Fatalf("Unmarshal line 0: %v", err)
	}
	if ev1.Type != AsRunItemStart {
		t.Errorf("line 0 Type = %q, want %q", ev1.Type, AsRunItemStart)
	}
	if ev1.ItemID != "item-001" {
		t.Errorf("line 0 ItemID = %q, want %q", ev1.ItemID, "item-001")
	}

	var ev2 AsRunEvent
	if err := json.Unmarshal([]byte(lines[1]), &ev2); err != nil {
		t.Fatalf("Unmarshal line 1: %v", err)
	}
	if ev2.Type != AsRunError {
		t.Errorf("line 1 Type = %q, want %q", ev2.Type, AsRunError)
	}
	if ev2.Message != "test error" {
		t.Errorf("line 1 Message = %q, want %q", ev2.Message, "test error")
	}
}

func TestAsRunLogger_DefaultRingCapacity(t *testing.T) {
	dir := t.TempDir()
	// RingCapacity of 0 should use a sensible default.
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 0,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	// Log more than a small number to confirm it works.
	for i := 0; i < 100; i++ {
		logger.Log(AsRunEvent{
			Timestamp: time.Now().UTC(),
			Type:      AsRunItemStart,
			ChannelID: 1,
			ItemID:    "item",
		})
	}

	events := logger.All()
	if len(events) != 100 {
		t.Errorf("All() len = %d, want 100", len(events))
	}
}

// --- Task 3: Query Filter Tests ---

func TestAsRunQuery_FilterByType(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	base := time.Now().UTC()
	logger.Log(AsRunEvent{Timestamp: base, Type: AsRunItemStart, ChannelID: 1, ItemID: "a"})
	logger.Log(AsRunEvent{Timestamp: base.Add(time.Second), Type: AsRunError, ChannelID: 1, Message: "fail"})
	logger.Log(AsRunEvent{Timestamp: base.Add(2 * time.Second), Type: AsRunItemStart, ChannelID: 1, ItemID: "b"})
	logger.Log(AsRunEvent{Timestamp: base.Add(3 * time.Second), Type: AsRunItemEnd, ChannelID: 1, ItemID: "a"})

	events := logger.Query(AsRunQuery{Type: AsRunItemStart})
	if len(events) != 2 {
		t.Fatalf("Query(Type=item_start) len = %d, want 2", len(events))
	}
	for _, ev := range events {
		if ev.Type != AsRunItemStart {
			t.Errorf("expected Type=%q, got %q", AsRunItemStart, ev.Type)
		}
	}
}

func TestAsRunQuery_FilterByChannel(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	base := time.Now().UTC()
	logger.Log(AsRunEvent{Timestamp: base, Type: AsRunItemStart, ChannelID: 1, ItemID: "a"})
	logger.Log(AsRunEvent{Timestamp: base.Add(time.Second), Type: AsRunItemStart, ChannelID: 2, ItemID: "b"})
	logger.Log(AsRunEvent{Timestamp: base.Add(2 * time.Second), Type: AsRunItemEnd, ChannelID: 1, ItemID: "a"})
	logger.Log(AsRunEvent{Timestamp: base.Add(3 * time.Second), Type: AsRunItemEnd, ChannelID: 2, ItemID: "b"})

	ch1 := 1
	events := logger.Query(AsRunQuery{ChannelID: &ch1})
	if len(events) != 2 {
		t.Fatalf("Query(ChannelID=1) len = %d, want 2", len(events))
	}
	for _, ev := range events {
		if ev.ChannelID != 1 {
			t.Errorf("expected ChannelID=1, got %d", ev.ChannelID)
		}
	}

	ch2 := 2
	events = logger.Query(AsRunQuery{ChannelID: &ch2})
	if len(events) != 2 {
		t.Fatalf("Query(ChannelID=2) len = %d, want 2", len(events))
	}
	for _, ev := range events {
		if ev.ChannelID != 2 {
			t.Errorf("expected ChannelID=2, got %d", ev.ChannelID)
		}
	}
}

func TestAsRunQuery_FilterBySince(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	base := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	logger.Log(AsRunEvent{Timestamp: base, Type: AsRunItemStart, ChannelID: 1, ItemID: "a"})
	logger.Log(AsRunEvent{Timestamp: base.Add(time.Minute), Type: AsRunItemStart, ChannelID: 1, ItemID: "b"})
	logger.Log(AsRunEvent{Timestamp: base.Add(2 * time.Minute), Type: AsRunItemStart, ChannelID: 1, ItemID: "c"})

	since := base.Add(30 * time.Second)
	events := logger.Query(AsRunQuery{Since: &since})
	if len(events) != 2 {
		t.Fatalf("Query(Since) len = %d, want 2", len(events))
	}
	if events[0].ItemID != "b" {
		t.Errorf("events[0].ItemID = %q, want %q", events[0].ItemID, "b")
	}
	if events[1].ItemID != "c" {
		t.Errorf("events[1].ItemID = %q, want %q", events[1].ItemID, "c")
	}
}

func TestAsRunQuery_FilterByUntil(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	base := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	logger.Log(AsRunEvent{Timestamp: base, Type: AsRunItemStart, ChannelID: 1, ItemID: "a"})
	logger.Log(AsRunEvent{Timestamp: base.Add(time.Minute), Type: AsRunItemStart, ChannelID: 1, ItemID: "b"})
	logger.Log(AsRunEvent{Timestamp: base.Add(2 * time.Minute), Type: AsRunItemStart, ChannelID: 1, ItemID: "c"})

	until := base.Add(90 * time.Second)
	events := logger.Query(AsRunQuery{Until: &until})
	if len(events) != 2 {
		t.Fatalf("Query(Until) len = %d, want 2", len(events))
	}
	if events[0].ItemID != "a" {
		t.Errorf("events[0].ItemID = %q, want %q", events[0].ItemID, "a")
	}
	if events[1].ItemID != "b" {
		t.Errorf("events[1].ItemID = %q, want %q", events[1].ItemID, "b")
	}
}

func TestAsRunQuery_FilterByTimeRange(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	base := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		logger.Log(AsRunEvent{
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			Type:      AsRunItemStart,
			ChannelID: 1,
			ItemID:    string(rune('a' + i)),
		})
	}

	since := base.Add(90 * time.Second)  // after event "a" (0m) and "b" (1m)
	until := base.Add(150 * time.Second) // before event "c" at 2m (120s), only "b" at 1m passes? No: 120s >= 90s and 120s < 150s
	events := logger.Query(AsRunQuery{Since: &since, Until: &until})
	if len(events) != 1 {
		t.Fatalf("Query(Since+Until) len = %d, want 1 (only event at 2m=120s)", len(events))
	}
	if events[0].ItemID != "c" {
		t.Errorf("events[0].ItemID = %q, want %q", events[0].ItemID, "c")
	}
}

func TestAsRunQuery_Limit(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	base := time.Now().UTC()
	for i := 0; i < 10; i++ {
		logger.Log(AsRunEvent{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Type:      AsRunItemStart,
			ChannelID: 1,
			ItemID:    "item",
		})
	}

	events := logger.Query(AsRunQuery{Limit: 3})
	if len(events) != 3 {
		t.Fatalf("Query(Limit=3) len = %d, want 3", len(events))
	}
}

func TestAsRunQuery_CombinedFilters(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	base := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)

	// Mix of types, channels, and times.
	logger.Log(AsRunEvent{Timestamp: base, Type: AsRunItemStart, ChannelID: 1, ItemID: "a"})
	logger.Log(AsRunEvent{Timestamp: base.Add(time.Second), Type: AsRunError, ChannelID: 1, Message: "err1"})
	logger.Log(AsRunEvent{Timestamp: base.Add(2 * time.Second), Type: AsRunItemStart, ChannelID: 2, ItemID: "b"})
	logger.Log(AsRunEvent{Timestamp: base.Add(3 * time.Second), Type: AsRunItemStart, ChannelID: 1, ItemID: "c"})
	logger.Log(AsRunEvent{Timestamp: base.Add(4 * time.Second), Type: AsRunItemStart, ChannelID: 1, ItemID: "d"})
	logger.Log(AsRunEvent{Timestamp: base.Add(5 * time.Second), Type: AsRunItemStart, ChannelID: 2, ItemID: "e"})

	ch1 := 1
	since := base.Add(time.Second) // exclude first event
	events := logger.Query(AsRunQuery{
		Type:      AsRunItemStart,
		ChannelID: &ch1,
		Since:     &since,
		Limit:     1,
	})
	if len(events) != 1 {
		t.Fatalf("combined query len = %d, want 1", len(events))
	}
	if events[0].ItemID != "c" {
		t.Errorf("events[0].ItemID = %q, want %q (first ch1 item_start after 'since')", events[0].ItemID, "c")
	}
}

func TestAsRunQuery_Empty(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	events := logger.Query(AsRunQuery{})
	if len(events) != 0 {
		t.Errorf("Query on empty logger returned %d events, want 0", len(events))
	}

	events = logger.All()
	if len(events) != 0 {
		t.Errorf("All on empty logger returned %d events, want 0", len(events))
	}
}

func TestAsRunQuery_NoMatches(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	logger.Log(AsRunEvent{
		Timestamp: time.Now().UTC(),
		Type:      AsRunItemStart,
		ChannelID: 1,
	})

	events := logger.Query(AsRunQuery{Type: AsRunError})
	if len(events) != 0 {
		t.Errorf("Query(Type=error) len = %d, want 0", len(events))
	}
}

func TestAsRunQuery_ChronologicalOrder(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 5,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	base := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	// Fill ring to capacity then overflow.
	for i := 0; i < 8; i++ {
		logger.Log(AsRunEvent{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Type:      AsRunItemStart,
			ChannelID: 1,
			ItemID:    string(rune('a' + i)),
		})
	}

	events := logger.All()
	if len(events) != 5 {
		t.Fatalf("All() len = %d, want 5", len(events))
	}

	// Verify chronological order.
	for i := 1; i < len(events); i++ {
		if events[i].Timestamp.Before(events[i-1].Timestamp) {
			t.Errorf("events[%d].Timestamp (%v) before events[%d].Timestamp (%v)",
				i, events[i].Timestamp, i-1, events[i-1].Timestamp)
		}
	}
}

func TestAsRunQuery_LimitZeroMeansUnlimited(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{
		Dir:          dir,
		RingCapacity: 100,
	})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()

	base := time.Now().UTC()
	for i := 0; i < 10; i++ {
		logger.Log(AsRunEvent{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Type:      AsRunItemStart,
			ChannelID: 1,
		})
	}

	events := logger.Query(AsRunQuery{Limit: 0})
	if len(events) != 10 {
		t.Errorf("Query(Limit=0) len = %d, want 10 (unlimited)", len(events))
	}
}

// --- Task 4: Channel As-Run Event Emission ---

func TestChannelEmitsAsRunEvents(t *testing.T) {
	provider := fakeProvider(10)
	ch := NewChannel(1, ChannelConfig{AssetProvider: provider})

	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{Dir: dir, RingCapacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	ch.SetAsRunLog(func(ev AsRunEvent) { logger.Log(ev) })

	pl := testPlaylist(clipItem("clip-1"), clipItem("clip-2"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatal(err)
	}
	if err := ch.Start(); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	events := logger.Query(AsRunQuery{})
	starts := 0
	ends := 0
	for _, ev := range events {
		switch ev.Type {
		case AsRunItemStart:
			starts++
		case AsRunItemEnd:
			ends++
		}
	}
	if starts != 2 {
		t.Errorf("item_start events = %d, want 2", starts)
	}
	if ends != 2 {
		t.Errorf("item_end events = %d, want 2", ends)
	}
}

// --- Task 5: PodBoard As-Run Event Emission ---

func TestPodBoardEmitsAsRunEvents(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{Dir: dir, RingCapacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	pb := NewPodBoard(1)
	pb.SetAsRunLog(func(ev AsRunEvent) { logger.Log(ev) })

	pod, err := pb.CreatePod(PodConfig{
		Type:       "commercial",
		DurationMs: 100,
		AutoReturn: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pb.Arm(pod.ID); err != nil {
		t.Fatal(err)
	}
	if err := pb.Fire(pod.ID); err != nil {
		t.Fatal(err)
	}

	// Wait for auto-return to complete.
	waitFor(t, 2*time.Second, func() bool {
		p, _ := pb.GetPod(pod.ID)
		return p.State.IsTerminal()
	})

	events := logger.Query(AsRunQuery{Type: AsRunPodOperation})
	fireCount := 0
	returnCount := 0
	for _, ev := range events {
		switch ev.Operation {
		case "fire":
			fireCount++
		case "return":
			returnCount++
		}
	}
	if fireCount != 1 {
		t.Errorf("fire events = %d, want 1", fireCount)
	}
	if returnCount != 1 {
		t.Errorf("return events = %d, want 1", returnCount)
	}
}

func TestPodBoardDumpOutAsRunEvent(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{Dir: dir, RingCapacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	pb := NewPodBoard(1)
	pb.SetAsRunLog(func(ev AsRunEvent) { logger.Log(ev) })

	pod, err := pb.CreatePod(PodConfig{
		Type:         "commercial",
		DurationMs:   60000,
		AutoReturn:   true,
		AllowDumpOut: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pb.Arm(pod.ID); err != nil {
		t.Fatal(err)
	}
	if err := pb.Fire(pod.ID); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	if err := pb.DumpOut(pod.ID); err != nil {
		t.Fatal(err)
	}

	events := logger.Query(AsRunQuery{Type: AsRunPodOperation})
	dumpOutCount := 0
	for _, ev := range events {
		if ev.Operation == "dump_out" {
			dumpOutCount++
			if ev.ActualDurationMs <= 0 {
				t.Errorf("dump_out ActualDurationMs = %d, want > 0", ev.ActualDurationMs)
			}
		}
	}
	if dumpOutCount != 1 {
		t.Errorf("dump_out events = %d, want 1", dumpOutCount)
	}
}

// --- Task 6: SpliceCoordinator As-Run Emission ---

func TestSpliceCoordinatorEmitsAsRunEvent(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{Dir: dir, RingCapacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})

	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     100,
		WindowMs:      100,
		TimeoutMs:     5000,
	})
	sc.SetAsRunLog(func(ev AsRunEvent) { logger.Log(ev) })

	// currentPTS=0, preRoll=100ms=9000 ticks, window=100ms=9000 ticks.
	// targetPTS = 9000, window = [0, 18000].
	err = sc.BeginSplice("pod-test", true, true, 180000, 0)
	if err != nil {
		t.Fatal(err)
	}

	blackFrame := make([]byte, 320*240*3/2) // all zeros = black
	sc.IngestFrame(blackFrame, 320, 240, 5000)  // inside window
	sc.IngestFrame(blackFrame, 320, 240, 20000) // past window, triggers finalization

	waitFor(t, 2*time.Second, func() bool {
		return !sc.Active()
	})

	events := logger.Query(AsRunQuery{Type: AsRunSpliceActive})
	if len(events) != 1 {
		t.Fatalf("got %d splice_active events, want 1", len(events))
	}
	if events[0].PodID != "pod-test" {
		t.Errorf("pod_id = %q, want %q", events[0].PodID, "pod-test")
	}
	if events[0].BlackDetect == nil {
		t.Fatal("expected black_detect info")
	}
}

// --- Task 7: Manager AsRunLogger Wiring ---

func TestManagerPropagatesAsRunLogger(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{Dir: dir, RingCapacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	provider := fakeProvider(5)
	mgr := NewManager(ManagerConfig{AssetProvider: provider})
	mgr.SetAsRunLogger(logger)

	pl := testPlaylist(clipItem("clip-1"))
	if err := mgr.LoadPlaylist(1, pl); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(1); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 5*time.Second, func() bool {
		states := mgr.ChannelStates()
		return states[0].State == "completed"
	})

	events := logger.Query(AsRunQuery{ChannelID: intPtr(1)})
	hasStart := false
	hasEnd := false
	for _, ev := range events {
		if ev.Type == AsRunItemStart {
			hasStart = true
		}
		if ev.Type == AsRunItemEnd {
			hasEnd = true
		}
	}
	if !hasStart {
		t.Error("expected item_start event")
	}
	if !hasEnd {
		t.Error("expected item_end event")
	}
}

// intPtr is a helper to create *int for query filters.
func intPtr(i int) *int { return &i }
