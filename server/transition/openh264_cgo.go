//go:build cgo

package transition

// openh264_cgo.go provides the cgo link directives for the OpenH264 library.
// This is separated into its own file so the linker flags are specified once,
// avoiding duplicate library warnings.

/*
#cgo pkg-config: openh264
*/
import "C"
