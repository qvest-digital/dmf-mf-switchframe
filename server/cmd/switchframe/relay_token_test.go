package main

import "testing"

// Without authentication (--demo) an engine takes anonymous commands; it
// passes them on without credentials, not as the API token's, which the
// peer would take as an admin's.
func TestRelayToken(t *testing.T) {
	if got := relayToken(AppConfig{APIToken: "secret"}); got != "secret" {
		t.Fatalf("relayToken with auth = %q, want the API token", got)
	}
	if got := relayToken(AppConfig{APIToken: "secret", Demo: true}); got != "" {
		t.Fatalf("relayToken under --demo = %q, want none", got)
	}
}
