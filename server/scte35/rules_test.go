package scte35

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRuleEngine_DeleteByCommandType(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:      "r1",
		Name:    "strip splice_inserts",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "command_type", Operator: "=", Value: "5"},
		},
		Action: ActionDelete,
	})

	dur := 30 * time.Second
	msg := NewSpliceInsert(1, dur, true, true)
	action, _ := re.Evaluate(msg, "")

	if action != ActionDelete {
		t.Fatalf("expected delete, got %s", action)
	}
}

func TestRuleEngine_PassBySegType(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:      "r1",
		Name:    "pass placements",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "segmentation_type_id", Operator: "range", Value: "52-55"},
		},
		Action: ActionPass,
	})

	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("test")) // 0x34 = 52
	action, _ := re.Evaluate(msg, "")

	if action != ActionPass {
		t.Fatalf("expected pass, got %s", action)
	}
}

func TestRuleEngine_CompoundAND(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:      "r1",
		Name:    "long SSAI avails only",
		Enabled: true,
		Logic:   LogicAND,
		Conditions: []RuleCondition{
			{Field: "segmentation_type_id", Operator: "range", Value: "52-53"},
			{Field: "duration", Operator: ">=", Value: "30000"},
		},
		Action: ActionPass,
	})
	re.SetDefaultAction(ActionDelete)

	// Matches both: seg type 52 + duration 60s
	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("test"))
	action, _ := re.Evaluate(msg, "")
	if action != ActionPass {
		t.Fatalf("expected pass for matching compound AND, got %s", action)
	}

	// Matches only seg type, duration too short
	msg2 := NewTimeSignal(0x34, 10*time.Second, 0x0F, []byte("test"))
	action2, _ := re.Evaluate(msg2, "")
	if action2 != ActionDelete {
		t.Fatalf("expected default delete for partial compound AND, got %s", action2)
	}
}

func TestRuleEngine_CompoundOR(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:      "r1",
		Name:    "pass either type",
		Enabled: true,
		Logic:   LogicOR,
		Conditions: []RuleCondition{
			{Field: "command_type", Operator: "=", Value: "5"},
			{Field: "command_type", Operator: "=", Value: "6"},
		},
		Action: ActionPass,
	})
	re.SetDefaultAction(ActionDelete)

	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := re.Evaluate(msg, "")
	if action != ActionPass {
		t.Fatalf("expected pass for OR match, got %s", action)
	}
}

func TestRuleEngine_DefaultAction(t *testing.T) {
	re := NewRuleEngine()
	re.SetDefaultAction(ActionDelete)

	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := re.Evaluate(msg, "")

	if action != ActionDelete {
		t.Fatalf("expected default delete, got %s", action)
	}
}

func TestRuleEngine_DestinationFilter(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:           "r1",
		Name:         "dest1 only",
		Enabled:      true,
		Conditions:   []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Action:       ActionDelete,
		Destinations: []string{"dest1"},
	})

	msg := NewSpliceInsert(1, 30*time.Second, true, true)

	// Should match for dest1
	action, _ := re.Evaluate(msg, "dest1")
	if action != ActionDelete {
		t.Fatalf("expected delete for dest1, got %s", action)
	}

	// Should not match for dest2 (falls through to default=pass)
	action2, _ := re.Evaluate(msg, "dest2")
	if action2 != ActionPass {
		t.Fatalf("expected pass for dest2, got %s", action2)
	}
}

func TestRuleEngine_DisabledRule(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "disabled",
		Enabled:    false,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Action:     ActionDelete,
	})

	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := re.Evaluate(msg, "")
	if action != ActionPass {
		t.Fatalf("expected pass (disabled rule), got %s", action)
	}
}

func TestRuleEngine_FirstMatchWins(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "first",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Action:     ActionPass,
	})
	re.AddRule(Rule{
		ID:         "r2",
		Name:       "second",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Action:     ActionDelete,
	})

	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := re.Evaluate(msg, "")
	if action != ActionPass {
		t.Fatalf("expected pass (first match), got %s", action)
	}
}

func TestRuleEngine_ContainsOperator(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "contains test",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "upid", Operator: "contains", Value: "example.com"}},
		Action:     ActionDelete,
	})

	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("https://example.com/ad/1"))
	action, _ := re.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("expected delete for contains match, got %s", action)
	}
}

