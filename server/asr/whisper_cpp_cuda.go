//go:build cgo && whisper && !darwin

package asr

/*
#cgo LDFLAGS: -L/opt/whisper.cpp/build/src -L/opt/whisper.cpp/build/ggml/src -L/opt/whisper.cpp/build/ggml/src/ggml-cuda -lwhisper -lggml -lggml-cuda -lggml-cpu -lggml-base -lstdc++ -lm -lgomp -lcublas -lcublasLt -lcudart -L/usr/local/cuda/lib64
*/
import "C"
