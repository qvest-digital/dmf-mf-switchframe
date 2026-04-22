package video

// IsNominalPTSDelta reports whether a 90 kHz PTS delta matches a
// standard video frame duration. Integer rates (24/25/30/50/60 fps) are
// matched exactly. NTSC rational rates (23.976, 29.97, 59.94 fps) are
// matched with a +1 tick tolerance to accommodate Bresenham-distributed
// remainder from the 90000 × den / num division.
//
// Intended for diagnostic inter-frame delta checks in the engine
// pipeline: a true result means "this is what we expect at one of the
// broadcast frame rates we support"; a false result means the caller
// should log or count an anomaly (dropped frame, two-frame gap,
// non-monotonic PTS, etc.).
func IsNominalPTSDelta(delta int64) bool {
	switch delta {
	case 3000: // 30fps
		return true
	case 3003, 3004: // 29.97fps (30000/1001)
		return true
	case 3600: // 25fps
		return true
	case 3750: // 24fps
		return true
	case 3753, 3754: // 23.976fps (24000/1001)
		return true
	case 1800: // 50fps
		return true
	case 1500: // 60fps
		return true
	case 1501, 1502: // 59.94fps (60000/1001)
		return true
	default:
		return false
	}
}
