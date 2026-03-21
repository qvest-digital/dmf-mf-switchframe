package gpu

import "errors"

// ErrGPUNotAvailable indicates CUDA is not available on this system.
var ErrGPUNotAvailable = errors.New("gpu: CUDA not available")
