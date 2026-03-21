//go:build darwin

package gpu

import _ "embed"

// embeddedMetallib contains the compiled Metal shader library, embedded
// at build time. This is the fallback when the .metallib file cannot be
// found on disk (e.g., when running from a Go binary without the source
// tree present). ~126KB compressed.
//
//go:embed metal/switchframe_gpu.metallib
var embeddedMetallib []byte
