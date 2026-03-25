package nmos

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTAIVersion(t *testing.T) {
	v := NewVersion()
	s := string(v)

	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("expected format <seconds>:<nanoseconds>, got %q", s)
	}

	sec, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("seconds not a valid integer: %v", err)
	}
	if sec <= 0 {
		t.Fatalf("seconds should be positive, got %d", sec)
	}

	ns, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		t.Fatalf("nanoseconds not a valid integer: %v", err)
	}
	if ns < 0 || ns >= 1e9 {
		t.Fatalf("nanoseconds out of range [0, 1e9): %d", ns)
	}

	// TAI is UTC + leap seconds (currently 37). The seconds value should be
	// roughly time.Now().Unix() + 37.
	nowUTC := time.Now().Unix()
	diff := sec - nowUTC
	if diff < 30 || diff > 45 {
		t.Fatalf("TAI offset from UTC should be ~37s, got diff=%d (sec=%d, utc=%d)", diff, sec, nowUTC)
	}
}

func TestVersionOrdering(t *testing.T) {
	v1 := NewVersion()
	// Ensure v2 is created strictly after v1.
	time.Sleep(1 * time.Millisecond)
	v2 := NewVersion()

	if v2 <= v1 {
		t.Fatalf("expected v2 > v1, got v1=%q v2=%q", v1, v2)
	}
}

func TestVersionJSON(t *testing.T) {
	v := NewVersion()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Should be a JSON string (quoted).
	if data[0] != '"' {
		t.Fatalf("expected JSON string, got %s", data)
	}

	var v2 Version
	if err := json.Unmarshal(data, &v2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v2 != v {
		t.Fatalf("round-trip mismatch: %q != %q", v2, v)
	}
}
