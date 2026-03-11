package scte35

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRulesStore_CreateAndList(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	require.NoError(t, err)

	// Empty store should have no rules.
	require.Len(t, store.List(), 0)

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
	require.NoError(t, err)

	// ID should be assigned (8-char hex).
	require.Len(t, created.ID, 8)

	// Name should be preserved.
	require.Equal(t, "strip splice_inserts", created.Name)

	// List should return the created rule.
	rules := store.List()
	require.Len(t, rules, 1)
	require.Equal(t, created.ID, rules[0].ID)
	require.Equal(t, "strip splice_inserts", rules[0].Name)
	require.Equal(t, ActionDelete, rules[0].Action)
}

func TestRulesStore_Update(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	require.NoError(t, err)

	created, err := store.Create(Rule{
		Name:    "original name",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "command_type", Operator: "=", Value: "5"},
		},
		Logic:  LogicAND,
		Action: ActionDelete,
	})
	require.NoError(t, err)

	// Update the rule.
	updated := created
	updated.Name = "updated name"
	updated.Conditions = []RuleCondition{
		{Field: "segmentation_type_id", Operator: "range", Value: "16-17"},
	}
	updated.Action = ActionPass

	require.NoError(t, store.Update(created.ID, updated))

	rules := store.List()
	require.Len(t, rules, 1)
	require.Equal(t, "updated name", rules[0].Name)
	require.Equal(t, ActionPass, rules[0].Action)
	require.Len(t, rules[0].Conditions, 1)
	require.Equal(t, "16-17", rules[0].Conditions[0].Value)

	// Update non-existent rule should fail.
	require.Error(t, store.Update("nonexistent", updated))
}

func TestRulesStore_Delete(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	require.NoError(t, err)

	r1, err := store.Create(Rule{
		Name:       "rule1",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Logic:      LogicAND,
		Action:     ActionDelete,
	})
	require.NoError(t, err)

	r2, err := store.Create(Rule{
		Name:       "rule2",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "6"}},
		Logic:      LogicAND,
		Action:     ActionPass,
	})
	require.NoError(t, err)

	// Delete rule1.
	require.NoError(t, store.Delete(r1.ID))

	rules := store.List()
	require.Len(t, rules, 1)
	require.Equal(t, r2.ID, rules[0].ID)

	// Delete non-existent rule should fail.
	require.Error(t, store.Delete("nonexistent"))
}

func TestRulesStore_Reorder(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	require.NoError(t, err)

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
	require.NoError(t, store.Reorder([]string{r3.ID, r1.ID, r2.ID}))

	rules := store.List()
	require.Len(t, rules, 3)
	require.Equal(t, r3.ID, rules[0].ID)
	require.Equal(t, r1.ID, rules[1].ID)
	require.Equal(t, r2.ID, rules[2].ID)

	// Reorder with invalid ID should fail.
	require.Error(t, store.Reorder([]string{r3.ID, "invalid", r2.ID}))

	// Reorder with wrong count should fail.
	require.Error(t, store.Reorder([]string{r3.ID, r1.ID}))
}

func TestRulesStore_DefaultAction(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	require.NoError(t, err)

	// Default should be ActionPass.
	require.Equal(t, ActionPass, store.DefaultAction())

	// Set default to delete.
	require.NoError(t, store.SetDefaultAction(ActionDelete))
	require.Equal(t, ActionDelete, store.DefaultAction())

	// Engine should reflect the default action.
	engine := store.Engine()
	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := engine.Evaluate(msg, "")
	require.Equal(t, ActionDelete, action)
}

func TestRulesStore_Persistence(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rules.json")

	// Create store and add rules.
	store1, err := LoadRulesStore(path)
	require.NoError(t, err)

	r1, err := store1.Create(Rule{
		Name:       "persistent rule",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Logic:      LogicAND,
		Action:     ActionDelete,
	})
	require.NoError(t, err)

	require.NoError(t, store1.SetDefaultAction(ActionDelete))

	// Create a new store from the same file.
	store2, err := LoadRulesStore(path)
	require.NoError(t, err)

	// Rules should persist.
	rules := store2.List()
	require.Len(t, rules, 1)
	require.Equal(t, r1.ID, rules[0].ID)
	require.Equal(t, "persistent rule", rules[0].Name)

	// Default action should persist.
	require.Equal(t, ActionDelete, store2.DefaultAction())

	// Engine should be in sync.
	engine := store2.Engine()
	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := engine.Evaluate(msg, "")
	require.Equal(t, ActionDelete, action)
}

