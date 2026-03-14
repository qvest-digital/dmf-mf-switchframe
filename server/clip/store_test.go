// server/clip/store_test.go
package clip

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreCreateAndList(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, 10*1024*1024) // 10MB limit
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("new store should be empty")
	}

	c := &Clip{
		Name:     "test",
		Filename: "test.ts",
		Source:   SourceUpload,
		Codec:    "h264",
		Width:    1920,
		Height:   1080,
		ByteSize: 1000,
	}
	if err := s.Add(c); err != nil {
		t.Fatal(err)
	}
	if c.ID == "" {
		t.Error("ID should be assigned")
	}
	if len(s.List()) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(s.List()))
	}
}

func TestStoreGet(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 10*1024*1024)

	c := &Clip{Name: "test", Filename: "test.ts", Source: SourceUpload, Codec: "h264", ByteSize: 100}
	_ = s.Add(c)

	got, err := s.Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "test" {
		t.Errorf("Name = %q, want %q", got.Name, "test")
	}

	_, err = s.Get("nonexistent")
	if err != ErrNotFound {
		t.Errorf("Get nonexistent = %v, want ErrNotFound", err)
	}
}

func TestStoreUpdate(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 10*1024*1024)

	c := &Clip{Name: "old", Filename: "test.ts", Source: SourceUpload, Codec: "h264", ByteSize: 100}
	_ = s.Add(c)

	err := s.Update(c.ID, func(clip *Clip) {
		clip.Name = "new"
		clip.Loop = true
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(c.ID)
	if got.Name != "new" || !got.Loop {
		t.Errorf("Update failed: Name=%q Loop=%v", got.Name, got.Loop)
	}
}

func TestStoreDelete(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 10*1024*1024)

	c := &Clip{Name: "test", Filename: "test.ts", Source: SourceUpload, Codec: "h264", ByteSize: 100}
	_ = s.Add(c)
	// Create the file on disk
	_ = os.WriteFile(filepath.Join(dir, c.Filename), []byte("data"), 0644)

	if err := s.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Error("should be empty after delete")
	}
	if _, err := os.Stat(filepath.Join(dir, c.Filename)); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestStoreStorageLimit(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 500) // 500 bytes limit

	c1 := &Clip{Name: "a", Filename: "a.ts", Source: SourceUpload, Codec: "h264", ByteSize: 300}
	if err := s.Add(c1); err != nil {
		t.Fatal(err)
	}

	c2 := &Clip{Name: "b", Filename: "b.ts", Source: SourceUpload, Codec: "h264", ByteSize: 300}
	if err := s.Add(c2); err != ErrStorageFull {
		t.Errorf("expected ErrStorageFull, got %v", err)
	}
}

func TestStoreEphemeralExemptFromLimit(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 500)

	c1 := &Clip{Name: "a", Filename: "a.ts", Source: SourceUpload, Codec: "h264", ByteSize: 400}
	_ = s.Add(c1)

	c2 := &Clip{Name: "b", Filename: "b.ts", Source: SourceReplay, Codec: "h264", ByteSize: 400, Ephemeral: true}
	if err := s.Add(c2); err != nil {
		t.Errorf("ephemeral should bypass limit, got %v", err)
	}
}

func TestStorePin(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 10*1024*1024)

	c := &Clip{Name: "replay clip", Filename: "r.ts", Source: SourceReplay, Codec: "h264", ByteSize: 100, Ephemeral: true}
	_ = s.Add(c)

	if err := s.Pin(c.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(c.ID)
	if got.Ephemeral {
		t.Error("should no longer be ephemeral after pin")
	}
}

func TestStorePinRejectsWhenFull(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 500)

	c1 := &Clip{Name: "a", Filename: "a.ts", Source: SourceUpload, Codec: "h264", ByteSize: 450}
	_ = s.Add(c1)

	c2 := &Clip{Name: "b", Filename: "b.ts", Source: SourceReplay, Codec: "h264", ByteSize: 100, Ephemeral: true}
	_ = s.Add(c2)

	if err := s.Pin(c2.ID); err != ErrStorageFull {
		t.Errorf("Pin should fail with ErrStorageFull, got %v", err)
	}
}

func TestStoreCleanEphemeral(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 10*1024*1024)

	old := &Clip{
		Name: "old", Filename: "old.ts", Source: SourceReplay, Codec: "h264",
		ByteSize: 100, Ephemeral: true,
		CreatedAt: time.Now().Add(-25 * time.Hour),
	}
	_ = s.Add(old)
	_ = os.WriteFile(filepath.Join(dir, old.Filename), []byte("data"), 0644)

	recent := &Clip{
		Name: "recent", Filename: "recent.ts", Source: SourceReplay, Codec: "h264",
		ByteSize: 100, Ephemeral: true,
		CreatedAt: time.Now(),
	}
	_ = s.Add(recent)

	removed := s.CleanEphemeral(24*time.Hour, nil)
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
	if len(s.List()) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(s.List()))
	}
}

func TestStoreCleanEphemeralSkipsLoadedClips(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 10*1024*1024)

	old := &Clip{
		Name: "loaded", Filename: "loaded.ts", Source: SourceReplay, Codec: "h264",
		ByteSize: 100, Ephemeral: true,
		CreatedAt: time.Now().Add(-25 * time.Hour),
	}
	_ = s.Add(old)

	loadedIDs := map[string]bool{old.ID: true}
	removed := s.CleanEphemeral(24*time.Hour, loadedIDs)
	if removed != 0 {
		t.Errorf("should not remove loaded clip, removed %d", removed)
	}
}

func TestStorePersistence(t *testing.T) {
	dir := t.TempDir()
	s1, _ := NewStore(dir, 10*1024*1024)

	c := &Clip{Name: "persist", Filename: "p.ts", Source: SourceUpload, Codec: "h264", ByteSize: 100}
	_ = s1.Add(c)
	id := c.ID

	// Reload from disk
	s2, err := NewStore(dir, 10*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "persist" {
		t.Errorf("Name = %q after reload, want %q", got.Name, "persist")
	}
}

func TestStoreTotalBytes(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir, 10*1024*1024)

	c1 := &Clip{Name: "a", Filename: "a.ts", Source: SourceUpload, Codec: "h264", ByteSize: 300}
	_ = s.Add(c1)
	c2 := &Clip{Name: "b", Filename: "b.ts", Source: SourceUpload, Codec: "h264", ByteSize: 200}
	_ = s.Add(c2)

	if got := s.TotalBytes(); got != 500 {
		t.Errorf("TotalBytes = %d, want 500", got)
	}
}
