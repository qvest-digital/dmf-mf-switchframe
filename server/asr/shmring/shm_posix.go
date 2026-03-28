//go:build cgo && !windows

package shmring

/*
#include <fcntl.h>
#include <stdlib.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

// shm_open is in libc on macOS, but needs -lrt on Linux.
#cgo linux LDFLAGS: -lrt

static int shm_open_create(const char *name) {
	return shm_open(name, O_RDWR | O_CREAT | O_EXCL, 0600);
}

static int shm_open_existing(const char *name) {
	return shm_open(name, O_RDWR, 0);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// shmOpen creates or opens a POSIX shared memory object.
// name must start with '/'. Returns the file descriptor.
func shmOpen(name string, create bool) (int, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var fd C.int
	var err error
	if create {
		fd, err = C.shm_open_create(cname)
	} else {
		fd, err = C.shm_open_existing(cname)
	}
	if fd < 0 {
		return 0, fmt.Errorf("shm_open(%s): %v", name, err)
	}
	return int(fd), nil
}

// shmFtruncate sets the size of the shared memory object.
func shmFtruncate(fd int, size int) error {
	ret, err := C.ftruncate(C.int(fd), C.off_t(size))
	if ret != 0 {
		return fmt.Errorf("ftruncate: %v", err)
	}
	return nil
}

// shmMmap maps the shared memory object into the process address space.
func shmMmap(fd int, size int) (unsafe.Pointer, error) {
	ptr, err := C.mmap(nil, C.size_t(size), C.PROT_READ|C.PROT_WRITE, C.MAP_SHARED, C.int(fd), 0)
	if ptr == C.MAP_FAILED {
		return nil, fmt.Errorf("mmap: %v", err)
	}
	return ptr, nil
}

// shmMunmap unmaps a shared memory region.
func shmMunmap(ptr unsafe.Pointer, size int) error {
	ret, err := C.munmap(ptr, C.size_t(size))
	if ret != 0 {
		return fmt.Errorf("munmap: %v", err)
	}
	return nil
}

// shmUnlink removes a POSIX shared memory object.
func shmUnlink(name string) error {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	ret, err := C.shm_unlink(cname)
	if ret != 0 {
		return fmt.Errorf("shm_unlink(%s): %v", name, err)
	}
	return nil
}

// shmFstat returns the size of an open shared memory file descriptor.
func shmFstat(fd int) (int, error) {
	var st C.struct_stat
	ret, err := C.fstat(C.int(fd), &st)
	if ret != 0 {
		return 0, fmt.Errorf("fstat: %v", err)
	}
	return int(st.st_size), nil
}

// shmClose closes the file descriptor.
func shmClose(fd int) error {
	ret, err := C.close(C.int(fd))
	if ret != 0 {
		return fmt.Errorf("close: %v", err)
	}
	return nil
}
