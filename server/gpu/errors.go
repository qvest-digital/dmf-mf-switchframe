package gpu

import "errors"

// ErrGPUNotAvailable indicates the GPU backend is not available on this system.
var ErrGPUNotAvailable = errors.New("gpu: not available")
