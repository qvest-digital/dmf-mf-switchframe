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
