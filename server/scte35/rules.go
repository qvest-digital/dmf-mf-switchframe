package scte35

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RuleAction is the action a rule engine returns after evaluation.
type RuleAction = string

// Action constants for rule evaluation results.
const (
	ActionPass    RuleAction = "pass"
	ActionDelete  RuleAction = "delete"
	ActionReplace RuleAction = "replace"
)

// ConditionLogic specifies how multiple conditions within a rule combine.
type ConditionLogic = string

// Logic constants for compound rule conditions.
const (
	LogicAND ConditionLogic = "and"
	LogicOR  ConditionLogic = "or"
)

// RuleCondition defines a single matching condition within a rule.
type RuleCondition struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

// ReplaceParams holds replacement parameters for ActionReplace rules.
type ReplaceParams struct {
	Duration *time.Duration `json:"duration,omitempty"`
	EventID  *uint32        `json:"eventID,omitempty"`
}

// Rule defines an SCTE-35 processing rule.
type Rule struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Enabled      bool            `json:"enabled"`
	Priority     int             `json:"priority,omitempty"`
	Conditions   []RuleCondition `json:"conditions,omitempty"`
	Logic        ConditionLogic  `json:"logic,omitempty"`
	Action       RuleAction      `json:"action"`
	ReplaceWith  *ReplaceParams  `json:"replaceWith,omitempty"`
	Destinations []string        `json:"destinations,omitempty"`
}