func TestRuleEngine_RangeOperator(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "range test",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "segmentation_type_id", Operator: "range", Value: "16-25"}},
		Action:     ActionDelete,
	})

	msg := NewTimeSignal(20, 60*time.Second, 0x0F, []byte("test"))
	action, _ := re.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("expected delete for range match, got %s", action)
	}

	msg2 := NewTimeSignal(30, 60*time.Second, 0x0F, []byte("test"))
	action2, _ := re.Evaluate(msg2, "")
	if action2 != ActionPass {
		t.Fatalf("expected pass for out-of-range, got %s", action2)
	}
}

func TestRuleEngine_BackwardCompat_SingleCondition(t *testing.T) {
	ruleJSON := `{
		"id": "r1",
		"name": "legacy",
		"enabled": true,
		"condition": {"field": "command_type", "operator": "=", "value": "5"},
		"action": "delete"
	}`
	var rule Rule
	if err := json.Unmarshal([]byte(ruleJSON), &rule); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(rule.Conditions) != 1 {
		t.Fatalf("expected 1 condition from singular key, got %d", len(rule.Conditions))
	}
}

func TestRuleEngine_MatchesOperator(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "regex match",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "upid", Operator: "matches", Value: `^https://.*\.example\.com/`}},
		Action:     ActionDelete,
	})

	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("https://ads.example.com/avail/1"))
	action, _ := re.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("expected delete for matches, got %s", action)
	}

	msg2 := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("https://other.net/avail/1"))
	action2, _ := re.Evaluate(msg2, "")
	if action2 != ActionPass {
		t.Fatalf("expected pass for non-matching regex, got %s", action2)
	}
}

func TestRuleEngine_SetRules(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "old",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Action:     ActionDelete,
	})

	// Replace all rules.
	re.SetRules([]Rule{
		{
			ID:         "r2",
			Name:       "new",
			Enabled:    true,
			Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
			Action:     ActionPass,
		},
	})

	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := re.Evaluate(msg, "")
	if action != ActionPass {
		t.Fatalf("expected pass after SetRules, got %s", action)
	}
}

func TestRuleEngine_ReplaceAction(t *testing.T) {
	newDur := 15 * time.Second
	newEventID := uint32(999)
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "replace duration and event",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Action:     ActionReplace,
		ReplaceWith: &ReplaceParams{
			Duration: &newDur,
			EventID:  &newEventID,
		},
	})

	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, modified := re.Evaluate(msg, "")
	if action != ActionReplace {
		t.Fatalf("expected replace, got %s", action)
	}
	if modified == nil {
		t.Fatal("expected modified message for replace action")
	}
	if modified.EventID != 999 {
		t.Fatalf("expected event ID 999, got %d", modified.EventID)
	}
	if modified.BreakDuration == nil || *modified.BreakDuration != 15*time.Second {
		t.Fatalf("expected 15s duration, got %v", modified.BreakDuration)
	}
	// Original should be unmodified.
	if msg.EventID != 1 {
		t.Fatalf("original msg event ID mutated: %d", msg.EventID)
	}
}

func TestRuleEngine_NotEqualOperator(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "not splice_null",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "!=", Value: "0"}},
		Action:     ActionDelete,
	})

	// splice_insert (command_type=5, != 0) should match.
	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := re.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("expected delete for !=0, got %s", action)
	}

	// splice_null (command_type=0, == 0) should not match.
	msgNull := &CueMessage{CommandType: CommandSpliceNull}
	action2, _ := re.Evaluate(msgNull, "")
	if action2 != ActionPass {
		t.Fatalf("expected pass for ==0, got %s", action2)
	}
}

func TestRuleEngine_IsOutField(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "cue-out only",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "is_out", Operator: "=", Value: "true"}},
		Action:     ActionDelete,
	})

	msgOut := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := re.Evaluate(msgOut, "")
	if action != ActionDelete {
		t.Fatalf("expected delete for is_out=true, got %s", action)
	}

	msgIn := NewSpliceInsert(1, 0, false, false)
	action2, _ := re.Evaluate(msgIn, "")
	if action2 != ActionPass {
		t.Fatalf("expected pass for is_out=false, got %s", action2)
	}
}

