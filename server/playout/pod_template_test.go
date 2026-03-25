package playout

import "testing"

func TestBuiltinTemplates(t *testing.T) {
	templates := BuiltinPodTemplates()
	if len(templates) != 4 {
		t.Fatalf("expected 4 builtin templates, got %d", len(templates))
	}

	q, ok := templates["quarter"]
	if !ok {
		t.Fatal("missing quarter template")
	}
	if q.DefaultDurationMs != 180000 {
		t.Errorf("quarter duration = %d, want 180000", q.DefaultDurationMs)
	}
	if !q.AutoReturn {
		t.Error("quarter should have auto_return")
	}
	if !q.AllowDumpOut {
		t.Error("quarter should allow dump out")
	}
	if q.Priority != PriorityNormal {
		t.Errorf("quarter priority = %v, want normal", q.Priority)
	}

	inj, ok := templates["injury"]
	if !ok {
		t.Fatal("missing injury template")
	}
	if inj.DefaultDurationMs != 0 {
		t.Errorf("injury duration = %d, want 0 (open-ended)", inj.DefaultDurationMs)
	}
	if inj.AutoReturn {
		t.Error("injury should not auto_return")
	}
	if !inj.HoldUntilManualReturn {
		t.Error("injury should hold until manual return")
	}
	if inj.Priority != PriorityHigh {
		t.Errorf("injury priority = %v, want high", inj.Priority)
	}

	ms, ok := templates["mandatory_sponsor"]
	if !ok {
		t.Fatal("missing mandatory_sponsor template")
	}
	if ms.AllowDumpOut {
		t.Error("mandatory_sponsor should not allow dump out")
	}
	if !ms.Contractual {
		t.Error("mandatory_sponsor should be contractual")
	}
	if ms.Priority != PriorityMandatory {
		t.Errorf("mandatory_sponsor priority = %v, want mandatory", ms.Priority)
	}

	to, ok := templates["timeout"]
	if !ok {
		t.Fatal("missing timeout template")
	}
	if !to.Ephemeral {
		t.Error("timeout should be ephemeral")
	}
	if to.DefaultDurationMs != 60000 {
		t.Errorf("timeout duration = %d, want 60000", to.DefaultDurationMs)
	}
}

func TestPodTemplate_Instantiate(t *testing.T) {
	tmpl := PodTemplate{
		Type:              "quarter",
		DefaultDurationMs: 180000,
		AutoReturn:        true,
		AllowDumpOut:      true,
		Priority:          PriorityNormal,
	}

	pod := tmpl.Instantiate("pod-123")
	if pod.ID != "pod-123" {
		t.Errorf("pod ID = %q, want %q", pod.ID, "pod-123")
	}
	if pod.Config.Type != "quarter" {
		t.Errorf("pod type = %q, want %q", pod.Config.Type, "quarter")
	}
	if pod.Config.DurationMs != 180000 {
		t.Errorf("pod duration = %d, want 180000", pod.Config.DurationMs)
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
	if len(all) != 4 {
		t.Fatalf("expected 4 templates, got %d", len(all))
	}

	// Get existing.
	tmpl, err := store.Get("quarter")
	if err != nil {
		t.Fatalf("Get(quarter): %v", err)
	}
	if tmpl.DefaultDurationMs != 180000 {
		t.Errorf("quarter duration = %d, want 180000", tmpl.DefaultDurationMs)
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
	if len(store.List()) != 5 {
		t.Fatalf("expected 5 templates after Put, got %d", len(store.List()))
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
	if len(store.List()) != 4 {
		t.Fatalf("expected 4 templates after Delete, got %d", len(store.List()))
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
