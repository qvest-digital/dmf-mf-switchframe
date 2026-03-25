package nmos

import (
	"testing"
)

func TestParseTXTRecords(t *testing.T) {
	records := []string{
		"api_ver=v1.0,v1.1,v1.2,v1.3",
		"api_proto=https",
		"api_auth=true",
		"pri=50",
	}

	info := ParseTXTRecords(records)

	if info.Priority != 50 {
		t.Errorf("Priority = %d, want 50", info.Priority)
	}
	if info.Protocol != "https" {
		t.Errorf("Protocol = %q, want %q", info.Protocol, "https")
	}
	if !info.Auth {
		t.Error("Auth = false, want true")
	}
	wantVersions := []string{"v1.0", "v1.1", "v1.2", "v1.3"}
	if len(info.Versions) != len(wantVersions) {
		t.Fatalf("Versions length = %d, want %d", len(info.Versions), len(wantVersions))
	}
	for i, v := range info.Versions {
		if v != wantVersions[i] {
			t.Errorf("Versions[%d] = %q, want %q", i, v, wantVersions[i])
		}
	}
}

func TestParseTXTRecordsDefaults(t *testing.T) {
	// Empty records — pri defaults to 100, api_proto defaults to "http"
	info := ParseTXTRecords(nil)

	if info.Priority != 100 {
		t.Errorf("Priority = %d, want 100 (default)", info.Priority)
	}
	if info.Protocol != "http" {
		t.Errorf("Protocol = %q, want %q (default)", info.Protocol, "http")
	}
	if info.Auth {
		t.Error("Auth = true, want false (default)")
	}
	if len(info.Versions) != 0 {
		t.Errorf("Versions length = %d, want 0", len(info.Versions))
	}
}

func TestParseTXTRecordsPartial(t *testing.T) {
	// Only api_ver provided — pri and api_proto should default
	records := []string{
		"api_ver=v1.3",
	}
	info := ParseTXTRecords(records)

	if info.Priority != 100 {
		t.Errorf("Priority = %d, want 100 (default)", info.Priority)
	}
	if info.Protocol != "http" {
		t.Errorf("Protocol = %q, want %q (default)", info.Protocol, "http")
	}
	if len(info.Versions) != 1 || info.Versions[0] != "v1.3" {
		t.Errorf("Versions = %v, want [v1.3]", info.Versions)
	}
}

func TestParseTXTRecordsInvalidPriority(t *testing.T) {
	records := []string{
		"pri=notanumber",
	}
	info := ParseTXTRecords(records)

	// Invalid priority should fall back to default 100
	if info.Priority != 100 {
		t.Errorf("Priority = %d, want 100 (default for invalid)", info.Priority)
	}
}

func TestSortByPriority(t *testing.T) {
	entries := []RegistryEntry{
		{Host: "reg-c.local", Port: 80, Priority: 200},
		{Host: "reg-a.local", Port: 80, Priority: 10},
		{Host: "reg-b.local", Port: 80, Priority: 100},
	}

	SortByPriority(entries)

	if entries[0].Host != "reg-a.local" {
		t.Errorf("entries[0].Host = %q, want %q", entries[0].Host, "reg-a.local")
	}
	if entries[1].Host != "reg-b.local" {
		t.Errorf("entries[1].Host = %q, want %q", entries[1].Host, "reg-b.local")
	}
	if entries[2].Host != "reg-c.local" {
		t.Errorf("entries[2].Host = %q, want %q", entries[2].Host, "reg-c.local")
	}
}

func TestSortByPriorityEmpty(t *testing.T) {
	// Should not panic on empty slice
	var entries []RegistryEntry
	SortByPriority(entries)
}

func TestFilterByVersion(t *testing.T) {
	entries := []RegistryEntry{
		{Host: "reg-a.local", Port: 80, Versions: []string{"v1.0", "v1.1", "v1.2", "v1.3"}},
		{Host: "reg-b.local", Port: 80, Versions: []string{"v1.0", "v1.1"}},
		{Host: "reg-c.local", Port: 80, Versions: []string{"v1.2", "v1.3"}},
	}

	filtered := FilterByVersion(entries, "v1.3")

	if len(filtered) != 2 {
		t.Fatalf("filtered length = %d, want 2", len(filtered))
	}
	if filtered[0].Host != "reg-a.local" {
		t.Errorf("filtered[0].Host = %q, want %q", filtered[0].Host, "reg-a.local")
	}
	if filtered[1].Host != "reg-c.local" {
		t.Errorf("filtered[1].Host = %q, want %q", filtered[1].Host, "reg-c.local")
	}
}

func TestFilterByVersionNoMatch(t *testing.T) {
	entries := []RegistryEntry{
		{Host: "reg-a.local", Port: 80, Versions: []string{"v1.0", "v1.1"}},
		{Host: "reg-b.local", Port: 80, Versions: []string{"v1.0"}},
	}

	filtered := FilterByVersion(entries, "v1.3")

	if len(filtered) != 0 {
		t.Errorf("filtered length = %d, want 0", len(filtered))
	}
}

func TestFilterByVersionPreservesOrder(t *testing.T) {
	entries := []RegistryEntry{
		{Host: "z.local", Port: 80, Priority: 300, Versions: []string{"v1.3"}},
		{Host: "a.local", Port: 80, Priority: 100, Versions: []string{"v1.3"}},
		{Host: "m.local", Port: 80, Priority: 200, Versions: []string{"v1.3"}},
	}

	// Pre-sort by priority, then filter — order should be preserved
	SortByPriority(entries)
	filtered := FilterByVersion(entries, "v1.3")

	if len(filtered) != 3 {
		t.Fatalf("filtered length = %d, want 3", len(filtered))
	}
	if filtered[0].Host != "a.local" {
		t.Errorf("filtered[0].Host = %q, want %q", filtered[0].Host, "a.local")
	}
	if filtered[1].Host != "m.local" {
		t.Errorf("filtered[1].Host = %q, want %q", filtered[1].Host, "m.local")
	}
	if filtered[2].Host != "z.local" {
		t.Errorf("filtered[2].Host = %q, want %q", filtered[2].Host, "z.local")
	}
}
