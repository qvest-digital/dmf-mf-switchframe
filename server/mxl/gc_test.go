//go:build cgo && mxl

package mxl

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// unheldFlow puts a flow directory in a fresh domain whose data file no
// writer holds, which is all the SDK's collection looks at.
func unheldFlow(t *testing.T) (domain, dir string) {
	t.Helper()
	domain, err := os.MkdirTemp("/dev/shm", "switchframe-mxl-gc-*")
	if err != nil {
		t.Skipf("no /dev/shm: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(domain) })
	dir = filepath.Join(domain, "11111111-2222-4333-8444-555555555555.mxl-flow")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return domain, dir
}

func TestNewInstanceGC_ZeroNeverCollects(t *testing.T) {
	domain, dir := unheldFlow(t)
	inst, err := NewInstanceGC(domain, 0)
	if err != nil {
		t.Fatalf("NewInstanceGC: %v", err)
	}
	defer inst.Close()

	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("a flow no writer holds was collected with collection off: %v", err)
	}
}

func TestNewInstanceGC_CollectsAtTheInterval(t *testing.T) {
	domain, dir := unheldFlow(t)
	inst, err := NewInstanceGC(domain, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewInstanceGC: %v", err)
	}
	defer inst.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a flow no writer holds was not collected")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
