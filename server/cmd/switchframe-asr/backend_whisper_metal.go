//go:build cgo && whisper && darwin

package main

/*
#cgo LDFLAGS: -L/opt/whisper.cpp/build/src -lwhisper -L/opt/whisper.cpp/build/ggml/src -lggml -lggml-base -lggml-cpu -L/opt/whisper.cpp/build/ggml/src/ggml-metal -lggml-metal -L/opt/whisper.cpp/build/ggml/src/ggml-blas -lggml-blas -lstdc++ -lm -framework Foundation -framework Metal -framework MetalKit -framework Accelerate
*/
import "C"
