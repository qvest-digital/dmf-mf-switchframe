package clip

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const peerID = "0123456789abcdef0123456789abcdef"

// newEngineStore is newLibraryStore for one engine of several sharing lib,
// holding peerID locally with the given content.
func newEngineStore(t *testing.T, lib, owner, content string) *Store {
	t.Helper()
	s, _ := newLibraryStore(t, lib)
	s.libOwner = owner
	s.retryWait = func() time.Duration { return time.Millisecond }
	writeFile(t, filepath.Join(s.Dir(), "upload.ts"), content)
	if err := s.AddWithID(peerID, &Clip{Name: "Upload", Filename: "upload.ts", ByteSize: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	return s
}

func readLib(t *testing.T, lib, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lib, name))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The UI uploads a clip to both engines under one ID, so both copy it into
// the library at once. While engine a's copy runs, engine b must touch
// neither a's media nor a's marker: deleting either loses the only
// complete copy or lets a cut-short one be listed.
func TestPublishLeavesALivePeerCopyAlone(t *testing.T) {
	lib := t.TempDir()
	writeFile(t, filepath.Join(lib, peerID+".ts"), "clip-from-a, still copy")
	writeFile(t, filepath.Join(lib, peerID+".ts.pending.a"), peerID)
	b := newEngineStore(t, lib, "b", "clip-from-b")

	if err := b.Publish(peerID); !errors.Is(err, ErrPublishPending) {
		t.Fatalf("publish = %v, want ErrPublishPending", err)
	}
	if got := readLib(t, lib, peerID+".ts"); got != "clip-from-a, still copy" {
		t.Fatalf("engine a's media changed: %q", got)
	}
	if !exists(filepath.Join(lib, peerID+".ts.pending.a")) {
		t.Fatal("engine a's marker removed")
	}
	if exists(filepath.Join(lib, peerID+".ts.pending.b")) {
		t.Fatal("engine b left a marker of its own")
	}
	if got, _ := b.Get(peerID); got.LibraryPath != "" || !exists(filepath.Join(b.Dir(), "upload.ts")) {
		t.Fatalf("engine b's clip left its local copy: %+v", got)
	}
}

// Once engine a's copy is done, engine b's retry takes a's file.
func TestPublishSoonTakesThePeerCopyOnceItEnds(t *testing.T) {
	lib := t.TempDir()
	writeFile(t, filepath.Join(lib, peerID+".ts"), "clip-from")
	writeFile(t, filepath.Join(lib, peerID+".ts.pending.a"), peerID)
	b := newEngineStore(t, lib, "b", "clip-from-b")

	if err := b.PublishSoon(peerID); !errors.Is(err, ErrPublishPending) {
		t.Fatalf("publish = %v, want ErrPublishPending", err)
	}
	// Engine a finishes: complete media, sidecar, marker gone.
	if err := os.Remove(filepath.Join(lib, peerID+".ts")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(lib, peerID+".ts"), "clip-from-a")
	if err := os.Remove(filepath.Join(lib, peerID+".ts.pending.a")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := b.Get(peerID); got.LibraryPath != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got, _ := b.Get(peerID); got.LibraryPath != peerID+".ts" {
		t.Fatalf("engine b never published: %+v", got)
	}
	if got := readLib(t, lib, peerID+".ts"); got != "clip-from-a" {
		t.Fatalf("library holds %q, want engine a's copy", got)
	}
}

// A marker older than peerMarkerTimeout was left by an engine that died
// mid-copy; its media may be cut short and is replaced.
func TestPublishReplacesBesideADeadPeerMarker(t *testing.T) {
	lib := t.TempDir()
	writeFile(t, filepath.Join(lib, peerID+".ts"), "clip-cut")
	marker := filepath.Join(lib, peerID+".ts.pending.a")
	writeFile(t, marker, peerID)
	old := time.Now().Add(-2 * peerMarkerTimeout)
	if err := os.Chtimes(marker, old, old); err != nil {
		t.Fatal(err)
	}
	b := newEngineStore(t, lib, "b", "clip-from-b")
	if err := b.Publish(peerID); err != nil {
		t.Fatal(err)
	}
	if got := readLib(t, lib, peerID+".ts"); got != "clip-from-b" {
		t.Fatalf("library holds %q", got)
	}
	if want := []string{peerID + ".ts", peerID + ".ts.json"}; !equal(libraryFiles(t, lib), want) {
		t.Fatalf("library holds %v, want %v", libraryFiles(t, lib), want)
	}
}

// Media beside any engine's marker is not listed while it may be cut short.
func TestScanSkipsMediaBesideAnyEnginesMarker(t *testing.T) {
	lib := t.TempDir()
	writeFile(t, filepath.Join(lib, peerID+".ts"), "clip-cut")
	writeFile(t, filepath.Join(lib, peerID+".ts.pending.b"), peerID)
	a, probes := newLibraryStore(t, lib)
	a.libOwner = "a"
	res, err := a.ScanLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 || len(res.Skipped) != 1 || *probes != 0 {
		t.Fatalf("scan = %+v after %d probes, want the clip skipped unprobed", res, *probes)
	}
}

// An engine that died after the sidecar was written, but before its marker
// was deleted, left a complete clip. It is listed, and the marker deleted by
// its own engine, or by any once it is dead, so it does not stay hidden;
// another engine's live marker stays.
func TestScanListsAFinishedClipBehindALeftoverMarker(t *testing.T) {
	for _, tc := range []struct {
		marker    string
		age       time.Duration
		keepsMark bool
	}{
		{marker: ".pending.a", keepsMark: false},
		{marker: ".pending.b", keepsMark: true},
		{marker: ".pending.b", age: 2 * peerMarkerTimeout, keepsMark: false},
	} {
		lib := t.TempDir()
		media := filepath.Join(lib, peerID+".ts")
		writeFile(t, media, "clip-whole")
		if err := writeLibrarySidecar(media, &Clip{Name: "Whole", ByteSize: int64(len("clip-whole"))}); err != nil {
			t.Fatal(err)
		}
		marker := media + tc.marker
		writeFile(t, marker, peerID)
		if tc.age > 0 {
			old := time.Now().Add(-tc.age)
			_ = os.Chtimes(marker, old, old)
		}
		a, probes := newLibraryStore(t, lib)
		a.libOwner = "a"
		res, err := a.ScanLibrary()
		if err != nil {
			t.Fatal(err)
		}
		if res.Added != 1 || *probes != 0 {
			t.Fatalf("%s age %v: scan = %+v after %d probes, want the clip listed from its sidecar", tc.marker, tc.age, res, *probes)
		}
		if exists(marker) != tc.keepsMark {
			t.Fatalf("%s age %v: marker kept = %v, want %v", tc.marker, tc.age, exists(marker), tc.keepsMark)
		}
	}
}

// Two Publish calls for one clip in one store, as an upload and the restart
// retry, run one after the other: concurrently, the second would take the
// first's marker as its own leftover and delete the media being copied.
func TestPublishOfOneClipRunsOneAtATime(t *testing.T) {
	lib := t.TempDir()
	s := newEngineStore(t, lib, "a", "clip-upload")
	var running, most atomic.Int32
	orig := copyIntoLibrary
	copyIntoLibrary = func(dst, src string) error {
		n := running.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		running.Add(-1)
		return orig(dst, src)
	}
	t.Cleanup(func() { copyIntoLibrary = orig })

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.Publish(peerID)
		}(i)
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Fatalf("%d copies of one clip ran at once", most.Load())
	}
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{peerID + ".ts", peerID + ".ts.json"}; !equal(libraryFiles(t, lib), want) {
		t.Fatalf("library holds %v, want %v", libraryFiles(t, lib), want)
	}
}
