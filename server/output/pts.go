package output

// ptsPeriod is the period of the 33-bit MPEG PTS.
const ptsPeriod = int64(1) << 33

// unwrapPTS returns the value congruent to pts modulo 2^33 nearest ref.
// Video PTS wraps at 33 bits, after about 26.5 hours, while audio PTS keeps
// counting past it; FLV takes one timeline that never runs backwards, so
// both are read as the nearest continuation of the last frame.
func unwrapPTS(pts, ref int64) int64 {
	d := (pts - ref) % ptsPeriod
	if d < 0 {
		d += ptsPeriod
	}
	if d >= ptsPeriod/2 {
		d -= ptsPeriod
	}
	return ref + d
}
