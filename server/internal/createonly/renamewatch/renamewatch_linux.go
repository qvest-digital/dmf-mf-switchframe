// Package renamewatch lets a test fail when a file is renamed into, within
// or out of a directory: on an object-store mount a rename loses or empties
// the file, so code writing there must never issue one.
package renamewatch

import (
	"bytes"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"
)

// Watch starts recording renames in the given directories, which must
// exist. The returned function lists the paths renamed since. The kernel
// queues an event when the rename happens, so a rename that returned
// before the call is in the list.
func Watch(t testing.TB, dirs ...string) func() []string {
	t.Helper()
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		t.Fatalf("inotify: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Close(fd) })
	watched := map[int32]string{}
	for _, d := range dirs {
		wd, err := syscall.InotifyAddWatch(fd, d, syscall.IN_MOVED_TO|syscall.IN_MOVED_FROM)
		if err != nil {
			t.Fatalf("inotify watch %s: %v", d, err)
		}
		watched[int32(wd)] = d
	}
	return func() []string {
		var renamed []string
		buf := make([]byte, 64*1024)
		for {
			n, err := syscall.Read(fd, buf)
			if err != nil || n <= 0 {
				return renamed
			}
			for off := 0; off+syscall.SizeofInotifyEvent <= n; {
				ev := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[off]))
				start := off + syscall.SizeofInotifyEvent
				name := string(bytes.TrimRight(buf[start:start+int(ev.Len)], "\x00"))
				renamed = append(renamed, filepath.Join(watched[ev.Wd], name))
				off = start + int(ev.Len)
			}
		}
	}
}
