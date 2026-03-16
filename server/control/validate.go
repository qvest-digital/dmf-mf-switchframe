package control

import "fmt"

// String length limits for user-provided names and labels.
const (
	MaxNameLen  = 128
	MaxLabelLen = 128
)

// Audio parameter range limits.
const (
	MinTrimDB   = -20.0
	MaxTrimDB   = 20.0
	MinLevelDB  = -80.0
	MaxLevelDB  = 10.0
	MinDelayMs  = 0
	MaxDelayMs  = 500
	MinEQFreq   = 20.0
	MaxEQFreq   = 20000.0
	MinEQGainDB = -20.0
	MaxEQGainDB = 20.0
	MinEQQ      = 0.1
	MaxEQQ      = 20.0
)

// validateStringLen checks that value does not exceed max bytes.
func validateStringLen(name, value string, max int) error {
	if len(value) > max {
		return fmt.Errorf("%s too long: %d bytes (max %d)", name, len(value), max)
	}
	return nil
}

// validateRange checks that value is within [min, max].
func validateRange(name string, value, min, max float64) error {
	if value < min || value > max {
		return fmt.Errorf("%s out of range: %.2f (valid: %.2f to %.2f)", name, value, min, max)
	}
	return nil
}

// validateIntRange checks that value is within [min, max].
func validateIntRange(name string, value, min, max int) error {
	if value < min || value > max {
		return fmt.Errorf("%s out of range: %d (valid: %d to %d)", name, value, min, max)
	}
	return nil
}
