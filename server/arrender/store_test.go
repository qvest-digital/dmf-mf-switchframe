package arrender

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssetStore_NewEmpty(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}
	defer store.Close()

	if len(store.List()) != 0 {
		t.Error("new store should have no assets")
	}
}

func TestAssetStore_Upload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	data := []byte("fake glb data")
	info, err := store.Upload("My Model", "model.glb", data)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if info.Name != "My Model" {
		t.Errorf("Name = %q, want %q", info.Name, "My Model")
	}
	if info.Format != "glb" {
		t.Errorf("Format = %q, want %q", info.Format, "glb")
	}
	if info.ByteSize != int64(len(data)) {
		t.Errorf("ByteSize = %d, want %d", info.ByteSize, len(data))
	}
	if info.ID == "" {
		t.Error("ID should not be empty")
	}
	if info.CreatedAt == 0 {
		t.Error("CreatedAt should not be zero")
	}

	// Verify file exists on disk.
	fp, err := store.FilePath(info.ID)
	if err != nil {
		t.Fatalf("FilePath: %v", err)
	}
	diskData, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(diskData) != string(data) {
		t.Error("disk data does not match uploaded data")
	}
}

func TestAssetStore_UploadAllFormats(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 20)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	tests := []struct {
		filename string
		format   string
	}{
		{"model.glb", "glb"},
		{"scene.gltf", "gltf"},
		{"model.usd", "usd"},
		{"scene.usda", "usda"},
		{"binary.usdc", "usdc"},
		{"archive.usdz", "usdz"},
		{"env.hdr", "hdr"},
		{"env.exr", "exr"},
		{"MODEL.GLB", "glb"}, // case insensitive
	}

	for _, tt := range tests {
		info, err := store.Upload(tt.filename, tt.filename, []byte("data"))
		if err != nil {
			t.Errorf("Upload(%q): %v", tt.filename, err)
			continue
		}
		if info.Format != tt.format {
			t.Errorf("Upload(%q).Format = %q, want %q", tt.filename, info.Format, tt.format)
		}
	}
}

func TestAssetStore_UploadInvalidExtension(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	_, err = store.Upload("bad", "model.obj", []byte("data"))
	if !errors.Is(err, ErrInvalidAsset) {
		t.Errorf("Upload(.obj) = %v, want ErrInvalidAsset", err)
	}

	_, err = store.Upload("bad", "model.fbx", []byte("data"))
	if !errors.Is(err, ErrInvalidAsset) {
		t.Errorf("Upload(.fbx) = %v, want ErrInvalidAsset", err)
	}
}

func TestAssetStore_UploadEmptyName(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	_, err = store.Upload("", "model.glb", []byte("data"))
	if !errors.Is(err, ErrInvalidAsset) {
		t.Errorf("Upload(empty name) = %v, want ErrInvalidAsset", err)
	}
}

func TestAssetStore_UploadPathTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	// Path traversal in filename.
	_, err = store.Upload("ok", "../../../etc/passwd.glb", []byte("data"))
	if !errors.Is(err, ErrInvalidAsset) {
		t.Errorf("Upload(path traversal filename) = %v, want ErrInvalidAsset", err)
	}

	// Path traversal in name.
	_, err = store.Upload("../../../etc", "model.glb", []byte("data"))
	if !errors.Is(err, ErrInvalidAsset) {
		t.Errorf("Upload(path traversal name) = %v, want ErrInvalidAsset", err)
	}

	// Backslash in filename.
	_, err = store.Upload("ok", "..\\model.glb", []byte("data"))
	if !errors.Is(err, ErrInvalidAsset) {
		t.Errorf("Upload(backslash traversal) = %v, want ErrInvalidAsset", err)
	}
}

func TestAssetStore_Get(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	info, _ := store.Upload("Test Model", "model.glb", []byte("data"))

	got, err := store.Get(info.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Test Model" {
		t.Errorf("Name = %q, want %q", got.Name, "Test Model")
	}

	// Verify it returns a copy.
	got.Name = "mutated"
	got2, _ := store.Get(info.ID)
	if got2.Name != "Test Model" {
		t.Error("Get should return a copy, not a reference")
	}
}

