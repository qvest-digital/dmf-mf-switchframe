//go:build cgo && cuda && tensorrt

package arinfer

/*
#cgo CFLAGS: -I/usr/local/cuda/include
#cgo LDFLAGS: -L/usr/local/cuda/lib64 -lcudart
*/
import "C"
