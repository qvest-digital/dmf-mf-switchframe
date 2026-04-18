//go:build cgo && whisper && !darwin

package main

/*
#cgo LDFLAGS: -L/opt/whisper.cpp/build/src -lwhisper -L/opt/whisper.cpp/build/ggml/src -lggml -lggml-base -lggml-cpu -L/opt/whisper.cpp/build/ggml/src/ggml-cuda -lggml-cuda -L/usr/local/cuda/lib64 -lcudart -lcublas -lstdc++ -lm -lgomp
*/
import "C"