func TestAssetStore_Get_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	_, err = store.Get("nonexistent")
	if !errors.Is(err, ErrAssetNotFound) {
		t.Errorf("Get(nonexistent) = %v, want ErrAssetNotFound", err)
	}
}

func TestAssetStore_List(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	// Upload in reverse order to verify sorting.
	_, _ = store.Upload("Third", "c.glb", []byte("data"))
	_, _ = store.Upload("First", "a.glb", []byte("data"))
	_, _ = store.Upload("Second", "b.glb", []byte("data"))

	list := store.List()
	if len(list) != 3 {
		t.Fatalf("len(List) = %d, want 3", len(list))
	}

	// Verify sorted by CreatedAt ascending.
	for i := 1; i < len(list); i++ {
		if list[i].CreatedAt < list[i-1].CreatedAt {
			t.Error("List should be sorted by CreatedAt ascending")
		}
	}
}

func TestAssetStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	info, _ := store.Upload("Model", "model.glb", []byte("data"))
	fp, _ := store.FilePath(info.ID)

	// Verify file exists.
	if _, err := os.Stat(fp); err != nil {
		t.Fatalf("asset file should exist: %v", err)
	}

	if err := store.Delete(info.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify file removed.
	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Error("asset file should be deleted")
	}

	// Verify metadata removed.
	_, err = store.Get(info.ID)
	if !errors.Is(err, ErrAssetNotFound) {
		t.Errorf("Get after delete = %v, want ErrAssetNotFound", err)
	}

	if len(store.List()) != 0 {
		t.Error("store should be empty after delete")
	}
}

func TestAssetStore_Delete_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	err = store.Delete("nonexistent")
	if !errors.Is(err, ErrAssetNotFound) {
		t.Errorf("Delete(nonexistent) = %v, want ErrAssetNotFound", err)
	}
}

func TestAssetStore_MaxAssets(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 2)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	_, err = store.Upload("One", "a.glb", []byte("data"))
	if err != nil {
		t.Fatalf("Upload 1: %v", err)
	}
	_, err = store.Upload("Two", "b.glb", []byte("data"))
	if err != nil {
		t.Fatalf("Upload 2: %v", err)
	}

	_, err = store.Upload("Three", "c.glb", []byte("data"))
	if !errors.Is(err, ErrTooManyAssets) {
		t.Errorf("Upload 3 = %v, want ErrTooManyAssets", err)
	}

	// After deleting one, should be able to upload again.
	list := store.List()
	if err := store.Delete(list[0].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.Upload("Three", "c.glb", []byte("data"))
	if err != nil {
		t.Errorf("Upload after delete: %v", err)
	}
}

func TestAssetStore_Persistence(t *testing.T) {
	dir := t.TempDir()

	// Create store and upload an asset.
	store1, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore 1: %v", err)
	}

	info, err := store1.Upload("Persistent", "model.glb", []byte("persisted data"))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	savedID := info.ID

	// Close and reopen.
	_ = store1.Close()

	store2, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore 2: %v", err)
	}

	// Verify asset is still there.
	got, err := store2.Get(savedID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Name != "Persistent" {
		t.Errorf("Name = %q, want %q", got.Name, "Persistent")
	}
	if got.Format != "glb" {
		t.Errorf("Format = %q, want %q", got.Format, "glb")
	}
	if got.ByteSize != int64(len("persisted data")) {
		t.Errorf("ByteSize = %d, want %d", got.ByteSize, len("persisted data"))
	}

	// Verify file contents survived.
	fp, _ := store2.FilePath(savedID)
	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("ReadFile after reopen: %v", err)
	}
	if string(data) != "persisted data" {
		t.Error("file contents differ after reopen")
	}
}

func TestAssetStore_FilePath_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	_, err = store.FilePath("nope")
	if !errors.Is(err, ErrAssetNotFound) {
		t.Errorf("FilePath(nope) = %v, want ErrAssetNotFound", err)
	}
}

func TestAssetStore_DefaultMaxAssets(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 0)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}
	if store.maxAssets != 64 {
		t.Errorf("maxAssets = %d, want 64 (default)", store.maxAssets)
	}
}

