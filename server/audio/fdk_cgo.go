package audio

// fdk_cgo.go provides the cgo link directives for the FDK AAC library.
// This is separated into its own file so the linker flags are specified once,
// avoiding duplicate library warnings.

/*
#cgo pkg-config: fdk-aac
*/
import "C"
