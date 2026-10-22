// Package nmos provides NMOS IS-04 resource types and TAI version tracking
// for SMPTE ST 2110 discovery and connection management.
package nmos

import (
	"fmt"
	"time"
)

// TAILeapSeconds is the current offset between TAI and UTC.
// As of 2017-01-01, TAI = UTC + 37 seconds.
const TAILeapSeconds int64 = 37

// Version is a TAI timestamp in the format "<seconds>:<nanoseconds>".
// It is used as a version identifier for NMOS resources per IS-04.
// Versions are monotonically ordered via lexicographic string comparison
// because the seconds field uses consistent zero-padded width (from Unix
// epoch, seconds are always 10 digits for the foreseeable future) and
// nanoseconds are zero-padded to 9 digits.
type Version string

// NewVersion returns a new Version stamped with the current TAI time.
func NewVersion() Version {
	now := time.Now()
	taiSec := now.Unix() + TAILeapSeconds
	ns := int64(now.Nanosecond())
	return Version(fmt.Sprintf("%d:%09d", taiSec, ns))
}

// CompareVersions orders two "<seconds>:<nanoseconds>" timestamps, such as
// IS-05 activation times: negative when a is earlier, 0 when equal or
// either is malformed, positive when a is later.
func CompareVersions(a, b string) int {
	var as, an, bs, bn int64
	if _, err := fmt.Sscanf(a, "%d:%d", &as, &an); err != nil {
		return 0
	}
	if _, err := fmt.Sscanf(b, "%d:%d", &bs, &bn); err != nil {
		return 0
	}
	switch {
	case as != bs:
		return cmpInt(as, bs)
	default:
		return cmpInt(an, bn)
	}
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
