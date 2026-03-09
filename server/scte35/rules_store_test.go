package scte35

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRulesStore_CreateAndList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	if err != nil {
		t.Fatalf("LoadRulesStore: %v", err)
	}

	// Empty store should have no rules.
	if got := store.List(); len(got) != 0 {
		t.Fatalf("expected empty list, got %d rules", len(got))
	}

	// Create a rule.
	rule := Rule{
		Name:    "strip splice_inserts",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "command_type", Operator: "=", Value: "5"},
		},
		Logic:  LogicAND,
		Action: ActionDelete,
	}
	created, err := store.Create(rule)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// ID should be assigned (8-char hex).
	if len(created.ID) != 8 {
		t.Fatalf("expected 8-char ID, got %q", created.ID)
	}

	// Name should be preserved.
	if created.Name != "strip splice_inserts" {
		t.Fatalf("expected name 'strip splice_inserts', got %q", created.Name)
	}

	// List should return the created rule.
	rules := store.List()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].ID != created.ID {
		t.Fatalf("expected ID %q, got %q", created.ID, rules[0].ID)
	}
	if rules[0].Name != "strip splice_inserts" {
		t.Fatalf("expected name 'strip splice_inserts', got %q", rules[0].Name)
	}
	if rules[0].Action != ActionDelete {
		t.Fatalf("expected action delete, got %q", rules[0].Action)
	}
}

func TestRulesStore_Update(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	if err != nil {
		t.Fatalf("LoadRulesStore: %v", err)
	}

	created, err := store.Create(Rule{
		Name:    "original name",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "command_type", Operator: "=", Value: "5"},
		},
		Logic:  LogicAND,
		Action: ActionDelete,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update the rule.
	updated := created
	updated.Name = "updated name"
	updated.Conditions = []RuleCondition{
		{Field: "segmentation_type_id", Operator: "range", Value: "16-17"},
	}
	updated.Action = ActionPass

	if err := store.Update(created.ID, updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	rules := store.List()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Name != "updated name" {
		t.Fatalf("expected name 'updated name', got %q", rules[0].Name)
	}
	if rules[0].Action != ActionPass {
		t.Fatalf("expected action pass, got %q", rules[0].Action)
	}
	if len(rules[0].Conditions) != 1 || rules[0].Conditions[0].Value != "16-17" {
		t.Fatalf("conditions not updated: %+v", rules[0].Conditions)
	}

	// Update non-existent rule should fail.
	if err := store.Update("nonexistent", updated); err == nil {
		t.Fatal("expected error for non-existent rule")
	}
}

func TestRulesStore_Delete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	if err != nil {
		t.Fatalf("LoadRulesStore: %v", err)
	}

	r1, err := store.Create(Rule{
		Name:       "rule1",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Logic:      LogicAND,
		Action:     ActionDelete,
	})
	if err != nil {
		t.Fatalf("Create r1: %v", err)
	}

	r2, err := store.Create(Rule{
		Name:       "rule2",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "6"}},
		Logic:      LogicAND,
		Action:     ActionPass,
	})
	if err != nil {
		t.Fatalf("Create r2: %v", err)
	}

	// Delete rule1.
	if err := store.Delete(r1.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	rules := store.List()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule after delete, got %d", len(rules))
	}
	if rules[0].ID != r2.ID {
		t.Fatalf("expected remaining rule to be r2 (%q), got %q", r2.ID, rules[0].ID)
	}

	// Delete non-existent rule should fail.
	if err := store.Delete("nonexistent"); err == nil {
		t.Fatal("expected error for non-existent rule")
	}
}

func TestRulesStore_Reorder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	if err != nil {
		t.Fatalf("LoadRulesStore: %v", err)
	}

	r1, _ := store.Create(Rule{
		Name: "first", Enabled: true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Logic: LogicAND, Action: ActionDelete,
	})
	r2, _ := store.Create(Rule{
		Name: "second", Enabled: true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "6"}},
		Logic: LogicAND, Action: ActionPass,
	})
	r3, _ := store.Create(Rule{
		Name: "third", Enabled: true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "0"}},
		Logic: LogicAND, Action: ActionDelete,
	})

	// Reorder: third, first, second.
	if err := store.Reorder([]string{r3.ID, r1.ID, r2.ID}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}

	rules := store.List()
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}
	if rules[0].ID != r3.ID {
		t.Fatalf("expected first rule to be r3 (%q), got %q", r3.ID, rules[0].ID)
	}
	if rules[1].ID != r1.ID {
		t.Fatalf("expected second rule to be r1 (%q), got %q", r1.ID, rules[1].ID)
	}
	if rules[2].ID != r2.ID {
		t.Fatalf("expected third rule to be r2 (%q), got %q", r2.ID, rules[2].ID)
	}

	// Reorder with invalid ID should fail.
	if err := store.Reorder([]string{r3.ID, "invalid", r2.ID}); err == nil {
		t.Fatal("expected error for invalid ID in reorder")
	}

	// Reorder with wrong count should fail.
	if err := store.Reorder([]string{r3.ID, r1.ID}); err == nil {
		t.Fatal("expected error for wrong count in reorder")
	}
}

