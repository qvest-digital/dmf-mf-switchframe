package playout

import "testing"

func TestPodTemplateStore_Builtins_AreGeneric(t *testing.T) {
	store := NewPodTemplateStore()
	all := store.List()
	if len(all) != 3 {
		t.Fatalf("expected 3 builtin templates, got %d", len(all))
	}

	// Verify generic builtins exist.
	for _, name := range []string{"short", "standard", "long"} {
		tmpl, err := store.Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		if tmpl.Type != name {
			t.Errorf("template %q: Type = %q, want %q", name, tmpl.Type, name)
		}
		if !tmpl.AutoReturn {
			t.Errorf("template %q: expected auto_return", name)
		}
		if !tmpl.AllowDumpOut {
			t.Errorf("template %q: expected allow_dump_out", name)
		}
		if tmpl.Priority != PriorityNormal {
			t.Errorf("template %q: Priority = %v, want normal", name, tmpl.Priority)
		}
	}

	// Verify specific durations.
	short, _ := store.Get("short")
	if short.DefaultDurationMs != 30000 {
		t.Errorf("short duration = %d, want 30000", short.DefaultDurationMs)
	}
	standard, _ := store.Get("standard")
	if standard.DefaultDurationMs != 120000 {
		t.Errorf("standard duration = %d, want 120000", standard.DefaultDurationMs)
	}
	long, _ := store.Get("long")
	if long.DefaultDurationMs != 300000 {
		t.Errorf("long duration = %d, want 300000", long.DefaultDurationMs)
	}

	// Verify sport-specific templates are gone.
	for _, name := range []string{"quarter", "timeout", "injury", "mandatory_sponsor"} {
		_, err := store.Get(name)
		if err == nil {
			t.Errorf("expected sport-specific template %q to not exist", name)
		}
	}
}

func TestPodTemplateStore_LoadFromPlaylist(t *testing.T) {
	store := NewPodTemplateStore()

	defs := []PodTemplateDef{
		{Label: "halftime", DurationMs: 600000, AutoReturn: true, Priority: PriorityNormal},
		{Label: "injury_timeout", DurationMs: 0, AutoReturn: false, Priority: PriorityHigh},
	}
	store.LoadFromPlaylist(defs)

	// Manifest templates should be available.
	ht, err := store.Get("halftime")
	if err != nil {
		t.Fatalf("Get(halftime): %v", err)
	}
	if ht.DefaultDurationMs != 600000 {
		t.Errorf("halftime duration = %d, want 600000", ht.DefaultDurationMs)
	}
	if !ht.AutoReturn {
		t.Error("halftime should have auto_return")
	}
	if !ht.AllowDumpOut {
		t.Error("halftime should allow dump out")
	}

	it, err := store.Get("injury_timeout")
	if err != nil {
		t.Fatalf("Get(injury_timeout): %v", err)
	}
	if it.DefaultDurationMs != 0 {
		t.Errorf("injury_timeout duration = %d, want 0", it.DefaultDurationMs)
	}
	if it.AutoReturn {
		t.Error("injury_timeout should not have auto_return")
	}
	if it.Priority != PriorityHigh {
		t.Errorf("injury_timeout priority = %v, want high", it.Priority)
	}

	// Builtins should still be present.
	for _, name := range []string{"short", "standard", "long"} {
		if _, err := store.Get(name); err != nil {
			t.Errorf("builtin %q should still exist after LoadFromPlaylist: %v", name, err)
		}
	}
}

func TestPodTemplateStore_LoadFromPlaylist_OverridesBuiltin(t *testing.T) {
	store := NewPodTemplateStore()

	// Override the "short" builtin with a manifest definition.
	defs := []PodTemplateDef{
		{Label: "short", DurationMs: 15000, AutoReturn: false, Priority: PriorityHigh},
	}
	store.LoadFromPlaylist(defs)

	tmpl, err := store.Get("short")
	if err != nil {
		t.Fatalf("Get(short): %v", err)
	}
	if tmpl.DefaultDurationMs != 15000 {
		t.Errorf("overridden short duration = %d, want 15000", tmpl.DefaultDurationMs)
	}
	if tmpl.AutoReturn {
		t.Error("overridden short should not auto_return")
	}
	if tmpl.Priority != PriorityHigh {
		t.Errorf("overridden short priority = %v, want high", tmpl.Priority)
	}
}

