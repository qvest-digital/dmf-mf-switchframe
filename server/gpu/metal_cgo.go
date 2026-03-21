//go:build darwin

package gpu

/*
#cgo CFLAGS: -x objective-c -fno-objc-arc
#cgo LDFLAGS: -framework Metal -framework MetalPerformanceShaders -framework Foundation -framework CoreGraphics
#include "metal_bridge.h"
*/
import "C"