func TestRuleEngine_EventIDField(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "specific event",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "event_id", Operator: "=", Value: "42"}},
		Action:     ActionDelete,
	})

	msg := NewSpliceInsert(42, 30*time.Second, true, true)
	action, _ := re.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("expected delete for event_id=42, got %s", action)
	}

	msg2 := NewSpliceInsert(99, 30*time.Second, true, true)
	action2, _ := re.Evaluate(msg2, "")
	if action2 != ActionPass {
		t.Fatalf("expected pass for event_id=99, got %s", action2)
	}
}

func TestRuleEngine_DurationComparison(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:      "r1",
		Name:    "short breaks",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "duration", Operator: "<", Value: "15000"},
		},
		Action: ActionDelete,
	})

	msg := NewSpliceInsert(1, 10*time.Second, true, true)
	action, _ := re.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("expected delete for 10s < 15s, got %s", action)
	}

	msg2 := NewSpliceInsert(1, 60*time.Second, true, true)
	action2, _ := re.Evaluate(msg2, "")
	if action2 != ActionPass {
		t.Fatalf("expected pass for 60s >= 15s, got %s", action2)
	}
}

func TestRuleEngine_EmptyConditions(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:      "r1",
		Name:    "no conditions",
		Enabled: true,
		Action:  ActionDelete,
	})

	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := re.Evaluate(msg, "")
	// Rule with no conditions should not match.
	if action != ActionPass {
		t.Fatalf("expected pass for empty conditions, got %s", action)
	}
}

func TestRuleEngine_BackwardCompat_DefaultLogicAND(t *testing.T) {
	ruleJSON := `{
		"id": "r1",
		"name": "no logic field",
		"enabled": true,
		"conditions": [
			{"field": "command_type", "operator": "=", "value": "5"},
			{"field": "is_out", "operator": "=", "value": "true"}
		],
		"action": "delete"
	}`
	var rule Rule
	if err := json.Unmarshal([]byte(ruleJSON), &rule); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	// Logic should default to AND.
	if rule.Logic != LogicAND {
		t.Fatalf("expected default logic AND, got %q", rule.Logic)
	}
}

func TestRuleEngine_ConcurrentAccess(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "concurrent",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "command_type", Operator: "=", Value: "5"}},
		Action:     ActionDelete,
	})

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			msg := NewSpliceInsert(1, 30*time.Second, true, true)
			for j := 0; j < 100; j++ {
				action, _ := re.Evaluate(msg, "")
				if action != ActionDelete {
					t.Errorf("expected delete, got %s", action)
				}
			}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestMatchRange_NegativeValues(t *testing.T) {
	tests := []struct {
		val, rangeStr string
		want          bool
	}{
		// Standard positive range.
		{"5", "0-10", true},
		{"15", "0-10", false},
		// Negative min, positive max.
		{"-5", "-10-5", true},
		{"0", "-10-5", true},
		{"6", "-10-5", false},
		{"-11", "-10-5", false},
		// Both negative.
		{"-5", "-10--1", true},
		{"-10", "-10--1", true},
		{"-1", "-10--1", true},
		{"0", "-10--1", false},
		// Single values.
		{"3", "3-3", true},
		{"4", "3-3", false},
	}
	for _, tt := range tests {
		got := matchRange(tt.val, tt.rangeStr)
		if got != tt.want {
			t.Errorf("matchRange(%q, %q) = %v, want %v", tt.val, tt.rangeStr, got, tt.want)
		}
	}
}

func TestRuleEngine_RegexCacheReuse(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "regex rule",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "upid", Operator: "matches", Value: `^https://`}},
		Action:     ActionDelete,
	})

	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("https://example.com"))

	// Evaluate twice — second should use cached regex.
	action1, _ := re.Evaluate(msg, "")
	action2, _ := re.Evaluate(msg, "")

	if action1 != ActionDelete {
		t.Fatalf("first eval: expected delete, got %s", action1)
	}
	if action2 != ActionDelete {
		t.Fatalf("second eval: expected delete, got %s", action2)
	}

	// Verify cache contains the pattern.
	_, found := re.regexCache.Load(`^https://`)
	if !found {
		t.Error("expected regex pattern to be cached")
	}

	// SetRules should clear cache.
	re.SetRules(nil)
	_, found = re.regexCache.Load(`^https://`)
	if found {
		t.Error("expected cache to be cleared after SetRules")
	}
}

