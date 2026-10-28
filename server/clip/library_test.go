package clip

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// newLibraryStore returns a store with a library attached and a probe that
// counts its calls instead of decoding.
func newLibraryStore(t *testing.T, lib string) (*Store, *int) {
	t.Helper()
	s, err := NewStore(t.TempDir(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachLibrary(lib); err != nil {
		t.Fatal(err)
	}
	probes := 0
	s.probe = func(path string) (*ProbeResult, error) {
		probes++
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if string(data[:4]) != "clip" {
			return nil, ErrInvalidFormat
		}
		return &ProbeResult{Codec: "h264", Width: 1920, Height: 1080, FPSNum: 50, FPSDen: 1, DurationMs: 10_000}, nil
	}
	return s, &probes
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func libraryFiles(t *testing.T, lib string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(lib, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(lib, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// Clips put into the library by something other than switchframe, such as
// files uploaded to the bucket for a production, are listed by the next
// store that attaches it, under the same ID on every store.
func TestScanLibraryListsPreloadedClips(t *testing.T) {
	lib := t.TempDir()
	writeFile(t, filepath.Join(lib, "Opener.mp4"), "clip-opener")
	writeFile(t, filepath.Join(lib, "show", "Break.ts"), "clip-break")
	writeFile(t, filepath.Join(lib, "notes.txt"), "not media")
	writeFile(t, filepath.Join(lib, "Broken.mp4"), "garbage")

	a, _ := newLibraryStore(t, lib)
	res, err := a.ScanLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 || len(res.Skipped) != 1 || res.Skipped[0] != "Broken.mp4" {
		t.Fatalf("scan = %+v, want 2 added and Broken.mp4 skipped", res)
	}
	names := map[string]*Clip{}
	for _, c := range a.List() {
		names[c.Name] = c
	}
	op, ok := names["Opener"]
	if !ok || names["Break"] == nil {
		t.Fatalf("listed %v, want Opener and Break", names)
	}
	if op.LibraryPath != "Opener.mp4" || op.Width != 1920 || op.ByteSize != int64(len("clip-opener")) {
		t.Fatalf("Opener = %+v", op)
	}
	if got := a.MediaPath(op); got != filepath.Join(lib, "Opener.mp4") {
		t.Fatalf("MediaPath = %q", got)
	}

	b, _ := newLibraryStore(t, lib)
	if _, err := b.ScanLibrary(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(op.ID); err != nil {
		t.Fatalf("second store does not know %s: %v", op.ID, err)
	}
}

// The first scan writes a sidecar per clip, so later scans do not probe
// the media again: a full read of every clip on each booking would be slow
// against an object store.
func TestScanLibraryProbesOnceThenReadsSidecar(t *testing.T) {
	lib := t.TempDir()
	writeFile(t, filepath.Join(lib, "Opener.mp4"), "clip-opener")

	a, probes := newLibraryStore(t, lib)
	if _, err := a.ScanLibrary(); err != nil {
		t.Fatal(err)
	}
	if *probes != 1 {
		t.Fatalf("probes = %d, want 1", *probes)
	}
	if _, err := os.Stat(filepath.Join(lib, "Opener.mp4.json")); err != nil {
		t.Fatalf("no sidecar: %v", err)
	}

	b, probesB := newLibraryStore(t, lib)
	if _, err := b.ScanLibrary(); err != nil {
		t.Fatal(err)
	}
	if *probesB != 0 {
		t.Fatalf("second store probed %d times, want 0", *probesB)
	}
	if len(b.List()) != 1 || b.List()[0].Width != 1920 {
		t.Fatalf("second store lists %+v", b.List())
	}

	// A sidecar that no longer matches the file is ignored.
	if err := os.Remove(filepath.Join(lib, "Opener.mp4")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(lib, "Opener.mp4"), "clip-opener-v2")
	c, probesC := newLibraryStore(t, lib)
	if _, err := c.ScanLibrary(); err != nil {
		t.Fatal(err)
	}
	if *probesC != 1 || c.List()[0].ByteSize != int64(len("clip-opener-v2")) {
		t.Fatalf("stale sidecar used: probes=%d clips=%+v", *probesC, c.List())
	}
}

// A clip uploaded on one engine lands in the library and is served from
// there, so the next booking, or the other engine, finds it.
func TestPublishCopiesIntoLibrary(t *testing.T) {
	lib := t.TempDir()
	a, _ := newLibraryStore(t, lib)
	writeFile(t, filepath.Join(a.Dir(), "upload.ts"), "clip-upload")
	c := &Clip{Name: "Upload", Filename: "upload.ts", Source: SourceUpload, ByteSize: 11, Width: 1280}
	if err := a.Add(c); err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(c.ID); err != nil {
		t.Fatal(err)
	}

	got, _ := a.Get(c.ID)
	if got.LibraryPath != c.ID+".ts" || got.Filename != "" {
		t.Fatalf("after publish: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(a.Dir(), "upload.ts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local copy kept: %v", err)
	}
	if want := []string{c.ID + ".ts", c.ID + ".ts.json"}; !equal(libraryFiles(t, lib), want) {
		t.Fatalf("library holds %v, want %v", libraryFiles(t, lib), want)
	}
	if a.TotalBytes() != 0 {
		t.Fatalf("library clip counted against local storage: %d", a.TotalBytes())
	}

	next, probes := newLibraryStore(t, lib)
	if _, err := next.ScanLibrary(); err != nil {
		t.Fatal(err)
	}
	n, err := next.Get(c.ID)
	if err != nil {
		t.Fatalf("next booking does not see the upload: %v", err)
	}
	if n.Name != "Upload" || n.Width != 1280 || *probes != 0 {
		t.Fatalf("next booking sees %+v after %d probes, want the upload's own metadata", n, *probes)
	}
}

// Publish only creates files. A library file already present under the
// clip's name, as when the other engine published the same upload, is
// adopted and left as it is: the library cannot overwrite in place.
func TestPublishNeverOverwrites(t *testing.T) {
	lib := t.TempDir()
	a, _ := newLibraryStore(t, lib)
	const id = "0123456789abcdef0123456789abcdef"
	writeFile(t, filepath.Join(lib, id+".ts"), "clip-from-peer")
	writeFile(t, filepath.Join(a.Dir(), "upload.ts"), "clip-from-here")
	if err := a.AddWithID(id, &Clip{Name: "Shared", Filename: "upload.ts", ByteSize: 14}); err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(id); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(lib, id+".ts"))
	if string(data) != "clip-from-peer" {
		t.Fatalf("library file overwritten: %q", data)
	}
	if got, _ := a.Get(id); got.LibraryPath != id+".ts" {
		t.Fatalf("not adopted: %+v", got)
	}
}

// The library holds media and sidecars only: the index and temporary files
// stay local, since they are renamed into place, which an object-store
// mount cannot do.
func TestLibraryHoldsNoIndex(t *testing.T) {
	lib := t.TempDir()
	a, _ := newLibraryStore(t, lib)
	writeFile(t, filepath.Join(lib, "Opener.mp4"), "clip-opener")
	if _, err := a.ScanLibrary(); err != nil {
		t.Fatal(err)
	}
	id := a.List()[0].ID
	if err := a.Update(id, func(c *Clip) { c.Loop = true }); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Opener.mp4", "Opener.mp4.json"}; !equal(libraryFiles(t, lib), want) {
		t.Fatalf("library holds %v, want %v", libraryFiles(t, lib), want)
	}
	var side Clip
	data, _ := os.ReadFile(filepath.Join(lib, "Opener.mp4.json"))
	if err := json.Unmarshal(data, &side); err != nil || !side.Loop {
		t.Fatalf("sidecar not updated: %s (%v)", data, err)
	}
}

// Deleting a library clip deletes it from the library, so no later booking
// lists it; a store that still lists it drops it on its next scan.
func TestDeleteRemovesFromLibrary(t *testing.T) {
	lib := t.TempDir()
	writeFile(t, filepath.Join(lib, "Opener.mp4"), "clip-opener")
	a, _ := newLibraryStore(t, lib)
	b, _ := newLibraryStore(t, lib)
	for _, s := range []*Store{a, b} {
		if _, err := s.ScanLibrary(); err != nil {
			t.Fatal(err)
		}
	}
	id := a.List()[0].ID
	if err := a.Delete(id); err != nil {
		t.Fatal(err)
	}
	if files := libraryFiles(t, lib); len(files) != 0 {
		t.Fatalf("library still holds %v", files)
	}
	res, err := b.ScanLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 1 || len(b.List()) != 0 {
		t.Fatalf("other store after rescan: %+v, %d clips", res, len(b.List()))
	}
	// Already gone from the library is not an error for the second delete.
	c, _ := newLibraryStore(t, lib)
	writeFile(t, filepath.Join(lib, "Again.mp4"), "clip-again")
	if _, err := c.ScanLibrary(); err != nil {
		t.Fatal(err)
	}
	againID := c.List()[0].ID
	_ = os.Remove(filepath.Join(lib, "Again.mp4"))
	if err := c.Delete(againID); err != nil {
		t.Fatalf("delete of a clip already gone from the library: %v", err)
	}
}

// Ephemeral cleanup at start must not delete exported replays from the
// library: they are there to be kept for later bookings.
func TestCleanEphemeralKeepsLibraryClips(t *testing.T) {
	lib := t.TempDir()
	a, _ := newLibraryStore(t, lib)
	writeFile(t, filepath.Join(a.Dir(), "replay.ts"), "clip-replay")
	c := &Clip{Name: "cam1 replay", Filename: "replay.ts", Source: SourceReplay, Ephemeral: true,
		CreatedAt: time.Now().Add(-48 * time.Hour)}
	if err := a.Add(c); err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(c.ID); err != nil {
		t.Fatal(err)
	}
	if n := a.CleanEphemeral(24*time.Hour, nil); n != 0 {
		t.Fatalf("cleaned %d library clips", n)
	}
	if _, err := os.Stat(filepath.Join(lib, c.ID+".ts")); err != nil {
		t.Fatalf("replay gone from library: %v", err)
	}
}

// Without a library the store behaves as before: nothing is copied and a
// scan is a no-op.
func TestNoLibraryIsANoOp(t *testing.T) {
	s, err := NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(s.Dir(), "a.ts"), "clip-a")
	c := &Clip{Name: "a", Filename: "a.ts", ByteSize: 6}
	if err := s.Add(c); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(c.ID); err != nil {
		t.Fatal(err)
	}
	if res, err := s.ScanLibrary(); err != nil || res.Added != 0 {
		t.Fatalf("scan without library: %+v %v", res, err)
	}
	if got, _ := s.Get(c.ID); got.Filename != "a.ts" || got.LibraryPath != "" {
		t.Fatalf("clip changed: %+v", got)
	}
}

// A real MPEG-TS clip dropped into the library is probed with Validate
// and plays from there.
func TestScanLibraryValidatesRealClip(t *testing.T) {
	lib := t.TempDir()
	if err := os.WriteFile(filepath.Join(lib, "Real.ts"), generateTestTS(t, 30), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachLibrary(lib); err != nil {
		t.Fatal(err)
	}
	res, err := s.ScanLibrary()
	if err != nil || res.Added != 1 {
		t.Fatalf("scan = %+v, %v", res, err)
	}
	c := s.List()[0]
	if c.Codec != "h264" || c.Width == 0 || c.DurationMs == 0 {
		t.Fatalf("clip = %+v", c)
	}
	frames, _, err := DemuxFile(s.MediaPath(c))
	if err != nil || len(frames) == 0 {
		t.Fatalf("demux library clip: %d frames, %v", len(frames), err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A copy killed part way still leaves a file: the kernel closes the
// descriptor and the store completes the upload with what was written. The
// pending marker Publish writes first keeps a scan from listing that file
// as a clip of the wrong length.
func TestScanSkipsMediaWithPendingMarker(t *testing.T) {
	lib := t.TempDir()
	writeFile(t, filepath.Join(lib, "0123456789abcdef0123456789abcdef.ts"), "clip-cut-sh")
	writeFile(t, filepath.Join(lib, "0123456789abcdef0123456789abcdef.ts.pending"), "x")
	s, probes := newLibraryStore(t, lib)
	res, err := s.ScanLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 || len(res.Skipped) != 1 || *probes != 0 {
		t.Fatalf("scan = %+v after %d probes, want the pending clip skipped unprobed", res, *probes)
	}
	if _, err := os.Stat(filepath.Join(lib, "0123456789abcdef0123456789abcdef.ts.json")); !os.IsNotExist(err) {
		t.Fatalf("a sidecar now vouches for a cut-short file: %v", err)
	}
}

// The next Publish of a clip whose copy was cut short replaces the file
// rather than taking the short one as the other engine's.
func TestPublishReplacesCutShortCopy(t *testing.T) {
	lib := t.TempDir()
	const id = "0123456789abcdef0123456789abcdef"
	writeFile(t, filepath.Join(lib, id+".ts"), "clip-cut")
	writeFile(t, filepath.Join(lib, id+".ts.pending"), id)
	s, _ := newLibraryStore(t, lib)
	writeFile(t, filepath.Join(s.Dir(), "upload.ts"), "clip-complete")
	if err := s.AddWithID(id, &Clip{Name: "Upload", Filename: "upload.ts", ByteSize: 13}); err != nil {
		t.Fatal(err)
	}
	if n, left := s.PublishLocal(); n != 1 || left != 0 {
		t.Fatalf("PublishLocal = %d published, %d left, want 1, 0", n, left)
	}
	data, _ := os.ReadFile(filepath.Join(lib, id+".ts"))
	if string(data) != "clip-complete" {
		t.Fatalf("library holds %q", data)
	}
	if want := []string{id + ".ts", id + ".ts.json"}; !equal(libraryFiles(t, lib), want) {
		t.Fatalf("library holds %v, want %v", libraryFiles(t, lib), want)
	}
}

// The sidecar only saves a probe. A sidecar that cannot be written leaves
// the media in the library and the clip served from there.
func TestPublishKeepsMediaWhenSidecarFails(t *testing.T) {
	lib := t.TempDir()
	const id = "0123456789abcdef0123456789abcdef"
	// A non-empty directory where the sidecar goes: neither removed nor
	// created over.
	writeFile(t, filepath.Join(lib, id+".ts.json", "blocker"), "x")
	s, _ := newLibraryStore(t, lib)
	writeFile(t, filepath.Join(s.Dir(), "upload.ts"), "clip-upload")
	if err := s.AddWithID(id, &Clip{Name: "Upload", Filename: "upload.ts", ByteSize: 11}); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(id); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(lib, id+".ts")); err != nil || string(data) != "clip-upload" {
		t.Fatalf("library media: %q %v", data, err)
	}
	if got, _ := s.Get(id); got.LibraryPath != id+".ts" {
		t.Fatalf("clip not served from the library: %+v", got)
	}
	if err := s.Update(id, func(c *Clip) { c.Loop = true }); err != nil {
		t.Fatalf("update with an unwritable sidecar: %v", err)
	}
}

// A retry that cannot replace the cut-short file leaves the marker, so the
// file stays unlisted, and the clip local and playable.
func TestPublishKeepsThePendingMarkerWhenTheRetryFails(t *testing.T) {
	lib := t.TempDir()
	const id = "0123456789abcdef0123456789abcdef"
	// A non-empty directory in place of the media: not removable.
	writeFile(t, filepath.Join(lib, id+".ts", "blocker"), "x")
	writeFile(t, filepath.Join(lib, id+".ts.pending"), id)
	s, _ := newLibraryStore(t, lib)
	writeFile(t, filepath.Join(s.Dir(), "upload.ts"), "clip-complete")
	if err := s.AddWithID(id, &Clip{Name: "Upload", Filename: "upload.ts", ByteSize: 13}); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(id); err == nil {
		t.Fatal("publish over an unremovable file succeeded")
	}
	if _, err := os.Stat(filepath.Join(lib, id+".ts.pending")); err != nil {
		t.Fatalf("marker removed: %v", err)
	}
	if got, _ := s.Get(id); got.LibraryPath != "" || got.Filename != "upload.ts" {
		t.Fatalf("clip moved off its local file: %+v", got)
	}
}