func TestAssetStore_MetadataFileLocation(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	_, _ = store.Upload("Test", "model.glb", []byte("data"))

	// Verify assets.json exists.
	metaPath := filepath.Join(dir, "assets.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Errorf("assets.json should exist: %v", err)
	}
}

func TestAssetStore_FilenameValidation(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	tests := []struct {
		name     string
		filename string
		wantErr  bool
	}{
		{"empty filename", "", true},
		{"dot", ".", true},
		{"dot dot", "..", true},
		{"slash", "foo/bar.glb", true},
		{"backslash", "foo\\bar.glb", true},
		{"valid", "model.glb", false},
		{"valid with spaces", "my model.glb", false},
	}

	for _, tt := range tests {
		_, err := store.Upload("test", tt.filename, []byte("data"))
		if tt.wantErr && err == nil {
			t.Errorf("Upload(%q): expected error", tt.name)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("Upload(%q): unexpected error: %v", tt.name, err)
		}
	}
}

func TestAssetStore_DiskFilenameUsesID(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAssetStore(dir, 10)
	if err != nil {
		t.Fatalf("NewAssetStore: %v", err)
	}

	info, err := store.Upload("My Model", "original-name.glb", []byte("data"))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// The disk filename should be based on the generated ID, not the original name.
	if !strings.HasPrefix(info.Filename, info.ID) {
		t.Errorf("disk filename %q should start with ID %q", info.Filename, info.ID)
	}
	if !strings.HasSuffix(info.Filename, ".glb") {
		t.Errorf("disk filename %q should end with .glb", info.Filename)
	}
}

func TestBundledAssets(t *testing.T) {
	store, err := NewAssetStore(t.TempDir(), 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.LoadBundledAssets(); err != nil {
		t.Fatal(err)
	}
	assets := store.List()
	if len(assets) < 3 {
		t.Errorf("expected 3+ bundled assets, got %d", len(assets))
	}

	names := make(map[string]bool)
	for _, a := range assets {
		t.Logf("Bundled: %s (%s, %d bytes)", a.Name, a.Filename, a.ByteSize)
		names[a.Name] = true
	}

	for _, want := range []string{"podium", "clock", "ltstand"} {
		if !names[want] {
			t.Errorf("missing bundled asset %q", want)
		}
	}
}

func TestBundledAssets_Idempotent(t *testing.T) {
	store, err := NewAssetStore(t.TempDir(), 16)
	if err != nil {
		t.Fatal(err)
	}

	// Load twice — second call should be a no-op.
	if err := store.LoadBundledAssets(); err != nil {
		t.Fatal(err)
	}
	if err := store.LoadBundledAssets(); err != nil {
		t.Fatal(err)
	}

	assets := store.List()
	if len(assets) != 3 {
		t.Errorf("expected exactly 3 bundled assets after double load, got %d", len(assets))
	}
}

func TestBundledAssetsParse(t *testing.T) {
	store, err := NewAssetStore(t.TempDir(), 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.LoadBundledAssets(); err != nil {
		t.Fatal(err)
	}

	for _, asset := range store.List() {
		path, err := store.FilePath(asset.ID)
		if err != nil {
			t.Fatalf("FilePath(%s): %v", asset.ID, err)
		}
		meshes, err := ParseGLTF(path)
		if err != nil {
			t.Errorf("parse %s: %v", asset.Name, err)
			continue
		}
		if len(meshes) == 0 {
			t.Errorf("%s: no meshes found", asset.Name)
			continue
		}
		for _, m := range meshes {
			if len(m.Vertices) == 0 {
				t.Errorf("%s mesh %q: no vertices", asset.Name, m.Name)
			}
			if len(m.Indices) == 0 {
				t.Errorf("%s mesh %q: no indices", asset.Name, m.Name)
			}
			if len(m.Normals) == 0 {
				t.Errorf("%s mesh %q: no normals", asset.Name, m.Name)
			}
			t.Logf("  %s/%s: %d verts, %d tris, material=%.2f/%.2f/%.2f metal=%.1f rough=%.1f",
				asset.Name, m.Name,
				len(m.Vertices), len(m.Indices)/3,
				m.Material.BaseColor[0], m.Material.BaseColor[1], m.Material.BaseColor[2],
				m.Material.Metallic, m.Material.Roughness)
		}
	}
}