func TestRuleEngine_MultiDescriptor_MatchesSecond(t *testing.T) {
	// Rule matches segmentation_type_id=52 (0x34). Message has two descriptors:
	// type 48 (0x30) and type 52 (0x34). Rule should match the second descriptor.
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "match type 52",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "segmentation_type_id", Operator: "=", Value: "52"}},
		Action:     ActionDelete,
	})

	dur1 := uint64(900000)
	dur2 := uint64(2700000)
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x30, DurationTicks: &dur1, UPIDType: 0x0F, UPID: []byte("first")},
			{SegmentationType: 0x34, DurationTicks: &dur2, UPIDType: 0x0F, UPID: []byte("second")},
		},
	}

	action, _ := re.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("expected delete (match second descriptor), got %s", action)
	}
}

func TestRuleEngine_MultiDescriptor_NoneMatch(t *testing.T) {
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "match type 99",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "segmentation_type_id", Operator: "=", Value: "99"}},
		Action:     ActionDelete,
	})

	dur := uint64(900000)
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x30, DurationTicks: &dur},
			{SegmentationType: 0x34, DurationTicks: &dur},
		},
	}

	action, _ := re.Evaluate(msg, "")
	if action != ActionPass {
		t.Fatalf("expected pass (no descriptor matches), got %s", action)
	}
}

func TestRuleEngine_SingleDescriptor_Unchanged(t *testing.T) {
	// Regression: single descriptor behavior identical to before.
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "match type 52",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "segmentation_type_id", Operator: "=", Value: "52"}},
		Action:     ActionDelete,
	})

	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("test"))
	action, _ := re.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("expected delete for single descriptor match, got %s", action)
	}
}

func TestRuleEngine_NoDescriptors_SegTypeDoesNotMatch(t *testing.T) {
	// A message with no descriptors should NOT match rules on segmentation_type_id
	// or duration. Previously, extractFieldValues returned []string{"0"} for these
	// fields when no descriptors existed, causing rules like "< 50" or "= 0" to
	// incorrectly match descriptor-less messages.

	t.Run("segmentation_type_id equals zero", func(t *testing.T) {
		re := NewRuleEngine()
		re.AddRule(Rule{
			ID:         "r1",
			Name:       "match seg type 0",
			Enabled:    true,
			Conditions: []RuleCondition{{Field: "segmentation_type_id", Operator: "=", Value: "0"}},
			Action:     ActionDelete,
		})

		// splice_insert has no descriptors
		msg := NewSpliceInsert(1, 30*time.Second, true, true)
		action, _ := re.Evaluate(msg, "")
		if action != ActionPass {
			t.Fatalf("expected pass for descriptor-less message, got %s", action)
		}
	})

	t.Run("segmentation_type_id less than 50", func(t *testing.T) {
		re := NewRuleEngine()
		re.AddRule(Rule{
			ID:         "r1",
			Name:       "match seg type < 50",
			Enabled:    true,
			Conditions: []RuleCondition{{Field: "segmentation_type_id", Operator: "<", Value: "50"}},
			Action:     ActionDelete,
		})

		msg := NewSpliceInsert(1, 30*time.Second, true, true)
		action, _ := re.Evaluate(msg, "")
		if action != ActionPass {
			t.Fatalf("expected pass for descriptor-less message, got %s", action)
		}
	})

	t.Run("duration equals zero no break_duration", func(t *testing.T) {
		re := NewRuleEngine()
		re.AddRule(Rule{
			ID:         "r1",
			Name:       "match duration 0",
			Enabled:    true,
			Conditions: []RuleCondition{{Field: "duration", Operator: "=", Value: "0"}},
			Action:     ActionDelete,
		})

		// splice_insert with no duration and no descriptors
		msg := NewSpliceInsert(1, 0, false, false)
		action, _ := re.Evaluate(msg, "")
		if action != ActionPass {
			t.Fatalf("expected pass for descriptor-less message with no duration, got %s", action)
		}
	})

	t.Run("duration less than 15000 no break_duration", func(t *testing.T) {
		re := NewRuleEngine()
		re.AddRule(Rule{
			ID:         "r1",
			Name:       "short breaks",
			Enabled:    true,
			Conditions: []RuleCondition{{Field: "duration", Operator: "<", Value: "15000"}},
			Action:     ActionDelete,
		})

		// splice_insert with no duration and no descriptors
		msg := NewSpliceInsert(1, 0, false, false)
		action, _ := re.Evaluate(msg, "")
		if action != ActionPass {
			t.Fatalf("expected pass for descriptor-less message with no duration, got %s", action)
		}
	})
}
