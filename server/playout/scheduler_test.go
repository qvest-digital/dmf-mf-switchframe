package playout

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- helpers ---

func schedClipItem(id string, durationMs int64) PlaylistItem {
	return PlaylistItem{
		ID:         id,
		Type:       ItemClip,
		AssetURI:   "local:///clips/" + id + ".ts",
		Title:      "Clip " + id,
		DurationMs: durationMs,
	}
}

func schedLiveItem(id, source string) PlaylistItem {
	return PlaylistItem{
		ID:     id,
		Type:   ItemLive,
		Source: source,
		Title:  "Live " + id,
	}
}

func schedSlateItem(id string, durationMs int64) PlaylistItem {
	return PlaylistItem{
		ID:         id,
		Type:       ItemSlate,
		Title:      "Slate " + id,
		DurationMs: durationMs,
	}
}

func schedPlaylist(items ...PlaylistItem) *Playlist {
	return &Playlist{
		ID:    "sched-test-pl",
		Name:  "Scheduler Test Playlist",
		Items: items,
	}
}

// --- tests ---

func TestScheduler_InitialState(t *testing.T) {
	s := NewScheduler()

	if s.State() != SchedIdle {
		t.Errorf("initial state = %v, want SchedIdle", s.State())
	}
	if s.Mode() != ModeAutomated {
		t.Errorf("initial mode = %v, want ModeAutomated", s.Mode())
	}

	// Tick on an empty/stopped scheduler should return idle+completed.
	d := s.Tick(time.Now())
	if !d.Completed {
		t.Error("expected Completed=true for stopped scheduler with no playlist")
	}
	if d.Item != nil {
		t.Error("expected Item=nil for stopped scheduler")
	}
}

