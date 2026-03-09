package scte35

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	// ErrRuleNotFound is returned when a rule with the given ID does not exist.
	ErrRuleNotFound = errors.New("scte35: rule not found")

	// ErrTemplateNotFound is returned when a template with the given name does not exist.
	ErrTemplateNotFound = errors.New("scte35: template not found")
)

// rulesFile is the JSON on-disk format for the rules store.
type rulesFile struct {
	Rules         []Rule     `json:"rules"`
	DefaultAction RuleAction `json:"defaultAction"`
}

// RulesStore manages CRUD operations and file persistence for SCTE-35 rules.
// It mirrors the macro.Store pattern: file-based JSON with sync.RWMutex
// and atomic temp-file + rename writes.
type RulesStore struct {
	mu            sync.RWMutex
	path          string
	rules         []Rule
	defaultAction RuleAction
	engine        *RuleEngine
}

// LoadRulesStore loads a RulesStore from the given file path.
// If the file does not exist, the store starts empty with default action "pass".
// The internal RuleEngine is kept in sync with the store contents.
func LoadRulesStore(path string) (*RulesStore, error) {
	s := &RulesStore{
		path:          path,
		rules:         []Rule{},
		defaultAction: ActionPass,
		engine:        NewRuleEngine(),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read rules file: %w", err)
	}

	var f rulesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse rules file: %w", err)
	}

	if f.Rules != nil {
		s.rules = f.Rules
	}
	if f.DefaultAction != "" {
		s.defaultAction = f.DefaultAction
	}

	s.syncEngine()
	return s, nil
}

// List returns a copy of all rules.
func (s *RulesStore) List() []Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Rule, len(s.rules))
	copy(result, s.rules)
	return result
}

// Create adds a new rule to the store. An 8-character hex ID is assigned.
// The rule is appended to the end of the list. Returns the created rule
// (with ID assigned).
func (s *RulesStore) Create(rule Rule) (Rule, error) {
	id, err := generateID()
	if err != nil {
		return Rule{}, fmt.Errorf("generate ID: %w", err)
	}
	rule.ID = id

	s.mu.Lock()
	defer s.mu.Unlock()

	s.rules = append(s.rules, rule)
	s.syncEngine()

	if err := s.save(); err != nil {
		// Roll back on save failure.
		s.rules = s.rules[:len(s.rules)-1]
		s.syncEngine()
		return Rule{}, fmt.Errorf("save rules: %w", err)
	}

	return rule, nil
}

// Update replaces a rule by ID. The provided rule's ID field is overwritten
// with the id parameter to prevent ID changes.
func (s *RulesStore) Update(id string, rule Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.rules {
		if s.rules[i].ID == id {
			old := s.rules[i]
			rule.ID = id
			s.rules[i] = rule
			s.syncEngine()

			if err := s.save(); err != nil {
				// Roll back on save failure.
				s.rules[i] = old
				s.syncEngine()
				return fmt.Errorf("save rules: %w", err)
			}
			return nil
		}
	}

	return ErrRuleNotFound
}

// Delete removes a rule by ID.
func (s *RulesStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, r := range s.rules {
		if r.ID == id {
			old := make([]Rule, len(s.rules))
			copy(old, s.rules)
			s.rules = append(s.rules[:i], s.rules[i+1:]...)
			s.syncEngine()

			if err := s.save(); err != nil {
				// Roll back on save failure.
				s.rules = old
				s.syncEngine()
				return fmt.Errorf("save rules: %w", err)
			}
			return nil
		}
	}

	return ErrRuleNotFound
}

// Reorder reorders rules according to the provided ID list.
// All current rule IDs must be present exactly once.
func (s *RulesStore) Reorder(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(ids) != len(s.rules) {
		return fmt.Errorf("scte35: reorder requires exactly %d IDs, got %d", len(s.rules), len(ids))
	}

	// Build lookup by ID.
	byID := make(map[string]Rule, len(s.rules))
	for _, r := range s.rules {
		byID[r.ID] = r
	}

	seen := make(map[string]bool, len(ids))
	reordered := make([]Rule, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			return fmt.Errorf("scte35: duplicate ID %q in reorder", id)
		}
		seen[id] = true
		r, ok := byID[id]
		if !ok {
			return fmt.Errorf("scte35: rule %q not found during reorder", id)
		}
		reordered = append(reordered, r)
	}

	old := s.rules
	s.rules = reordered
	s.syncEngine()

	if err := s.save(); err != nil {
		s.rules = old
		s.syncEngine()
		return fmt.Errorf("save rules: %w", err)
	}
	return nil
}