func TestPodTemplateStore_LoadFromPlaylist_ClearsOld(t *testing.T) {
	store := NewPodTemplateStore()

	// First load: add "halftime".
	store.LoadFromPlaylist([]PodTemplateDef{
		{Label: "halftime", DurationMs: 600000, AutoReturn: true},
	})
	if _, err := store.Get("halftime"); err != nil {
		t.Fatalf("halftime should exist after first load: %v", err)
	}

	// Second load: different set, "halftime" should be gone.
	store.LoadFromPlaylist([]PodTemplateDef{
		{Label: "postgame", DurationMs: 300000, AutoReturn: true},
	})
	if _, err := store.Get("halftime"); err == nil {
		t.Error("halftime should be cleared after second LoadFromPlaylist")
	}
	if _, err := store.Get("postgame"); err != nil {
		t.Fatalf("postgame should exist after second load: %v", err)
	}

	// Builtins should survive.
	for _, name := range []string{"short", "standard", "long"} {
		if _, err := store.Get(name); err != nil {
			t.Errorf("builtin %q should survive LoadFromPlaylist: %v", name, err)
		}
	}
}

func TestPodTemplate_Instantiate(t *testing.T) {
	tmpl := PodTemplate{
		Type:              "standard",
		DefaultDurationMs: 120000,
		AutoReturn:        true,
		AllowDumpOut:      true,
		Priority:          PriorityNormal,
	}

	pod := tmpl.Instantiate("pod-123")
	if pod.ID != "pod-123" {
		t.Errorf("pod ID = %q, want %q", pod.ID, "pod-123")
	}
	if pod.Config.Type != "standard" {
		t.Errorf("pod type = %q, want %q", pod.Config.Type, "standard")
	}
	if pod.Config.DurationMs != 120000 {
		t.Errorf("pod duration = %d, want 120000", pod.Config.DurationMs)
	}
	if pod.State != PodScheduled {
		t.Errorf("pod state = %v, want scheduled", pod.State)
	}
	if pod.Armed {
		t.Error("pod should not be armed initially")
	}
}

func TestPodTemplateStore_CRUD(t *testing.T) {
	store := NewPodTemplateStore()

	// List builtins.
	all := store.List()
	if len(all) != 3 {
		t.Fatalf("expected 3 templates, got %d", len(all))
	}

	// Get existing.
	tmpl, err := store.Get("standard")
	if err != nil {
		t.Fatalf("Get(standard): %v", err)
	}
	if tmpl.DefaultDurationMs != 120000 {
		t.Errorf("standard duration = %d, want 120000", tmpl.DefaultDurationMs)
	}

	// Create custom.
	custom := PodTemplate{
		Type:              "halftime",
		DefaultDurationMs: 600000,
		AutoReturn:        true,
		AllowDumpOut:      true,
		Priority:          PriorityNormal,
	}
	err = store.Put(custom)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if len(store.List()) != 4 {
		t.Fatalf("expected 4 templates after Put, got %d", len(store.List()))
	}

	// Update existing.
	custom.DefaultDurationMs = 900000
	err = store.Put(custom)
	if err != nil {
		t.Fatalf("Put update: %v", err)
	}
	got, _ := store.Get("halftime")
	if got.DefaultDurationMs != 900000 {
		t.Errorf("updated duration = %d, want 900000", got.DefaultDurationMs)
	}

	// Delete.
	err = store.Delete("halftime")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(store.List()) != 3 {
		t.Fatalf("expected 3 templates after Delete, got %d", len(store.List()))
	}

	// Delete nonexistent.
	err = store.Delete("nonexistent")
	if err == nil {
		t.Fatal("expected error deleting nonexistent template")
	}

	// Get nonexistent.
	_, err = store.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error getting nonexistent template")
	}
}
