package audio

import "math"

// BalanceGains computes L/R gains for a stereo balance control.
// balance: -1.0 (hard left) to +1.0 (hard right), 0.0 = center.
// At center both gains are 1.0 (transparent, zero CPU).
// Uses cos curve on the attenuated side for smooth rolloff.
func BalanceGains(balance float64) (gainL, gainR float32) {
	if balance == 0 {
		return 1.0, 1.0
	}
	if balance < 0 {
		return 1.0, float32(math.Cos(-balance * math.Pi / 2.0))
	}
	return float32(math.Cos(balance * math.Pi / 2.0)), 1.0
}
