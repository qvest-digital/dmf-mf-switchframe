//go:build !cgo || windows

package shmring

import (
	"errors"
	"unsafe"
)

var errShmNotAvailable = errors.New("shared memory not available (requires cgo)")

func shmOpen(name string, create bool) (int, error) { return 0, errShmNotAvailable }
func shmFtruncate(fd int, size int) error           { return errShmNotAvailable }
func shmMmap(fd int, size int) (unsafe.Pointer, error) {
	return nil, errShmNotAvailable
}
func shmMunmap(ptr unsafe.Pointer, size int) error { return errShmNotAvailable }
func shmUnlink(name string) error                  { return errShmNotAvailable }
func shmFstat(fd int) (int, error)                 { return 0, errShmNotAvailable }
func shmClose(fd int) error                        { return errShmNotAvailable }
