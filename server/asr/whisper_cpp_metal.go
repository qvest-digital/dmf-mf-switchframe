//go:build cgo && whisper && darwin

package asr

/*
#cgo LDFLAGS: -L/opt/whisper.cpp/build/src -L/opt/whisper.cpp/build/ggml/src -L/opt/whisper.cpp/build/ggml/src/ggml-metal -lwhisper -lggml -lggml-metal -lggml-cpu -lggml-base -lstdc++ -lm -framework Metal -framework Foundation -framework Accelerate
*/
import "C"