func TestRulesStore_DefaultAction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	if err != nil {
		t.Fatalf("LoadRulesStore: %v", err)
	}

	// Default should be ActionPass.
	if got := store.DefaultAction(); got != ActionPass {
		t.Fatalf("expected default action 'pass', got %q", got)
	}

	// Set default to delete.
	if err := store.SetDefaultAction(ActionDelete); err != nil {
		t.Fatalf("SetDefaultAction: %v", err)
	}
	if got := store.DefaultAction(); got != ActionDelete {
		t.Fatalf("expected default action 'delete', got %q", got)
	}

	// Engine should reflect the default action.
	engine := store.Engine()
	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := engine.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("engine default action should be delete, got %s", action)
	}
}

func TestRulesStore_Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")

	// Create store and add rules.
	store1, err := LoadRulesStore(path)
	if err != nil {
		t.Fatalf("LoadRulesStore (first): %v", err)
	}

	r1, err := store1.Create(Rule{
		Name:       "persistent rule",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Logic:      LogicAND,
		Action:     ActionDelete,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store1.SetDefaultAction(ActionDelete); err != nil {
		t.Fatalf("SetDefaultAction: %v", err)
	}

	// Create a new store from the same file.
	store2, err := LoadRulesStore(path)
	if err != nil {
		t.Fatalf("LoadRulesStore (second): %v", err)
	}

	// Rules should persist.
	rules := store2.List()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule in reloaded store, got %d", len(rules))
	}
	if rules[0].ID != r1.ID {
		t.Fatalf("expected rule ID %q, got %q", r1.ID, rules[0].ID)
	}
	if rules[0].Name != "persistent rule" {
		t.Fatalf("expected name 'persistent rule', got %q", rules[0].Name)
	}

	// Default action should persist.
	if got := store2.DefaultAction(); got != ActionDelete {
		t.Fatalf("expected persisted default action 'delete', got %q", got)
	}

	// Engine should be in sync.
	engine := store2.Engine()
	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := engine.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("engine should match persisted rule (delete), got %s", action)
	}
}

func TestRulesStore_PresetTemplates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	if err != nil {
		t.Fatalf("LoadRulesStore: %v", err)
	}

	// List templates.
	templates := store.Templates()
	if len(templates) < 5 {
		t.Fatalf("expected at least 5 templates, got %d", len(templates))
	}

	// Templates should have names but no IDs (they are templates, not persisted).
	for _, tmpl := range templates {
		if tmpl.ID != "" {
			t.Fatalf("template should have empty ID, got %q", tmpl.ID)
		}
		if tmpl.Name == "" {
			t.Fatal("template should have a name")
		}
		if tmpl.Enabled {
			t.Fatalf("template %q should be disabled by default", tmpl.Name)
		}
	}

	// Verify specific template names.
	names := make(map[string]bool)
	for _, tmpl := range templates {
		names[tmpl.Name] = true
	}
	expectedNames := []string{
		"Strip short avails",
		"Strip program boundaries",
		"Fix missing duration",
		"Pass placement opportunities",
		"Block non-SSAI signals",
	}
	for _, name := range expectedNames {
		if !names[name] {
			t.Fatalf("expected template %q not found", name)
		}
	}

	// Create from template.
	created, err := store.CreateFromTemplate("Strip short avails")
	if err != nil {
		t.Fatalf("CreateFromTemplate: %v", err)
	}

	// Created rule should have an ID and the template's properties.
	if len(created.ID) != 8 {
		t.Fatalf("expected 8-char ID, got %q", created.ID)
	}
	if created.Name != "Strip short avails" {
		t.Fatalf("expected name 'Strip short avails', got %q", created.Name)
	}
	// Template-created rules start disabled.
	if created.Enabled {
		t.Fatal("template-created rule should be disabled")
	}

	// The rule should be in the store's list.
	rules := store.List()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	// Create from non-existent template should fail.
	_, err = store.CreateFromTemplate("nonexistent template")
	if err == nil {
		t.Fatal("expected error for non-existent template")
	}
}

func TestRulesStore_EngineSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	if err != nil {
		t.Fatalf("LoadRulesStore: %v", err)
	}

	// Create a rule that deletes splice_inserts.
	_, err = store.Create(Rule{
		Name:       "delete inserts",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Logic:      LogicAND,
		Action:     ActionDelete,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Engine should evaluate the rule.
	engine := store.Engine()
	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := engine.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("expected delete from engine, got %s", action)
	}

	// Time signal should pass (no matching rule).
	tsMsg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("test"))
	action2, _ := engine.Evaluate(tsMsg, "")
	if action2 != ActionPass {
		t.Fatalf("expected pass for time_signal, got %s", action2)
	}
}