func TestScheduler_LoadAndStart(t *testing.T) {
	s := NewScheduler()

	pl := schedPlaylist(
		schedClipItem("a", 5000),
		schedClipItem("b", 3000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	s.Start()

	if s.State() != SchedPlaying {
		t.Errorf("state after start = %v, want SchedPlaying", s.State())
	}

	now := time.Now()
	d := s.Tick(now)
	if d.State != SchedPlaying {
		t.Errorf("Tick state = %v, want SchedPlaying", d.State)
	}
	if d.Item == nil {
		t.Fatal("expected non-nil Item")
	}
	if d.Item.ID != "a" {
		t.Errorf("current item = %q, want %q", d.Item.ID, "a")
	}
	if d.IsIdle {
		t.Error("expected IsIdle=false")
	}
	if d.Completed {
		t.Error("expected Completed=false")
	}
}

func TestScheduler_SignalItemComplete_AdvancesCursor(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedClipItem("a", 5000),
		schedClipItem("b", 3000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "a" {
		t.Fatalf("expected item a, got %q", d.Item.ID)
	}

	// Signal that clip "a" finished playing.
	s.SignalItemComplete()

	d = s.Tick(now)
	if d.Item == nil || d.Item.ID != "b" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("after SignalItemComplete, item = %q, want %q", id, "b")
	}
	if s.Cursor() != 1 {
		t.Errorf("cursor = %d, want 1", s.Cursor())
	}
}

func TestScheduler_AutoAdvance_SlateDuration(t *testing.T) {
	s := NewScheduler()

	// Slate with 100ms duration — auto-advances by elapsed time.
	pl := schedPlaylist(
		schedSlateItem("slate-1", 100),
		schedClipItem("b", 5000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	start := time.Now()
	d := s.Tick(start)
	if d.Item.ID != "slate-1" {
		t.Fatalf("expected slate-1, got %q", d.Item.ID)
	}

	// Before duration elapsed — still on slate.
	d = s.Tick(start.Add(50 * time.Millisecond))
	if d.Item.ID != "slate-1" {
		t.Errorf("at 50ms: expected slate-1, got %q", d.Item.ID)
	}

	// After duration elapsed — should advance to next item.
	d = s.Tick(start.Add(101 * time.Millisecond))
	if d.Item == nil || d.Item.ID != "b" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("at 101ms: expected item b, got %q", id)
	}
}

func TestScheduler_LiveItem_WaitsForNext(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedLiveItem("live-1", "camera-1"),
		schedClipItem("b", 5000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "live-1" {
		t.Fatalf("expected live-1, got %q", d.Item.ID)
	}

	// Even after a long time, live items don't auto-advance.
	d = s.Tick(now.Add(10 * time.Minute))
	if d.Item.ID != "live-1" {
		t.Errorf("live item should not auto-advance, got %q", d.Item.ID)
	}
}

func TestScheduler_LoopItem_DoesNotAdvance(t *testing.T) {
	s := NewScheduler()

	loopItem := schedClipItem("loop-a", 1000)
	loopItem.Loop = true

	pl := schedPlaylist(
		loopItem,
		schedClipItem("b", 5000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "loop-a" {
		t.Fatalf("expected loop-a, got %q", d.Item.ID)
	}

	// SignalItemComplete should NOT advance when Loop is true.
	s.SignalItemComplete()
	d = s.Tick(now.Add(2 * time.Second))
	if d.Item.ID != "loop-a" {
		t.Errorf("loop item should stay, got %q", d.Item.ID)
	}
	if s.Cursor() != 0 {
		t.Errorf("cursor should remain 0, got %d", s.Cursor())
	}
}

func TestScheduler_LoopItem_NextOverridesLoop(t *testing.T) {
	s := NewScheduler()

	loopItem := schedClipItem("loop-a", 1000)
	loopItem.Loop = true

	pl := schedPlaylist(
		loopItem,
		schedClipItem("b", 5000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "loop-a" {
		t.Fatalf("expected loop-a, got %q", d.Item.ID)
	}

	// Explicit Next() should override Loop and advance.
	if err := s.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	d = s.Tick(now.Add(100 * time.Millisecond))
	if d.Item == nil || d.Item.ID != "b" {
		got := "nil"
		if d.Item != nil {
			got = d.Item.ID
		}
		t.Errorf("Next() should advance past loop item, got %q", got)
	}
}

func TestScheduler_HoldItem_WaitsForNext(t *testing.T) {
	s := NewScheduler()

	holdItem := schedClipItem("hold-a", 1000)
	holdItem.Hold = true

	pl := schedPlaylist(
		holdItem,
		schedClipItem("b", 5000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "hold-a" {
		t.Fatalf("expected hold-a, got %q", d.Item.ID)
	}

	// Signal complete on a Hold item — should still stay on this item.
	s.SignalItemComplete()
	d = s.Tick(now.Add(5 * time.Second))
	if d.Item.ID != "hold-a" {
		t.Errorf("hold item should wait for Next(), got %q", d.Item.ID)
	}

	// Explicit Next() should advance past the hold.
	if err := s.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	d = s.Tick(now.Add(6 * time.Second))
	if d.Item == nil || d.Item.ID != "b" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("after Next on hold item, expected b, got %q", id)
	}
}

func TestScheduler_ScheduledTime_AutoAdvances(t *testing.T) {
	s := NewScheduler()

	now := time.Now()
	sched := now.Add(200 * time.Millisecond)

	pl := schedPlaylist(
		schedLiveItem("live-1", "camera-1"),
		schedClipItem("b", 5000),
	)
	// Set scheduled time on the second item.
	pl.Items[1].ScheduledTime = &sched

	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	d := s.Tick(now)
	if d.Item.ID != "live-1" {
		t.Fatalf("expected live-1, got %q", d.Item.ID)
	}

	// Before scheduled time — should stay on live item.
	d = s.Tick(now.Add(100 * time.Millisecond))
	if d.Item.ID != "live-1" {
		t.Errorf("at 100ms: expected live-1, got %q", d.Item.ID)
	}

	// At or past scheduled time — should advance.
	d = s.Tick(now.Add(201 * time.Millisecond))
	if d.Item == nil || d.Item.ID != "b" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("at 201ms: expected b, got %q", id)
	}
}

func TestScheduler_Transition_Progress(t *testing.T) {
	s := NewScheduler()

	itemA := schedClipItem("a", 5000)
	itemB := schedClipItem("b", 5000)
	itemB.Transition = &TransitionConfig{
		Type:       "dissolve",
		DurationMs: 1000,
	}

	pl := schedPlaylist(itemA, itemB)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "a" {
		t.Fatalf("expected a, got %q", d.Item.ID)
	}

	// Signal item a complete — should start transition to b.
	s.SignalItemComplete()

	// Tick right after signal — should be transitioning at progress ~0.
	d = s.Tick(now)
	if d.State != SchedTransitioning {
		t.Errorf("state = %v, want SchedTransitioning", d.State)
	}
	if d.Transition == nil {
		t.Fatal("expected non-nil Transition")
	}
	if d.Transition.Outgoing == nil || d.Transition.Outgoing.ID != "a" {
		t.Error("expected outgoing=a")
	}
	if d.Transition.Incoming == nil || d.Transition.Incoming.ID != "b" {
		t.Error("expected incoming=b")
	}
	if d.Transition.Progress > 0.1 {
		t.Errorf("initial progress = %f, expected near 0", d.Transition.Progress)
	}

	// At 500ms — progress should be ~0.5.
	d = s.Tick(now.Add(500 * time.Millisecond))
	if d.State != SchedTransitioning {
		t.Errorf("at 500ms: state = %v, want SchedTransitioning", d.State)
	}
	if d.Transition.Progress < 0.4 || d.Transition.Progress > 0.6 {
		t.Errorf("at 500ms: progress = %f, expected ~0.5", d.Transition.Progress)
	}

	// At 1001ms — transition should be complete, playing item b.
	d = s.Tick(now.Add(1001 * time.Millisecond))
	if d.State != SchedPlaying {
		t.Errorf("at 1001ms: state = %v, want SchedPlaying", d.State)
	}
	if d.Item == nil || d.Item.ID != "b" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("at 1001ms: item = %q, want b", id)
	}
}

func TestScheduler_Transition_DefaultCut(t *testing.T) {
	s := NewScheduler()

	// No transition config on item b — should be a hard cut.
	pl := schedPlaylist(
		schedClipItem("a", 5000),
		schedClipItem("b", 3000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "a" {
		t.Fatalf("expected a, got %q", d.Item.ID)
	}

	s.SignalItemComplete()

	d = s.Tick(now)
	// Should go straight to playing b — no transition.
	if d.State != SchedPlaying {
		t.Errorf("state = %v, want SchedPlaying", d.State)
	}
	if d.Transition != nil {
		t.Error("expected nil Transition for hard cut")
	}
	if d.Item == nil || d.Item.ID != "b" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("item = %q, want b", id)
	}
}

func TestScheduler_ManualMode_BlocksAdvance(t *testing.T) {
	s := NewScheduler()

	pl := schedPlaylist(
		schedSlateItem("slate-1", 100),
		schedClipItem("b", 5000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	// Enter manual mode.
	s.TakeManual()
	if s.Mode() != ModeManual {
		t.Fatalf("mode = %v, want ModeManual", s.Mode())
	}

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "slate-1" {
		t.Fatalf("expected slate-1, got %q", d.Item.ID)
	}

	// Even after slate duration expires, manual mode blocks advance.
	d = s.Tick(now.Add(200 * time.Millisecond))
	if d.Item.ID != "slate-1" {
		t.Errorf("manual mode should block auto-advance, got %q", d.Item.ID)
	}

	// Return to auto allows advance.
	s.ReturnToAuto()
	d = s.Tick(now.Add(201 * time.Millisecond))
	if d.Item == nil || d.Item.ID != "b" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("after ReturnToAuto: expected b, got %q", id)
	}
}

func TestScheduler_Next_AdvancesPastLiveItem(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedLiveItem("live-1", "camera-1"),
		schedClipItem("b", 5000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "live-1" {
		t.Fatalf("expected live-1, got %q", d.Item.ID)
	}

	if err := s.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}

	d = s.Tick(now)
	if d.Item == nil || d.Item.ID != "b" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("after Next: expected b, got %q", id)
	}
}

func TestScheduler_Previous_GoesBack(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedClipItem("a", 5000),
		schedClipItem("b", 5000),
		schedClipItem("c", 5000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	// Move to item b.
	s.SignalItemComplete()
	d := s.Tick(now)
	if d.Item.ID != "b" {
		t.Fatalf("expected b, got %q", d.Item.ID)
	}

	// Go back to a.
	if err := s.Previous(); err != nil {
		t.Fatalf("Previous: %v", err)
	}
	d = s.Tick(now)
	if d.Item == nil || d.Item.ID != "a" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("after Previous: expected a, got %q", id)
	}
}

func TestScheduler_JumpTo_ByItemID(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedClipItem("a", 5000),
		schedClipItem("b", 5000),
		schedClipItem("c", 5000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "a" {
		t.Fatalf("expected a, got %q", d.Item.ID)
	}

	// Jump directly to item c.
	if err := s.JumpTo("c"); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}
	d = s.Tick(now)
	if d.Item == nil || d.Item.ID != "c" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("after JumpTo: expected c, got %q", id)
	}
	if s.Cursor() != 2 {
		t.Errorf("cursor = %d, want 2", s.Cursor())
	}

	// JumpTo unknown ID should error.
	if err := s.JumpTo("nonexistent"); err != ErrItemNotFound {
		t.Errorf("JumpTo unknown: expected ErrItemNotFound, got %v", err)
	}
}

func TestScheduler_PlaylistComplete(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedClipItem("a", 1000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "a" {
		t.Fatalf("expected a, got %q", d.Item.ID)
	}

	// Signal complete on last item.
	s.SignalItemComplete()
	d = s.Tick(now)

	if !d.Completed {
		t.Error("expected Completed=true after last item")
	}
	if d.State != SchedIdle {
		t.Errorf("state = %v, want SchedIdle", d.State)
	}
}

func TestScheduler_IdleState_BetweenScheduledItems(t *testing.T) {
	s := NewScheduler()

	now := time.Now()
	// Item b is scheduled far in the future — gap should be idle.
	futureTime := now.Add(10 * time.Second)

	itemA := schedSlateItem("a", 100)
	itemB := schedClipItem("b", 5000)
	itemB.ScheduledTime = &futureTime

	pl := schedPlaylist(itemA, itemB)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	// Play through slate a.
	d := s.Tick(now)
	if d.Item.ID != "a" {
		t.Fatalf("expected a, got %q", d.Item.ID)
	}

	// Slate auto-completes after 100ms.
	d = s.Tick(now.Add(101 * time.Millisecond))
	// We should be idle — next item has a future scheduled time.
	if !d.IsIdle {
		t.Error("expected IsIdle=true between scheduled items")
	}
	if d.State != SchedIdle {
		t.Errorf("state = %v, want SchedIdle", d.State)
	}

	// When we reach the scheduled time, item b should start.
	d = s.Tick(futureTime.Add(1 * time.Millisecond))
	if d.Item == nil || d.Item.ID != "b" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("at scheduled time: expected b, got %q", id)
	}
	if d.IsIdle {
		t.Error("expected IsIdle=false once scheduled item starts")
	}
}

func TestScheduler_BreakMode_BlocksAdvance(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedSlateItem("slate-1", 100),
		schedClipItem("b", 5000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	s.TakeBreak()
	if s.Mode() != ModeBreak {
		t.Fatalf("mode = %v, want ModeBreak", s.Mode())
	}

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "slate-1" {
		t.Fatalf("expected slate-1, got %q", d.Item.ID)
	}

	// After duration, break mode blocks advance.
	d = s.Tick(now.Add(200 * time.Millisecond))
	if d.Item.ID != "slate-1" {
		t.Errorf("break mode should block advance, got %q", d.Item.ID)
	}

	s.ReturnFromBreak()
	d = s.Tick(now.Add(201 * time.Millisecond))
	if d.Item == nil || d.Item.ID != "b" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("after ReturnFromBreak: expected b, got %q", id)
	}
}

func TestScheduler_Transition_WithDefaultTransition(t *testing.T) {
	s := NewScheduler()

	pl := schedPlaylist(
		schedClipItem("a", 5000),
		schedClipItem("b", 5000),
	)
	pl.DefaultTransition = TransitionConfig{
		Type:       "dissolve",
		DurationMs: 500,
	}

	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	s.Tick(now)
	s.SignalItemComplete()

	d := s.Tick(now)
	// Default transition should kick in since item b has no per-item transition.
	if d.State != SchedTransitioning {
		t.Errorf("state = %v, want SchedTransitioning", d.State)
	}
	if d.Transition == nil {
		t.Fatal("expected non-nil Transition from default config")
	}
	if d.Transition.Config.Type != "dissolve" {
		t.Errorf("transition type = %q, want dissolve", d.Transition.Config.Type)
	}

	// Complete the transition.
	d = s.Tick(now.Add(501 * time.Millisecond))
	if d.State != SchedPlaying {
		t.Errorf("state = %v, want SchedPlaying", d.State)
	}
	if d.Item == nil || d.Item.ID != "b" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("item = %q, want b", id)
	}
}

func TestScheduler_StopResetsState(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(schedClipItem("a", 5000))
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	if d.Item.ID != "a" {
		t.Fatalf("expected a, got %q", d.Item.ID)
	}

	s.Stop()
	d = s.Tick(now)
	if !d.Completed {
		t.Error("expected Completed=true after Stop")
	}
}

func TestScheduler_Next_WhenStopped(t *testing.T) {
	s := NewScheduler()
	err := s.Next()
	if err != ErrNotOnAir {
		t.Errorf("Next when stopped: expected ErrNotOnAir, got %v", err)
	}
}

func TestScheduler_Previous_AtStart(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedClipItem("a", 5000),
		schedClipItem("b", 5000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	// Previous at cursor 0 should stay at 0.
	if err := s.Previous(); err != nil {
		t.Fatalf("Previous: %v", err)
	}

	d := s.Tick(time.Now())
	if d.Item == nil || d.Item.ID != "a" {
		var id string
		if d.Item != nil {
			id = d.Item.ID
		}
		t.Errorf("Previous at start: expected a, got %q", id)
	}
}

func TestScheduler_LoadPlaylist_RejectsEmpty(t *testing.T) {
	s := NewScheduler()
	err := s.LoadPlaylist(&Playlist{ID: "empty", Name: "Empty"})
	if err == nil {
		t.Fatal("expected error for empty playlist")
	}
}

func TestScheduler_CurrentItem(t *testing.T) {
	s := NewScheduler()

	// Before loading, CurrentItem should be nil.
	if s.CurrentItem() != nil {
		t.Error("expected nil CurrentItem before load")
	}

	pl := schedPlaylist(schedClipItem("a", 5000))
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	item := s.CurrentItem()
	if item == nil || item.ID != "a" {
		t.Errorf("CurrentItem = %v, want item a", item)
	}
}

// ---------------------------------------------------------------------------
// SetCursor tests
// ---------------------------------------------------------------------------

func TestScheduler_SetCursor_MovesCursorAndState(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedSlateItem("s0", 5000),
		schedSlateItem("s1", 3000),
		schedClipItem("c2", 10000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	// Move to item 2 with 500ms elapsed.
	s.SetCursor(2, ModeManual, 500*time.Millisecond)

	snap := s.StatusSnapshot()
	if snap.Cursor != 2 {
		t.Errorf("Cursor = %d, want 2", snap.Cursor)
	}
	if snap.Mode != ModeManual {
		t.Errorf("Mode = %v, want ModeManual", snap.Mode)
	}
	if snap.State != SchedPlaying {
		t.Errorf("State = %v, want SchedPlaying", snap.State)
	}
	// ItemStartTime should be ~500ms in the past.
	elapsed := time.Since(snap.ItemStartTime)
	if elapsed < 400*time.Millisecond || elapsed > 1*time.Second {
		t.Errorf("elapsed since ItemStartTime = %v, want ~500ms", elapsed)
	}

	// Tick should return the correct item.
	decision := s.Tick(time.Now())
	if decision.Item == nil || decision.Item.ID != "c2" {
		t.Errorf("Tick item = %v, want c2", decision.Item)
	}
}

func TestScheduler_SetCursor_NoPlaylist(t *testing.T) {
	s := NewScheduler()
	// Should be a no-op, not panic.
	s.SetCursor(0, ModeAutomated, 0)
	if s.Cursor() != 0 {
		t.Errorf("Cursor = %d after SetCursor with no playlist", s.Cursor())
	}
}

func TestScheduler_SetCursor_OutOfRange(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(schedSlateItem("s0", 5000))
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	// Index too high — should be a no-op.
	s.SetCursor(5, ModeAutomated, 0)
	if s.Cursor() != 0 {
		t.Errorf("Cursor = %d, want 0 (unchanged) after out-of-range SetCursor", s.Cursor())
	}

	// Negative index — should be a no-op.
	s.SetCursor(-1, ModeAutomated, 0)
	if s.Cursor() != 0 {
		t.Errorf("Cursor = %d, want 0 (unchanged) after negative SetCursor", s.Cursor())
	}
}

func TestScheduler_SetCursor_SetsItemDuration(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedSlateItem("s0", 5000),
		schedSlateItem("s1", 3000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	// Move to item 1 (3000ms duration slate).
	s.SetCursor(1, ModeAutomated, 0)

	// Verify itemDurationMs was set correctly.
	s.mu.Lock()
	dur := s.itemDurationMs
	s.mu.Unlock()
	if dur != 3000 {
		t.Errorf("itemDurationMs = %d, want 3000", dur)
	}
}

func TestScheduler_SetCursor_ClearsFlags(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedSlateItem("s0", 5000),
		schedSlateItem("s1", 3000),
	)
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	// Set some flags that should get cleared.
	s.mu.Lock()
	s.itemComplete = true
	s.advancePend = 1
	s.holdComplete = true
	s.idleWaiting = true
	s.mu.Unlock()

	s.SetCursor(1, ModeAutomated, 0)

	s.mu.Lock()
	ic := s.itemComplete
	ap := s.advancePend
	hc := s.holdComplete
	iw := s.idleWaiting
	s.mu.Unlock()

	if ic {
		t.Error("itemComplete should be false after SetCursor")
	}
	if ap != 0 {
		t.Error("advancePend should be 0 after SetCursor")
	}
	if hc {
		t.Error("holdComplete should be false after SetCursor")
	}
	if iw {
		t.Error("idleWaiting should be false after SetCursor")
	}
}

// TestScheduler_NextDuringIdleWait_StartsScheduledItem is a regression test:
// Tick routed to tickIdleWaiting before any advancePend handling, so a Next()
// pressed during an idle wait (a) had no immediate effect and (b) stayed
// latched — when the scheduled time finally arrived the item played for one
// tick and was then skipped by the stale advancePend.
func TestScheduler_NextDuringIdleWait_StartsScheduledItem(t *testing.T) {
	s := NewScheduler()

	now := time.Now()
	futureTime := now.Add(10 * time.Second)

	itemA := schedSlateItem("a", 100)
	itemB := schedClipItem("b", 5000)
	itemB.ScheduledTime = &futureTime

	if err := s.LoadPlaylist(schedPlaylist(itemA, itemB)); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	// Play through slate a into the idle wait before scheduled item b.
	d := s.Tick(now)
	if d.Item == nil || d.Item.ID != "a" {
		t.Fatalf("expected a, got %+v", d.Item)
	}
	d = s.Tick(now.Add(101 * time.Millisecond))
	if !d.IsIdle {
		t.Fatal("expected idle wait before scheduled item b")
	}

	// Operator presses Next during the wait: item b must start NOW.
	if err := s.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	d = s.Tick(now.Add(200 * time.Millisecond))
	if d.IsIdle {
		t.Fatal("Next during idle wait was ignored — still idle")
	}
	if d.Item == nil || d.Item.ID != "b" {
		t.Fatalf("after Next during idle wait: expected item b, got %+v", d.Item)
	}

	// And the very next tick must NOT skip item b via a stale advancePend.
	d = s.Tick(now.Add(250 * time.Millisecond))
	if d.Item == nil || d.Item.ID != "b" {
		t.Fatalf("tick after scheduled item started: expected item b still playing, got %+v (stale advancePend skipped it)", d.Item)
	}
	if got := s.Cursor(); got != 1 {
		t.Fatalf("cursor = %d, want 1", got)
	}
}

// TestScheduler_JumpToDuringIdleWait_OverridesSchedule is a regression test:
// JumpTo set state=SchedPlaying but never cleared idleWaiting, so the next
// Tick still routed through tickIdleWaiting and re-entered the wait when the
// jumped-to item had a future ScheduledTime — silently ignoring the explicit
// operator action.
func TestScheduler_JumpToDuringIdleWait_OverridesSchedule(t *testing.T) {
	s := NewScheduler()

	now := time.Now()
	futureTime := now.Add(10 * time.Second)

	itemA := schedSlateItem("a", 100)
	itemB := schedClipItem("b", 5000)
	itemB.ScheduledTime = &futureTime

	if err := s.LoadPlaylist(schedPlaylist(itemA, itemB)); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	_ = s.Tick(now)
	d := s.Tick(now.Add(101 * time.Millisecond))
	if !d.IsIdle {
		t.Fatal("expected idle wait before scheduled item b")
	}

	// Operator explicitly jumps to b: it must play now, not re-enter the wait.
	if err := s.JumpTo("b"); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}
	d = s.Tick(now.Add(200 * time.Millisecond))
	if d.IsIdle {
		t.Fatal("JumpTo during idle wait was ignored — still idle")
	}
	if d.Item == nil || d.Item.ID != "b" {
		t.Fatalf("after JumpTo during idle wait: expected item b, got %+v", d.Item)
	}
}

// TestScheduler_PreviousDuringIdleWait_ClearsIdleWait verifies Previous()
// during an idle wait resumes playback of the previous item immediately.
func TestScheduler_PreviousDuringIdleWait_ClearsIdleWait(t *testing.T) {
	s := NewScheduler()

	now := time.Now()
	futureTime := now.Add(10 * time.Second)

	itemA := schedClipItem("a", 5000)
	itemB := schedClipItem("b", 5000)
	itemB.ScheduledTime = &futureTime

	if err := s.LoadPlaylist(schedPlaylist(itemA, itemB)); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	s.Start()

	_ = s.Tick(now)
	s.SignalItemComplete()
	d := s.Tick(now.Add(50 * time.Millisecond))
	if !d.IsIdle {
		t.Fatal("expected idle wait before scheduled item b")
	}

	if err := s.Previous(); err != nil {
		t.Fatalf("Previous: %v", err)
	}
	d = s.Tick(now.Add(100 * time.Millisecond))
	if d.IsIdle {
		t.Fatal("Previous during idle wait was ignored — still idle")
	}
	if d.Item == nil || d.Item.ID != "a" {
		t.Fatalf("after Previous during idle wait: expected item a, got %+v", d.Item)
	}

	s.mu.Lock()
	iw := s.idleWaiting
	s.mu.Unlock()
	if iw {
		t.Error("idleWaiting should be false after Previous")
	}
}

func TestScheduler_EndLoop_EndsAfterCurrentPass(t *testing.T) {
	s := NewScheduler()
	loop := schedClipItem("a", 1000)
	loop.Loop = true
	pl := schedPlaylist(loop, schedClipItem("b", 1000))
	if err := s.LoadPlaylist(pl); err != nil {
		t.Fatal(err)
	}
	s.Start()
	now := time.Now()
	s.Tick(now)

	// A finished pass of a looping clip does not advance on its own.
	s.SignalItemComplete()
	if d := s.Tick(now); d.Item == nil || d.Item.ID != "a" {
		t.Fatalf("loop advanced without EndLoop: %+v", d)
	}

	if err := s.EndLoop(); err != nil {
		t.Fatalf("EndLoop: %v", err)
	}
	if !s.LoopExitPending() {
		t.Fatal("LoopExitPending = false after EndLoop")
	}
	// EndLoop alone does not cut: the pass plays out first.
	if d := s.Tick(now); d.Item == nil || d.Item.ID != "a" {
		t.Fatalf("EndLoop advanced before the pass ended: %+v", d)
	}

	s.SignalItemComplete()
	if d := s.Tick(now); d.Item == nil || d.Item.ID != "b" {
		t.Fatalf("after the pass ended: want b, got %+v", d)
	}
	if s.LoopExitPending() {
		t.Fatal("LoopExitPending survived the advance")
	}
}

func TestScheduler_EndLoop_LastItemCompletesPlaylist(t *testing.T) {
	s := NewScheduler()
	loop := schedClipItem("a", 1000)
	loop.Loop = true
	if err := s.LoadPlaylist(schedPlaylist(loop)); err != nil {
		t.Fatal(err)
	}
	s.Start()
	now := time.Now()
	s.Tick(now)
	if err := s.EndLoop(); err != nil {
		t.Fatal(err)
	}
	s.SignalItemComplete()
	if d := s.Tick(now); !d.Completed {
		t.Fatalf("want the playlist completed, got %+v", d)
	}
}

func TestScheduler_EndLoop_Refused(t *testing.T) {
	s := NewScheduler()
	if err := s.EndLoop(); err != ErrNotOnAir {
		t.Fatalf("off air: want ErrNotOnAir, got %v", err)
	}
	if err := s.LoadPlaylist(schedPlaylist(schedClipItem("a", 1000))); err != nil {
		t.Fatal(err)
	}
	s.Start()
	s.Tick(time.Now())
	if err := s.EndLoop(); err != ErrNotLooping {
		t.Fatalf("clip without loop: want ErrNotLooping, got %v", err)
	}
}

func TestScheduler_EndLoop_ClearedByNextAndStart(t *testing.T) {
	s := NewScheduler()
	loop := schedClipItem("a", 1000)
	loop.Loop = true
	loop2 := schedClipItem("b", 1000)
	loop2.Loop = true
	if err := s.LoadPlaylist(schedPlaylist(loop, loop2)); err != nil {
		t.Fatal(err)
	}
	s.Start()
	now := time.Now()
	s.Tick(now)
	if err := s.EndLoop(); err != nil {
		t.Fatal(err)
	}
	if err := s.Next(); err != nil {
		t.Fatal(err)
	}
	if d := s.Tick(now); d.Item == nil || d.Item.ID != "b" {
		t.Fatalf("want b, got %+v", d)
	}
	// The release belonged to "a": "b" keeps looping.
	s.SignalItemComplete()
	if d := s.Tick(now); d.Item == nil || d.Item.ID != "b" {
		t.Fatalf("b stopped looping: %+v", d)
	}

	if err := s.EndLoop(); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	s.Start()
	if s.LoopExitPending() {
		t.Fatal("LoopExitPending survived a restart")
	}
}

// Snapshot sync moves the cursor with SetCursor. A release given for the
// item the engine was on must not make the item it is synced to end after
// one pass, or the two engines diverge.
func TestScheduler_SetCursorClearsEndLoop(t *testing.T) {
	s := NewScheduler()
	a := schedClipItem("a", 1000)
	a.Loop = true
	b := schedClipItem("b", 1000)
	b.Loop = true
	if err := s.LoadPlaylist(schedPlaylist(a, b, schedClipItem("c", 1000))); err != nil {
		t.Fatal(err)
	}
	s.Start()
	now := time.Now()
	s.Tick(now)
	if err := s.EndLoop(); err != nil {
		t.Fatal(err)
	}

	s.SetCursor(1, ModeAutomated, 200*time.Millisecond)
	if s.LoopExitPending() {
		t.Fatal("LoopExitPending survived SetCursor")
	}
	s.Tick(now)
	s.SignalItemComplete()
	if d := s.Tick(now); d.Item == nil || d.Item.ID != "b" {
		t.Fatalf("synced looping item ended after one pass: %+v", d)
	}
}

func TestScheduler_CueOffAir_StartsAtCuedItem(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedClipItem("a", 5000),
		schedClipItem("b", 5000),
		schedClipItem("c", 5000),
	)
	require.NoError(t, s.LoadPlaylist(pl))

	require.NoError(t, s.Cue("b"))
	require.Equal(t, 1, s.Cursor(), "a cue moves the off-air cursor")

	s.Start()
	d := s.Tick(time.Now())
	require.NotNil(t, d.Item)
	require.Equal(t, "b", d.Item.ID, "Start puts the cued item on air")

	// The cue is consumed: the next run starts from the top again.
	s.Stop()
	s.Start()
	d = s.Tick(time.Now())
	require.NotNil(t, d.Item)
	require.Equal(t, "a", d.Item.ID)
}

func TestScheduler_CueNeedsPlaylistAndKnownItem(t *testing.T) {
	s := NewScheduler()
	require.ErrorIs(t, s.Cue("a"), ErrNoPlaylist)

	require.NoError(t, s.LoadPlaylist(schedPlaylist(schedClipItem("a", 5000))))
	require.ErrorIs(t, s.Cue("nope"), ErrItemNotFound)
}

func TestScheduler_LoadPlaylistDropsCue(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(schedClipItem("a", 5000), schedClipItem("b", 5000))
	require.NoError(t, s.LoadPlaylist(pl))
	require.NoError(t, s.Cue("b"))
	require.NoError(t, s.LoadPlaylist(pl))

	s.Start()
	d := s.Tick(time.Now())
	require.NotNil(t, d.Item)
	require.Equal(t, "a", d.Item.ID)
}

// TestScheduler_NextPressesAddUp: every Next pressed before a tick moves one
// item further, and they land as one move on that tick, not one item per
// tick, so no item between them goes to air.
func TestScheduler_NextPressesAddUp(t *testing.T) {
	s := NewScheduler()
	require.NoError(t, s.LoadPlaylist(schedPlaylist(
		schedLiveItem("a", "cam-1"), schedLiveItem("b", "cam-2"),
		schedLiveItem("c", "cam-3"), schedLiveItem("d", "cam-4"),
	)))
	s.Start()
	now := time.Now()
	s.Tick(now)

	require.NoError(t, s.Next())
	require.NoError(t, s.Next())
	d := s.Tick(now.Add(time.Millisecond))
	require.NotNil(t, d.Item)
	require.Equal(t, "c", d.Item.ID)
	d = s.Tick(now.Add(2 * time.Millisecond))
	require.Equal(t, "c", d.Item.ID, "the presses must be used up by the move")
}

// TestScheduler_NextPressesThroughTransition: a multi-item move with a
// transition brings in the item the presses land on and commits to it, and
// a Next pressed during the transition counts from that item.
func TestScheduler_NextPressesThroughTransition(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(
		schedLiveItem("a", "cam-1"), schedLiveItem("b", "cam-2"),
		schedLiveItem("c", "cam-3"), schedLiveItem("d", "cam-4"),
	)
	pl.DefaultTransition = TransitionConfig{Type: "dissolve", DurationMs: 500}
	require.NoError(t, s.LoadPlaylist(pl))
	s.Start()
	now := time.Now()
	s.Tick(now)

	require.NoError(t, s.Next())
	require.NoError(t, s.Next())
	d := s.Tick(now)
	require.NotNil(t, d.Transition)
	require.Equal(t, "a", d.Transition.Outgoing.ID)
	require.Equal(t, "c", d.Transition.Incoming.ID)
	_, next := s.LiveSourcesAhead()
	require.Equal(t, "cam-3", next, "the incoming item's source must be armed")

	require.NoError(t, s.Next())
	d = s.Tick(now.Add(501 * time.Millisecond))
	require.Equal(t, "c", d.Item.ID)
	require.Equal(t, 2, s.Cursor())

	d = s.Tick(now.Add(502 * time.Millisecond))
	require.NotNil(t, d.Transition, "the Next pressed during the transition was lost")
	require.Equal(t, "d", d.Transition.Incoming.ID)
}

// TestScheduler_NextPressesDuringIdleWait: in a wait for a scheduled item,
// the first press starts it and each further one an item after it.
func TestScheduler_NextPressesDuringIdleWait(t *testing.T) {
	s := NewScheduler()
	now := time.Now()
	future := now.Add(10 * time.Second)
	b := schedSlateItem("b", 5000)
	b.ScheduledTime = &future
	require.NoError(t, s.LoadPlaylist(schedPlaylist(
		schedSlateItem("a", 100), b, schedSlateItem("c", 5000),
	)))
	s.Start()
	s.Tick(now)
	require.True(t, s.Tick(now.Add(101*time.Millisecond)).IsIdle)

	require.NoError(t, s.Next())
	require.NoError(t, s.Next())
	require.NoError(t, s.Next(), "a press past the end is ignored, not refused")
	require.NoError(t, s.Previous())
	d := s.Tick(now.Add(200 * time.Millisecond))
	require.NotNil(t, d.Item)
	require.Equal(t, "c", d.Item.ID)
	require.False(t, d.Completed)
}

// TestScheduler_NextPressesStopAtPlaylistEnd: presses past the end of the
// playlist are dropped, so a Previous after them takes back one that still
// counts rather than one past the end.
func TestScheduler_NextPressesStopAtPlaylistEnd(t *testing.T) {
	s := NewScheduler()
	require.NoError(t, s.LoadPlaylist(schedPlaylist(
		schedLiveItem("a", "cam-1"), schedLiveItem("b", "cam-2"),
	)))
	s.Start()
	now := time.Now()
	s.Tick(now)

	for range 5 {
		require.NoError(t, s.Next())
	}
	require.NoError(t, s.Previous())
	d := s.Tick(now.Add(time.Millisecond))
	require.False(t, d.Completed)
	require.Equal(t, "b", d.Item.ID)

	require.NoError(t, s.Next())
	require.NoError(t, s.Next())
	require.True(t, s.Tick(now.Add(2*time.Millisecond)).Completed)
}

// TestScheduler_NextPressesBoundedFromTheIncomingItem: during a transition
// into the last item, presses count from that item, so the playlist end
// bounds them there: two presses keep one, which a Previous takes back,
// and the last item stays on air.
func TestScheduler_NextPressesBoundedFromTheIncomingItem(t *testing.T) {
	s := NewScheduler()
	pl := schedPlaylist(schedLiveItem("a", "cam-1"), schedLiveItem("b", "cam-2"), schedLiveItem("c", "cam-3"))
	pl.DefaultTransition = TransitionConfig{Type: "dissolve", DurationMs: 500}
	require.NoError(t, s.LoadPlaylist(pl))
	s.Start()
	now := time.Now()
	s.Tick(now)
	require.NoError(t, s.Next())
	require.NoError(t, s.Next())
	d := s.Tick(now)
	require.NotNil(t, d.Transition)
	require.Equal(t, "c", d.Transition.Incoming.ID)

	require.NoError(t, s.Next())
	require.NoError(t, s.Next())
	require.NoError(t, s.Previous())
	d = s.Tick(now.Add(501 * time.Millisecond))
	require.Equal(t, "c", d.Item.ID)
	d = s.Tick(now.Add(502 * time.Millisecond))
	require.False(t, d.Completed, "a press past the end survived the bound")
	require.Equal(t, "c", d.Item.ID)
}

// TestScheduler_NextPressesCompleteFromIdleWait: in a wait for the last
// item, a press past it completes the playlist.
func TestScheduler_NextPressesCompleteFromIdleWait(t *testing.T) {
	s := NewScheduler()
	now := time.Now()
	future := now.Add(10 * time.Second)
	b := schedSlateItem("b", 5000)
	b.ScheduledTime = &future
	require.NoError(t, s.LoadPlaylist(schedPlaylist(schedSlateItem("a", 100), b)))
	s.Start()
	s.Tick(now)
	require.True(t, s.Tick(now.Add(101*time.Millisecond)).IsIdle)

	require.NoError(t, s.Next())
	require.NoError(t, s.Next())
	require.True(t, s.Tick(now.Add(200*time.Millisecond)).Completed)
}

func nextNowScheduler(t *testing.T, tc *TransitionConfig) *Scheduler {
	t.Helper()
	s := NewScheduler()
	pl := schedPlaylist(
		schedLiveItem("a", "cam-1"), schedLiveItem("b", "cam-2"),
		schedLiveItem("c", "cam-3"), schedLiveItem("d", "cam-4"),
	)
	if tc != nil {
		pl.DefaultTransition = *tc
	}
	require.NoError(t, s.LoadPlaylist(pl))
	s.Start()
	return s
}

// TestScheduler_NextNowDropsQueuedNexts: NextNow moves the cursor before
// the tick and takes the item after the one on air, whatever Next presses
// were queued.
func TestScheduler_NextNowDropsQueuedNexts(t *testing.T) {
	s := nextNowScheduler(t, nil)
	now := time.Now()
	s.Tick(now)

	require.NoError(t, s.Next())
	require.NoError(t, s.Next())
	require.NoError(t, s.NextNow())
	require.Equal(t, 1, s.Cursor(), "the cursor moves before the tick")
	d := s.Tick(now.Add(time.Millisecond))
	require.Equal(t, "b", d.Item.ID)
	d = s.Tick(now.Add(2 * time.Millisecond))
	require.Equal(t, "b", d.Item.ID, "a queued Next survived NextNow")

	// A Next after it counts from the item it took.
	require.NoError(t, s.Next())
	require.Equal(t, "c", s.Tick(now.Add(3*time.Millisecond)).Item.ID)
}

// TestScheduler_NextNowTransitionsFromTheItemOnAir: the item NextNow takes
// comes in through the playlist's transition, from the item on air.
func TestScheduler_NextNowTransitionsFromTheItemOnAir(t *testing.T) {
	s := nextNowScheduler(t, &TransitionConfig{Type: "dissolve", DurationMs: 500})
	now := time.Now()
	s.Tick(now)

	require.NoError(t, s.NextNow())
	d := s.Tick(now)
	require.NotNil(t, d.Transition)
	require.Equal(t, "a", d.Transition.Outgoing.ID)
	require.Equal(t, "b", d.Transition.Incoming.ID)
	// As in a transition Next starts, the cursor stays on the item going
	// out until the transition commits, and the item coming in airs next.
	snap := s.StatusSnapshot()
	require.Equal(t, 0, snap.Cursor)
	require.Equal(t, 1, snap.Next)
	d = s.Tick(now.Add(501 * time.Millisecond))
	require.Nil(t, d.Transition)
	require.Equal(t, "b", d.Item.ID)
	require.Equal(t, 1, s.Cursor())
}

// TestScheduler_LoadPlaylistForgetsTheItemOnAir: a playlist loaded on air
// starts from its first item, and a Next before the first tick goes out
// from that item, not from the index the old playlist had on air.
func TestScheduler_LoadPlaylistForgetsTheItemOnAir(t *testing.T) {
	s := nextNowScheduler(t, &TransitionConfig{Type: "dissolve", DurationMs: 500})
	now := time.Now()
	require.NoError(t, s.JumpTo("c"))
	require.Equal(t, "c", s.Tick(now).Item.ID)

	pl := schedPlaylist(schedLiveItem("w", "cam-1"), schedLiveItem("x", "cam-2"),
		schedLiveItem("y", "cam-3"), schedLiveItem("z", "cam-4"))
	pl.DefaultTransition = TransitionConfig{Type: "dissolve", DurationMs: 500}
	require.NoError(t, s.LoadPlaylist(pl))
	require.NoError(t, s.Next())
	d := s.Tick(now.Add(time.Millisecond))
	require.NotNil(t, d.Transition)
	require.Equal(t, "w", d.Transition.Outgoing.ID)
	require.Equal(t, "x", d.Transition.Incoming.ID)
}

// TestScheduler_NextNowDuringTransitionCuts: during a transition NextNow
// takes the item after the one coming in, at once and as a cut.
func TestScheduler_NextNowDuringTransitionCuts(t *testing.T) {
	s := nextNowScheduler(t, &TransitionConfig{Type: "dissolve", DurationMs: 500})
	now := time.Now()
	s.Tick(now)
	require.NoError(t, s.Next())
	require.NotNil(t, s.Tick(now).Transition)

	require.NoError(t, s.NextNow())
	d := s.Tick(now.Add(100 * time.Millisecond))
	require.Nil(t, d.Transition)
	require.Equal(t, "c", d.Item.ID)
	require.Equal(t, SchedPlaying, s.State())
}

// TestScheduler_NextNowAtLastItemCompletes: past the last item there is
// nothing to take, so the playlist completes, as Next would.
func TestScheduler_NextNowAtLastItemCompletes(t *testing.T) {
	s := nextNowScheduler(t, nil)
	now := time.Now()
	s.Tick(now)
	require.NoError(t, s.JumpTo("d"))
	s.Tick(now)

	require.NoError(t, s.NextNow())
	require.True(t, s.Tick(now.Add(time.Millisecond)).Completed)
}

// TestScheduler_NextNowDuringIdleWait starts the item waiting for its
// scheduled time.
func TestScheduler_NextNowDuringIdleWait(t *testing.T) {
	s := NewScheduler()
	now := time.Now()
	future := now.Add(10 * time.Second)
	b := schedSlateItem("b", 5000)
	b.ScheduledTime = &future
	require.NoError(t, s.LoadPlaylist(schedPlaylist(schedSlateItem("a", 100), b)))
	s.Start()
	s.Tick(now)
	require.True(t, s.Tick(now.Add(101*time.Millisecond)).IsIdle)

	require.NoError(t, s.NextNow())
	d := s.Tick(now.Add(200 * time.Millisecond))
	require.False(t, d.IsIdle)
	require.Equal(t, "b", d.Item.ID)
}

func TestScheduler_NextNowWhenStopped(t *testing.T) {
	s := NewScheduler()
	require.ErrorIs(t, s.NextNow(), ErrNotOnAir)
}

// TestScheduler_MovesBeforeATickGoOutFromTheItemOnAir: when the cursor
// moved off the item on air before a tick, the item at the cursor has not
// aired, so the transition to where the presses land runs from the item on
// air, and presses that come back to it leave it playing.
func TestScheduler_MovesBeforeATickGoOutFromTheItemOnAir(t *testing.T) {
	cases := []struct {
		name     string
		from     string
		presses  string // N = NextNow, n = Next, p = Previous, j = JumpTo b
		outgoing string // "" = no transition
		want     string // incoming item, or the item playing
	}{
		{"NextNow twice", "a", "NN", "a", "c"},
		{"NextNow then Next", "a", "Nn", "a", "c"},
		{"Previous then Next", "b", "pn", "", "b"},
		{"Previous then Next twice", "b", "pnn", "b", "c"},
		{"JumpTo then Next", "d", "jn", "d", "c"},
		{"NextNow then JumpTo cuts", "a", "Nj", "", "b"},
		{"NextNow twice then Previous cuts", "b", "NNp", "", "c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := nextNowScheduler(t, &TransitionConfig{Type: "dissolve", DurationMs: 500})
			now := time.Now()
			require.NoError(t, s.JumpTo(tc.from))
			require.Equal(t, tc.from, s.Tick(now).Item.ID)
			for _, p := range tc.presses {
				switch p {
				case 'N':
					require.NoError(t, s.NextNow())
				case 'n':
					require.NoError(t, s.Next())
				case 'p':
					require.NoError(t, s.Previous())
				case 'j':
					require.NoError(t, s.JumpTo("b"))
				}
			}
			d := s.Tick(now.Add(time.Millisecond))
			if tc.outgoing == "" {
				require.Nil(t, d.Transition)
				require.Equal(t, tc.want, d.Item.ID)
				return
			}
			require.NotNil(t, d.Transition)
			require.Equal(t, tc.outgoing, d.Transition.Outgoing.ID)
			require.Equal(t, tc.want, d.Transition.Incoming.ID)
			d = s.Tick(now.Add(502 * time.Millisecond))
			require.Equal(t, tc.want, d.Item.ID)
		})
	}
}

func reviewDecision(d SchedulerDecision) string {
	switch {
	case d.Completed:
		return "completed"
	case d.Transition != nil:
		return d.Transition.Outgoing.ID + "->" + d.Transition.Incoming.ID
	case d.IsIdle:
		return "idle"
	case d.Item != nil:
		return d.Item.ID
	}
	return ""
}

// TestScheduler_ComingBackKeepsTheItemOnAirAsItWas: moves that come back
// to the item on air before a tick leave it playing as it was. A slate
// keeps its timer, so it ends when it would have, and a looping clip keeps
// its loop release.
func TestScheduler_ComingBackKeepsTheItemOnAirAsItWas(t *testing.T) {
	for _, presses := range []string{"pn", "Np", "NNpp"} {
		t.Run(presses, func(t *testing.T) {
			press := func(s *Scheduler) {
				for _, p := range presses {
					switch p {
					case 'N':
						require.NoError(t, s.NextNow())
					case 'n':
						require.NoError(t, s.Next())
					case 'p':
						require.NoError(t, s.Previous())
					}
				}
			}
			t0 := time.Unix(0, 0)
			s := NewScheduler()
			require.NoError(t, s.LoadPlaylist(schedPlaylist(schedSlateItem("x", 100000),
				schedSlateItem("a", 1000), schedSlateItem("b", 1000), schedSlateItem("c", 1000))))
			s.Start()
			s.Tick(t0)
			require.NoError(t, s.JumpTo("a"))
			require.Equal(t, "a", s.Tick(t0).Item.ID)
			press(s)
			require.Equal(t, "a", reviewDecision(s.Tick(t0.Add(600*time.Millisecond))))
			require.Equal(t, "b", reviewDecision(s.Tick(t0.Add(1001*time.Millisecond))), "the slate's timer restarted")

			a := schedClipItem("a", 0)
			a.Loop = true
			s = NewScheduler()
			require.NoError(t, s.LoadPlaylist(schedPlaylist(schedClipItem("x", 0), a,
				schedClipItem("b", 0), schedClipItem("c", 0))))
			s.Start()
			s.Tick(t0)
			require.NoError(t, s.JumpTo("a"))
			s.Tick(t0)
			require.NoError(t, s.EndLoop())
			press(s)
			require.Equal(t, "a", reviewDecision(s.Tick(t0.Add(time.Millisecond))))
			require.True(t, s.LoopExitPending(), "the loop release was dropped")
			s.SignalItemComplete()
			require.Equal(t, "b", reviewDecision(s.Tick(t0.Add(2*time.Millisecond))))
		})
	}
}

// TestScheduler_RestartingForgetsTheItemOnAir: after Stop and Start, or a
// cursor set by peer sync or recovery, the item the last tick played is no
// longer on air, so a Next or NextNow before the next tick goes out from
// the cursor, as on a fresh scheduler.
func TestScheduler_RestartingForgetsTheItemOnAir(t *testing.T) {
	for _, tc := range []struct {
		name, op, want string
	}{
		{"restart then Next", "next", "a->b"},
		// From the first item NextNow takes b; going out from c it would
		// transition c->b.
		{"restart then NextNow", "nextnow", "b"},
		{"SetCursor then Next", "setcursor", "c->d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t0 := time.Unix(0, 0)
			s := nextNowScheduler(t, &TransitionConfig{Type: "dissolve", DurationMs: 500})
			s.Tick(t0)
			require.NoError(t, s.JumpTo("c"))
			s.Tick(t0)
			switch tc.op {
			case "next":
				s.Stop()
				s.Start()
				require.NoError(t, s.Next())
			case "nextnow":
				s.Stop()
				s.Start()
				require.NoError(t, s.NextNow())
			case "setcursor":
				require.NoError(t, s.JumpTo("a"))
				s.Tick(t0)
				s.SetCursor(2, ModeAutomated, 0)
				require.NoError(t, s.Next())
			}
			require.Equal(t, tc.want, reviewDecision(s.Tick(t0.Add(time.Millisecond))))
		})
	}
}

// TestScheduler_PreviousDuringAMoveTransition: during a transition from
// moves before a tick, as during one Next started, the cursor is on the
// item going out, so Previous goes back from it and never to the item the
// moves passed over.
func TestScheduler_PreviousDuringAMoveTransition(t *testing.T) {
	for _, presses := range []string{"NN", "nn", "jn"} {
		t.Run(presses, func(t *testing.T) {
			t0 := time.Unix(0, 0)
			s := nextNowScheduler(t, &TransitionConfig{Type: "dissolve", DurationMs: 500})
			require.NoError(t, s.JumpTo("b"))
			s.Tick(t0)
			for _, p := range presses {
				switch p {
				case 'N':
					require.NoError(t, s.NextNow())
				case 'n':
					require.NoError(t, s.Next())
				case 'j':
					require.NoError(t, s.JumpTo("c"))
				}
			}
			require.Equal(t, "b->d", reviewDecision(s.Tick(t0.Add(time.Millisecond))))
			snap := s.StatusSnapshot()
			require.Equal(t, 1, snap.Cursor, "the cursor left the item going out")
			require.Equal(t, 3, snap.Next)
			require.NoError(t, s.Previous())
			require.Equal(t, "a", reviewDecision(s.Tick(t0.Add(50*time.Millisecond))))
		})
	}
}

// TestScheduler_MovesBeforeATickWaitForTheSchedule: Next presses that land
// on an item scheduled later enter its wait, whether they count from the
// item on air or from a cursor JumpTo moved; NextNow takes it at once.
func TestScheduler_MovesBeforeATickWaitForTheSchedule(t *testing.T) {
	for _, tc := range []struct{ presses, want string }{
		{"nn", "idle"},
		{"jn", "idle"},
		{"jN", "c"},
	} {
		t.Run(tc.presses, func(t *testing.T) {
			t0 := time.Unix(0, 0)
			future := t0.Add(time.Hour)
			c := schedLiveItem("c", "cam-3")
			c.ScheduledTime = &future
			s := NewScheduler()
			require.NoError(t, s.LoadPlaylist(schedPlaylist(schedLiveItem("a", "cam-1"),
				schedLiveItem("b", "cam-2"), c, schedLiveItem("d", "cam-4"))))
			s.Start()
			s.Tick(t0)
			for _, p := range tc.presses {
				switch p {
				case 'n':
					require.NoError(t, s.Next())
				case 'N':
					require.NoError(t, s.NextNow())
				case 'j':
					require.NoError(t, s.JumpTo("b"))
				}
			}
			require.Equal(t, tc.want, reviewDecision(s.Tick(t0.Add(time.Millisecond))))
		})
	}
}

// TestScheduler_IdleWaitNamesTheWaitingItemNext: in a wait for a scheduled
// item, that item airs next, also with one Next pending, which starts it;
// a second press lands on the item after it.
func TestScheduler_IdleWaitNamesTheWaitingItemNext(t *testing.T) {
	t0 := time.Unix(0, 0)
	future := t0.Add(10 * time.Second)
	b := schedSlateItem("b", 5000)
	b.ScheduledTime = &future
	s := NewScheduler()
	require.NoError(t, s.LoadPlaylist(schedPlaylist(schedSlateItem("a", 100), b, schedSlateItem("c", 5000))))
	s.Start()
	s.Tick(t0)
	require.True(t, s.Tick(t0.Add(101*time.Millisecond)).IsIdle)
	require.Equal(t, 1, s.StatusSnapshot().Next)
	require.NoError(t, s.Next())
	require.Equal(t, 1, s.StatusSnapshot().Next)
	require.NoError(t, s.Next())
	require.Equal(t, 2, s.StatusSnapshot().Next)
}

// TestScheduler_NextAfterNextNowDuringATransitionCuts: NextNow during a
// transition cuts, so a Next after it before the tick cuts on to the item
// after, rather than transition out of the item NextNow took, which never
// aired.
func TestScheduler_NextAfterNextNowDuringATransitionCuts(t *testing.T) {
	t0 := time.Unix(0, 0)
	s := nextNowScheduler(t, &TransitionConfig{Type: "dissolve", DurationMs: 500})
	s.Tick(t0)
	require.NoError(t, s.Next())
	require.Equal(t, "a->b", reviewDecision(s.Tick(t0)))
	require.NoError(t, s.NextNow())
	require.NoError(t, s.Next())
	require.Equal(t, "d", reviewDecision(s.Tick(t0.Add(100*time.Millisecond))))
	require.Equal(t, 3, s.Cursor())
}

// TestScheduler_RestartingTheItemOnAir: Previous on the first item and a
// JumpTo to the item on air restart it, as they did before moves came back
// to the item on air: a slate's timer starts again.
func TestScheduler_RestartingTheItemOnAir(t *testing.T) {
	for _, op := range []string{"previous", "jump"} {
		t.Run(op, func(t *testing.T) {
			t0 := time.Unix(0, 0)
			s := NewScheduler()
			require.NoError(t, s.LoadPlaylist(schedPlaylist(schedSlateItem("a", 1000), schedSlateItem("b", 1000))))
			s.Start()
			s.Tick(t0)
			if op == "previous" {
				require.NoError(t, s.Previous())
			} else {
				require.NoError(t, s.JumpTo("a"))
			}
			require.Equal(t, "a", reviewDecision(s.Tick(t0.Add(600*time.Millisecond))))
			require.Equal(t, "a", reviewDecision(s.Tick(t0.Add(1500*time.Millisecond))), "the slate did not restart")
			require.Equal(t, "b", reviewDecision(s.Tick(t0.Add(1601*time.Millisecond))))
		})
	}
}

// TestScheduler_EndLoopActsOnTheItemPlaying: after Previous moved the
// cursor off a looping clip before a tick, that clip is still the one
// playing, so EndLoop releases its loop, whatever the item at the cursor
// is, and the release holds when Next comes back to it.
func TestScheduler_EndLoopActsOnTheItemPlaying(t *testing.T) {
	for _, xLoops := range []bool{true, false} {
		t.Run(fmt.Sprintf("x loops %v", xLoops), func(t *testing.T) {
			t0 := time.Unix(0, 0)
			a := schedClipItem("a", 0)
			a.Loop = true
			x := schedClipItem("x", 0)
			x.Loop = xLoops
			s := NewScheduler()
			require.NoError(t, s.LoadPlaylist(schedPlaylist(x, a, schedClipItem("b", 0))))
			s.Start()
			s.Tick(t0)
			require.NoError(t, s.JumpTo("a"))
			require.Equal(t, "a", reviewDecision(s.Tick(t0)))

			require.NoError(t, s.Previous())
			require.NoError(t, s.EndLoop(), "a loops and is playing")
			require.True(t, s.LoopExitPending())
			snap := s.StatusSnapshot()
			require.Equal(t, 1, snap.Playing)
			require.True(t, snap.LoopExit)
			require.NoError(t, s.Next())
			require.Equal(t, "a", reviewDecision(s.Tick(t0.Add(time.Millisecond))))
			require.True(t, s.LoopExitPending(), "a's loop release was lost")
			s.SignalItemComplete()
			require.Equal(t, "b", reviewDecision(s.Tick(t0.Add(2*time.Millisecond))))
		})
	}
}