// UnmarshalJSON supports both singular "condition" and plural "conditions"
// for backward compatibility.
func (r *Rule) UnmarshalJSON(data []byte) error {
	type Alias Rule
	aux := &struct {
		*Alias
		Condition *RuleCondition `json:"condition,omitempty"`
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	// Promote singular "condition" to Conditions slice if Conditions wasn't set.
	if aux.Condition != nil && len(r.Conditions) == 0 {
		r.Conditions = []RuleCondition{*aux.Condition}
	}
	// Default logic to AND if unset.
	if r.Logic == "" {
		r.Logic = LogicAND
	}
	return nil
}

// RuleEngine evaluates SCTE-35 messages against an ordered set of rules.
// First matching rule wins.
type RuleEngine struct {
	mu            sync.RWMutex
	rules         []Rule
	defaultAction RuleAction
	regexCache    sync.Map // pattern string -> *regexp.Regexp
}

// NewRuleEngine creates a new RuleEngine with default action "pass".
func NewRuleEngine() *RuleEngine {
	return &RuleEngine{
		defaultAction: ActionPass,
	}
}

// AddRule appends a rule to the engine.
func (re *RuleEngine) AddRule(r Rule) {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.rules = append(re.rules, r)
	re.regexCache = sync.Map{}
}

// SetDefaultAction sets the action returned when no rule matches.
func (re *RuleEngine) SetDefaultAction(action RuleAction) {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.defaultAction = action
}

// SetRules replaces all rules atomically.
func (re *RuleEngine) SetRules(rules []Rule) {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.rules = make([]Rule, len(rules))
	copy(re.rules, rules)
	re.regexCache = sync.Map{}
}

// Evaluate checks msg against all rules in order (first-match wins).
// destID is used to filter rules by their Destinations list; pass "" to
// match rules with no destination filter.
// Returns the action and an optionally modified message (non-nil only for
// ActionReplace with ReplaceWith params).
func (re *RuleEngine) Evaluate(msg *CueMessage, destID string) (RuleAction, *CueMessage) {
	re.mu.RLock()
	defer re.mu.RUnlock()

	for _, r := range re.rules {
		if !r.Enabled {
			continue
		}

		// Destination filter: if rule specifies destinations, skip if destID
		// is not in the list.
		if len(r.Destinations) > 0 && !containsString(r.Destinations, destID) {
			continue
		}

		if re.matchRule(r, msg) {
			if r.Action == ActionReplace && r.ReplaceWith != nil {
				modified := applyReplace(msg, r.ReplaceWith)
				return r.Action, modified
			}
			return r.Action, nil
		}
	}

	return re.defaultAction, nil
}

// containsString checks if s is in the slice.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// applyReplace creates a copy of msg with ReplaceParams applied.
func applyReplace(msg *CueMessage, params *ReplaceParams) *CueMessage {
	cp := *msg // shallow copy
	// Deep copy Descriptors to prevent shared mutations.
	if len(cp.Descriptors) > 0 {
		descs := make([]SegmentationDescriptor, len(cp.Descriptors))
		copy(descs, cp.Descriptors)
		cp.Descriptors = descs
	}
	if params.Duration != nil {
		d := *params.Duration
		cp.BreakDuration = &d
	}
	if params.EventID != nil {
		cp.EventID = *params.EventID
	}
	return &cp
}

// getCompiledRegex returns a compiled regex from the cache, compiling on miss.
func (re *RuleEngine) getCompiledRegex(pattern string) (*regexp.Regexp, error) {
	if cached, ok := re.regexCache.Load(pattern); ok {
		return cached.(*regexp.Regexp), nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	re.regexCache.Store(pattern, compiled)
	return compiled, nil
}

// matchRule checks if all/any conditions in the rule match the message.
func (re *RuleEngine) matchRule(r Rule, msg *CueMessage) bool {
	if len(r.Conditions) == 0 {
		return false
	}

	logic := r.Logic
	if logic == "" {
		logic = LogicAND
	}

	if logic == LogicOR {
		for _, c := range r.Conditions {
			if re.evaluateCondition(c, msg) {
				return true
			}
		}
		return false
	}

	// Default: AND logic — all conditions must match.
	for _, c := range r.Conditions {
		if !re.evaluateCondition(c, msg) {
			return false
		}
	}
	return true
}

// evaluateCondition evaluates a single condition against a message.
func (re *RuleEngine) evaluateCondition(c RuleCondition, msg *CueMessage) bool {
	fieldVal := extractField(c.Field, msg)

	switch c.Operator {
	case "=":
		return fieldVal == c.Value
	case "!=":
		return fieldVal != c.Value
	case ">=":
		return compareNumeric(fieldVal, c.Value) >= 0
	case "<=":
		return compareNumeric(fieldVal, c.Value) <= 0
	case ">":
		return compareNumeric(fieldVal, c.Value) > 0
	case "<":
		return compareNumeric(fieldVal, c.Value) < 0
	case "contains":
		return strings.Contains(fieldVal, c.Value)
	case "range":
		return matchRange(fieldVal, c.Value)
	case "matches":
		compiled, err := re.getCompiledRegex(c.Value)
		if err != nil {
			return false
		}
		return compiled.MatchString(fieldVal)
	default:
		return false
	}
}

// extractField returns a string representation of a CueMessage field.
func extractField(field string, msg *CueMessage) string {
	switch field {
	case "command_type":
		return fmt.Sprintf("%d", msg.CommandType)
	case "event_id":
		return fmt.Sprintf("%d", msg.EventID)
	case "is_out":
		if msg.IsOut {
			return "true"
		}
		return "false"
	case "segmentation_type_id":
		if len(msg.Descriptors) > 0 {
			return fmt.Sprintf("%d", msg.Descriptors[0].SegmentationType)
		}
		return "0"
	case "duration":
		// Check top-level BreakDuration first (splice_insert).
		if msg.BreakDuration != nil {
			return fmt.Sprintf("%d", msg.BreakDuration.Milliseconds())
		}
		// Fall back to first descriptor's duration ticks (time_signal).
		if len(msg.Descriptors) > 0 && msg.Descriptors[0].DurationTicks != nil {
			ms := ticksToMillis(*msg.Descriptors[0].DurationTicks)
			return fmt.Sprintf("%d", ms)
		}
		return "0"
	case "upid":
		if len(msg.Descriptors) > 0 {
			return string(msg.Descriptors[0].UPID)
		}
		return ""
	default:
		return ""
	}
}

// ticksToMillis converts 90 kHz PTS ticks to milliseconds.
func ticksToMillis(ticks uint64) int64 {
	return int64(ticks * 1000 / 90000)
}

// compareNumeric compares two string-encoded numbers.
// Falls back to lexicographic comparison if either value is not a number.
func compareNumeric(a, b string) int {
	ai, errA := strconv.ParseFloat(a, 64)
	bi, errB := strconv.ParseFloat(b, 64)
	if errA != nil || errB != nil {
		return strings.Compare(a, b)
	}
	switch {
	case ai < bi:
		return -1
	case ai > bi:
		return 1
	default:
		return 0
	}
}

// matchRange checks if a numeric value is within a "min-max" range (inclusive).
// Handles negative values by finding the last '-' preceded by a digit (the
// separator), not a leading negative sign or the '-' after 'e'/'E' in floats.
func matchRange(val, rangeStr string) bool {
	// Find the separator '-': the last '-' that is preceded by a digit.
	sepIdx := -1
	for i := len(rangeStr) - 1; i > 0; i-- {
		if rangeStr[i] == '-' && rangeStr[i-1] >= '0' && rangeStr[i-1] <= '9' {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		return false
	}

	v, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return false
	}
	lo, err := strconv.ParseFloat(rangeStr[:sepIdx], 64)
	if err != nil {
		return false
	}
	hi, err := strconv.ParseFloat(rangeStr[sepIdx+1:], 64)
	if err != nil {
		return false
	}
	return v >= lo && v <= hi
}
