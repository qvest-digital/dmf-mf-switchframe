package playout

import (
	"os"
	"path/filepath"
	"testing"
)

func validPlaylist() *Playlist {
	return &Playlist{
		ID:   "pl-1",
		Name: "Test Show",
		Items: []PlaylistItem{
			{
				ID:       "item-1",
				Type:     ItemClip,
				AssetURI: "file:///clips/intro.ts",
				Title:    "Intro",
			},
			{
				ID:     "item-2",
				Type:   ItemLive,
				Source: "camera-1",
				Title:  "Live Camera",
			},
			{
				ID:       "item-3",
				Type:     ItemSlate,
				AssetURI: "file:///slates/brb.ts",
				Title:    "BRB Slate",
			},
		},
	}
}

func TestValidatePlaylist_Valid(t *testing.T) {
	pl := validPlaylist()
	if err := ValidatePlaylist(pl); err != nil {
		t.Fatalf("expected valid playlist, got error: %v", err)
	}
}

func TestValidatePlaylist_Empty(t *testing.T) {
	pl := &Playlist{ID: "pl-1", Name: "Empty"}
	err := ValidatePlaylist(pl)
	if err != ErrEmptyPlaylist {
		t.Fatalf("expected ErrEmptyPlaylist, got: %v", err)
	}
}

func TestValidatePlaylist_DuplicateIDs(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "Dupes",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemClip, AssetURI: "file:///a.ts"},
			{ID: "item-1", Type: ItemClip, AssetURI: "file:///b.ts"},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for duplicate IDs")
	}
}

func TestValidatePlaylist_ClipWithoutURI(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "No URI",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemClip, Title: "Missing URI"},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for clip without asset_uri")
	}
}

func TestValidatePlaylist_LiveWithoutSource(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "No Source",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemLive, Title: "Missing Source"},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for live without source")
	}
}

func TestValidatePlaylist_MissingID(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "No Item ID",
		Items: []PlaylistItem{
			{Type: ItemClip, AssetURI: "file:///a.ts"},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for item without ID")
	}
}

func TestValidatePlaylist_UnknownType(t *testing.T) {
	pl := &Playlist{
		ID:   "pl-1",
		Name: "Bad Type",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemType("unknown"), AssetURI: "file:///a.ts"},
		},
	}
	err := ValidatePlaylist(pl)
	if err == nil {
		t.Fatal("expected error for unknown item type")
	}
}

func TestLoadPlaylist_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "playlist.json")

	original := validPlaylist()
	if err := SavePlaylist(path, original); err != nil {
		t.Fatalf("SavePlaylist failed: %v", err)
	}

	loaded, err := LoadPlaylist(path)
	if err != nil {
		t.Fatalf("LoadPlaylist failed: %v", err)
	}

	if loaded.ID != original.ID {
		t.Errorf("ID mismatch: got %q, want %q", loaded.ID, original.ID)
	}
	if loaded.Name != original.Name {
		t.Errorf("Name mismatch: got %q, want %q", loaded.Name, original.Name)
	}
	if len(loaded.Items) != len(original.Items) {
		t.Fatalf("Items count mismatch: got %d, want %d", len(loaded.Items), len(original.Items))
	}
	for i, item := range loaded.Items {
		orig := original.Items[i]
		if item.ID != orig.ID {
			t.Errorf("item[%d] ID mismatch: got %q, want %q", i, item.ID, orig.ID)
		}
		if item.Type != orig.Type {
			t.Errorf("item[%d] Type mismatch: got %q, want %q", i, item.Type, orig.Type)
		}
		if item.AssetURI != orig.AssetURI {
			t.Errorf("item[%d] AssetURI mismatch: got %q, want %q", i, item.AssetURI, orig.AssetURI)
		}
		if item.Source != orig.Source {
			t.Errorf("item[%d] Source mismatch: got %q, want %q", i, item.Source, orig.Source)
		}
		if item.Title != orig.Title {
			t.Errorf("item[%d] Title mismatch: got %q, want %q", i, item.Title, orig.Title)
		}
	}
}

func TestLoadPlaylist_FileNotFound(t *testing.T) {
	_, err := LoadPlaylist("/nonexistent/path/playlist.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoadPlaylist_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json}"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := LoadPlaylist(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
