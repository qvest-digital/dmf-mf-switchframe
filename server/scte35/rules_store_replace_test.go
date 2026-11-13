package scte35

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The peer engine's rules and default action replace this engine's at
// once, persist, and are what the rule engine evaluates.
func TestRulesStoreReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	s, err := LoadRulesStore(path)
	require.NoError(t, err)
	_, err = s.Create(Rule{Name: "old", Enabled: true, Action: ActionPass})
	require.NoError(t, err)

	rules := []Rule{{ID: "r1", Name: "drop", Enabled: true, Action: ActionDelete}, {ID: "r2", Name: "off", Action: ActionPass}}
	require.NoError(t, s.Replace(rules, ActionDelete))
	require.Equal(t, rules, s.List())
	require.Equal(t, ActionDelete, s.DefaultAction())
	action, _ := s.Engine().Evaluate(&CueMessage{}, "")
	require.Equal(t, ActionDelete, action, "the rule engine does not evaluate the rules taken")

	reopened, err := LoadRulesStore(path)
	require.NoError(t, err)
	require.Equal(t, []string{"drop", "off"}, []string{reopened.List()[0].Name, reopened.List()[1].Name})
	require.Equal(t, ActionDelete, reopened.DefaultAction())
}