func TestRulesStore_PresetTemplates(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	require.NoError(t, err)

	// List templates.
	templates := store.Templates()
	require.GreaterOrEqual(t, len(templates), 5)

	// Templates should have names but no IDs (they are templates, not persisted).
	for _, tmpl := range templates {
		require.Empty(t, tmpl.ID)
		require.NotEmpty(t, tmpl.Name)
		require.False(t, tmpl.Enabled, "template %q should be disabled by default", tmpl.Name)
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
		require.True(t, names[name], "expected template %q not found", name)
	}

	// Create from template.
	created, err := store.CreateFromTemplate("Strip short avails")
	require.NoError(t, err)

	// Created rule should have an ID and the template's properties.
	require.Len(t, created.ID, 8)
	require.Equal(t, "Strip short avails", created.Name)
	// Template-created rules start disabled.
	require.False(t, created.Enabled, "template-created rule should be disabled")

	// The rule should be in the store's list.
	rules := store.List()
	require.Len(t, rules, 1)

	// Create from non-existent template should fail.
	_, err = store.CreateFromTemplate("nonexistent template")
	require.Error(t, err)
}

func TestRulesStore_EngineSync(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	require.NoError(t, err)

	// Create a rule that deletes splice_inserts.
	_, err = store.Create(Rule{
		Name:       "delete inserts",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Logic:      LogicAND,
		Action:     ActionDelete,
	})
	require.NoError(t, err)

	// Engine should evaluate the rule.
	engine := store.Engine()
	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := engine.Evaluate(msg, "")
	require.Equal(t, ActionDelete, action)

	// Time signal should pass (no matching rule).
	tsMsg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("test"))
	action2, _ := engine.Evaluate(tsMsg, "")
	require.Equal(t, ActionPass, action2)
}

// TestRulesStore_EnginePointerStableAfterCRUD verifies that the engine pointer
// returned by Engine() remains the same object after CRUD operations.
// This is critical because the injector holds a pointer to the engine at startup;
// if syncEngine() replaces the pointer, the injector becomes stale.
func TestRulesStore_EnginePointerStableAfterCRUD(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rules.json")
	store, err := LoadRulesStore(path)
	require.NoError(t, err)

	// Grab the engine pointer before any CRUD operations.
	engineBefore := store.Engine()

	// --- Create: add an enabled rule that deletes splice_inserts ---
	created, err := store.Create(Rule{
		Name:       "delete inserts",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Logic:      LogicAND,
		Action:     ActionDelete,
	})
	require.NoError(t, err)

	engineAfterCreate := store.Engine()
	require.Same(t, engineBefore, engineAfterCreate, "engine pointer changed after Create — injector would hold stale reference")

	// The original pointer should see the new rule take effect.
	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := engineBefore.Evaluate(msg, "")
	require.Equal(t, ActionDelete, action)

	// --- Update: change the rule action to pass ---
	updated := created
	updated.Action = ActionPass
	require.NoError(t, store.Update(created.ID, updated))

	engineAfterUpdate := store.Engine()
	require.Same(t, engineBefore, engineAfterUpdate, "engine pointer changed after Update — injector would hold stale reference")

	action, _ = engineBefore.Evaluate(msg, "")
	require.Equal(t, ActionPass, action)

	// --- Delete: remove the rule, should fall back to default action ---
	require.NoError(t, store.Delete(created.ID))

	engineAfterDelete := store.Engine()
	require.Same(t, engineBefore, engineAfterDelete, "engine pointer changed after Delete — injector would hold stale reference")

	action, _ = engineBefore.Evaluate(msg, "")
	require.Equal(t, ActionPass, action)

	// --- SetDefaultAction: change default to delete ---
	require.NoError(t, store.SetDefaultAction(ActionDelete))

	engineAfterDefault := store.Engine()
	require.Same(t, engineBefore, engineAfterDefault, "engine pointer changed after SetDefaultAction — injector would hold stale reference")

	action, _ = engineBefore.Evaluate(msg, "")
	require.Equal(t, ActionDelete, action)

	// --- Reorder: create two rules, reorder, verify pointer stable ---
	// Reset default to pass first.
	require.NoError(t, store.SetDefaultAction(ActionPass))

	r1, err := store.Create(Rule{
		Name:       "rule A",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Logic:      LogicAND,
		Action:     ActionDelete,
	})
	require.NoError(t, err)
	r2, err := store.Create(Rule{
		Name:       "rule B",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Logic:      LogicAND,
		Action:     ActionPass,
	})
	require.NoError(t, err)

	// First-match-wins: r1 (delete) should win since it was created first.
	action, _ = engineBefore.Evaluate(msg, "")
	require.Equal(t, ActionDelete, action)

	// Reorder: put r2 first so pass wins.
	require.NoError(t, store.Reorder([]string{r2.ID, r1.ID}))

	engineAfterReorder := store.Engine()
	require.Same(t, engineBefore, engineAfterReorder, "engine pointer changed after Reorder — injector would hold stale reference")

	action, _ = engineBefore.Evaluate(msg, "")
	require.Equal(t, ActionPass, action)
}
