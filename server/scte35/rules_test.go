package scte35

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuleEngine_DeleteByCommandType(t *testing.T) {
	t.Parallel()
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

	require.Equal(t, ActionDelete, action)
}

func TestRuleEngine_PassBySegType(t *testing.T) {
	t.Parallel()
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

	require.Equal(t, ActionPass, action)
}

func TestRuleEngine_CompoundAND(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionPass, action)

	// Matches only seg type, duration too short
	msg2 := NewTimeSignal(0x34, 10*time.Second, 0x0F, []byte("test"))
	action2, _ := re.Evaluate(msg2, "")
	require.Equal(t, ActionDelete, action2)
}

func TestRuleEngine_CompoundOR(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionPass, action)
}

func TestRuleEngine_DefaultAction(t *testing.T) {
	t.Parallel()
	re := NewRuleEngine()
	re.SetDefaultAction(ActionDelete)

	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	action, _ := re.Evaluate(msg, "")

	require.Equal(t, ActionDelete, action)
}

func TestRuleEngine_DestinationFilter(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)

	// Should not match for dest2 (falls through to default=pass)
	action2, _ := re.Evaluate(msg, "dest2")
	require.Equal(t, ActionPass, action2)
}

func TestRuleEngine_DisabledRule(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionPass, action)
}

func TestRuleEngine_FirstMatchWins(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionPass, action)
}

func TestRuleEngine_ContainsOperator(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)
}

func TestRuleEngine_RangeOperator(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)

	msg2 := NewTimeSignal(30, 60*time.Second, 0x0F, []byte("test"))
	action2, _ := re.Evaluate(msg2, "")
	require.Equal(t, ActionPass, action2)
}

func TestRuleEngine_BackwardCompat_SingleCondition(t *testing.T) {
	t.Parallel()
	ruleJSON := `{
		"id": "r1",
		"name": "legacy",
		"enabled": true,
		"condition": {"field": "command_type", "operator": "=", "value": "5"},
		"action": "delete"
	}`
	var rule Rule
	err := json.Unmarshal([]byte(ruleJSON), &rule)
	require.NoError(t, err)
	require.Len(t, rule.Conditions, 1)
}

func TestRuleEngine_MatchesOperator(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)

	msg2 := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("https://other.net/avail/1"))
	action2, _ := re.Evaluate(msg2, "")
	require.Equal(t, ActionPass, action2)
}

func TestRuleEngine_SetRules(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionPass, action)
}

func TestRuleEngine_ReplaceAction(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionReplace, action)
	require.NotNil(t, modified)
	require.Equal(t, uint32(999), modified.EventID)
	require.NotNil(t, modified.BreakDuration)
	require.Equal(t, 15*time.Second, *modified.BreakDuration)
	// Original should be unmodified.
	require.Equal(t, uint32(1), msg.EventID)
}

func TestRuleEngine_NotEqualOperator(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)

	// splice_null (command_type=0, == 0) should not match.
	msgNull := &CueMessage{CommandType: CommandSpliceNull}
	action2, _ := re.Evaluate(msgNull, "")
	require.Equal(t, ActionPass, action2)
}

func TestRuleEngine_IsOutField(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)

	msgIn := NewSpliceInsert(1, 0, false, false)
	action2, _ := re.Evaluate(msgIn, "")
	require.Equal(t, ActionPass, action2)
}

func TestRuleEngine_EventIDField(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)

	msg2 := NewSpliceInsert(99, 30*time.Second, true, true)
	action2, _ := re.Evaluate(msg2, "")
	require.Equal(t, ActionPass, action2)
}

func TestRuleEngine_DurationComparison(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)

	msg2 := NewSpliceInsert(1, 60*time.Second, true, true)
	action2, _ := re.Evaluate(msg2, "")
	require.Equal(t, ActionPass, action2)
}

func TestRuleEngine_EmptyConditions(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionPass, action)
}

func TestRuleEngine_BackwardCompat_DefaultLogicAND(t *testing.T) {
	t.Parallel()
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
	err := json.Unmarshal([]byte(ruleJSON), &rule)
	require.NoError(t, err)
	// Logic should default to AND.
	require.Equal(t, LogicAND, rule.Logic)
}

func TestRuleEngine_ConcurrentAccess(t *testing.T) {
	t.Parallel()
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
				require.Equal(t, ActionDelete, action)
			}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestMatchRange_NegativeValues(t *testing.T) {
	t.Parallel()
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
		require.Equal(t, tt.want, got, "matchRange(%q, %q)", tt.val, tt.rangeStr)
	}
}

func TestRuleEngine_RegexCacheReuse(t *testing.T) {
	t.Parallel()
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:         "r1",
		Name:       "regex rule",
		Enabled:    true,
		Conditions: []RuleCondition{{Field: "upid", Operator: "matches", Value: `^https://`}},
		Action:     ActionDelete,
	})

	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("https://example.com"))

	// Evaluate twice -- second should use cached regex.
	action1, _ := re.Evaluate(msg, "")
	action2, _ := re.Evaluate(msg, "")

	require.Equal(t, ActionDelete, action1)
	require.Equal(t, ActionDelete, action2)

	// Verify cache contains the pattern.
	_, found := re.regexCache.Load(`^https://`)
	require.True(t, found, "expected regex pattern to be cached")

	// SetRules should clear cache.
	re.SetRules(nil)
	_, found = re.regexCache.Load(`^https://`)
	require.False(t, found, "expected cache to be cleared after SetRules")
}

func TestRuleEngine_MultiDescriptor_MatchesSecond(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)
}

func TestRuleEngine_MultiDescriptor_NoneMatch(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionPass, action)
}

func TestRuleEngine_SingleDescriptor_Unchanged(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)
}

func TestRuleEngine_NoDescriptors_SegTypeDoesNotMatch(t *testing.T) {
	t.Parallel()
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
		require.Equal(t, ActionPass, action)
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
		require.Equal(t, ActionPass, action)
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
		require.Equal(t, ActionPass, action)
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
		require.Equal(t, ActionPass, action)
	})
}