// DefaultAction returns the current default action.
func (s *RulesStore) DefaultAction() RuleAction {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaultAction
}

// SetDefaultAction sets the default action and persists it.
func (s *RulesStore) SetDefaultAction(action RuleAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldAction := s.defaultAction
	s.defaultAction = action
	s.syncEngine()

	if err := s.save(); err != nil {
		s.defaultAction = oldAction
		s.syncEngine()
		return fmt.Errorf("save rules: %w", err)
	}
	return nil
}

// Engine returns the internal RuleEngine, which is kept in sync with
// the store's rules and default action.
func (s *RulesStore) Engine() *RuleEngine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.engine
}

// Templates returns preset rule templates. Templates are returned as
// disabled rules with empty IDs (not persisted until CreateFromTemplate).
func (s *RulesStore) Templates() []Rule {
	return presetTemplates()
}

// CreateFromTemplate creates a rule from a named template, assigns it an ID,
// and persists it. The created rule starts disabled.
func (s *RulesStore) CreateFromTemplate(templateName string) (Rule, error) {
	templates := presetTemplates()
	for _, tmpl := range templates {
		if tmpl.Name == templateName {
			return s.Create(tmpl)
		}
	}
	return Rule{}, ErrTemplateNotFound
}

// save writes the current rules to disk atomically (temp file + fsync + rename).
// Must be called with s.mu held.
func (s *RulesStore) save() error {
	f := rulesFile{
		Rules:         s.rules,
		DefaultAction: s.defaultAction,
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal rules: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, "rules-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("fsync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

// syncEngine rebuilds the internal RuleEngine from the current rules and
// default action. Must be called with s.mu held.
func (s *RulesStore) syncEngine() {
	engine := NewRuleEngine()
	engine.SetDefaultAction(s.defaultAction)
	// Only add enabled rules to the engine.
	for _, r := range s.rules {
		if r.Enabled {
			engine.AddRule(r)
		}
	}
	s.engine = engine
}

// generateID returns an 8-character hex string from 4 random bytes.
func generateID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// presetTemplates returns the hardcoded preset rule templates.
func presetTemplates() []Rule {
	dur30s := 30 * time.Second
	return []Rule{
		{
			Name:    "Strip short avails",
			Enabled: false,
			Conditions: []RuleCondition{
				{Field: "command_type", Operator: "=", Value: "5"},
				{Field: "duration", Operator: "<", Value: "15000"},
			},
			Logic:  LogicAND,
			Action: ActionDelete,
		},
		{
			Name:    "Strip program boundaries",
			Enabled: false,
			Conditions: []RuleCondition{
				{Field: "segmentation_type_id", Operator: "range", Value: "16-17"},
			},
			Logic:  LogicAND,
			Action: ActionDelete,
		},
		{
			Name:    "Fix missing duration",
			Enabled: false,
			Conditions: []RuleCondition{
				{Field: "duration", Operator: "=", Value: "0"},
			},
			Logic:  LogicAND,
			Action: ActionReplace,
			ReplaceWith: &ReplaceParams{
				Duration: &dur30s,
			},
		},
		{
			Name:    "Pass placement opportunities",
			Enabled: false,
			Conditions: []RuleCondition{
				{Field: "segmentation_type_id", Operator: "range", Value: "52-55"},
			},
			Logic:  LogicAND,
			Action: ActionPass,
		},
		{
			Name:    "Block non-SSAI signals",
			Enabled: false,
			Conditions: []RuleCondition{
				{Field: "command_type", Operator: "!=", Value: "0"},
			},
			Logic:  LogicAND,
			Action: ActionDelete,
		},
	}
}
