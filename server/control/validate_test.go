package control

import (
	"strings"
	"testing"
)

func TestValidateStringLen(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		value   string
		max     int
		wantErr bool
	}{
		{"empty string", "name", "", 128, false},
		{"short string", "name", "short", 128, false},
		{"exact max length", "name", strings.Repeat("a", 128), 128, false},
		{"one over max", "name", strings.Repeat("a", 129), 128, true},
		{"way over max", "name", strings.Repeat("a", 1000), 128, true},
		{"unicode within byte limit", "label", strings.Repeat("\u00e9", 64), 128, false},
		{"unicode over byte limit", "label", strings.Repeat("\u00e9", 65), 128, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStringLen(tt.field, tt.value, tt.max)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateStringLen(%q, %q, %d) error = %v, wantErr %v",
					tt.field, tt.value, tt.max, err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tt.field) {
				t.Errorf("error message should contain field name %q: got %q", tt.field, err.Error())
			}
		})
	}
}

func TestValidateRange(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		value   float64
		min     float64
		max     float64
		wantErr bool
	}{
		{"in range", "trim", 5.0, -20.0, 20.0, false},
		{"at min", "trim", -20.0, -20.0, 20.0, false},
		{"at max", "trim", 20.0, -20.0, 20.0, false},
		{"below min", "trim", -20.1, -20.0, 20.0, true},
		{"above max", "trim", 20.1, -20.0, 20.0, true},
		{"zero in range", "level", 0.0, -80.0, 10.0, false},
		{"negative in range", "level", -40.0, -80.0, 10.0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRange(tt.field, tt.value, tt.min, tt.max)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRange(%q, %f, %f, %f) error = %v, wantErr %v",
					tt.field, tt.value, tt.min, tt.max, err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tt.field) {
				t.Errorf("error message should contain field name %q: got %q", tt.field, err.Error())
			}
		})
	}
}

func TestValidateIntRange(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		value   int
		min     int
		max     int
		wantErr bool
	}{
		{"in range", "delay", 100, 0, 500, false},
		{"at min", "delay", 0, 0, 500, false},
		{"at max", "delay", 500, 0, 500, false},
		{"below min", "delay", -1, 0, 500, true},
		{"above max", "delay", 501, 0, 500, true},
		{"large negative", "delay", -9999, 0, 500, true},
		{"large positive", "delay", 999999, 0, 500, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIntRange(tt.field, tt.value, tt.min, tt.max)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateIntRange(%q, %d, %d, %d) error = %v, wantErr %v",
					tt.field, tt.value, tt.min, tt.max, err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tt.field) {
				t.Errorf("error message should contain field name %q: got %q", tt.field, err.Error())
			}
		})
	}
}

func TestValidationConstants(t *testing.T) {
	// Ensure constants are sensible (not accidentally swapped, etc.)
	if MaxNameLen <= 0 {
		t.Error("MaxNameLen must be positive")
	}
	if MaxLabelLen <= 0 {
		t.Error("MaxLabelLen must be positive")
	}
	if MinTrimDB >= MaxTrimDB {
		t.Error("MinTrimDB must be less than MaxTrimDB")
	}
	if MinLevelDB >= MaxLevelDB {
		t.Error("MinLevelDB must be less than MaxLevelDB")
	}
	if MinDelayMs >= MaxDelayMs {
		t.Error("MinDelayMs must be less than MaxDelayMs")
	}
	if MinEQFreq >= MaxEQFreq {
		t.Error("MinEQFreq must be less than MaxEQFreq")
	}
	if MinEQGainDB >= MaxEQGainDB {
		t.Error("MinEQGainDB must be less than MaxEQGainDB")
	}
	if MinEQQ >= MaxEQQ {
		t.Error("MinEQQ must be less than MaxEQQ")
	}
}
