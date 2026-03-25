package playout

import (
	"encoding/json"
	"fmt"
	"os"
)

// ValidatePlaylist checks that a playlist is well-formed:
//   - at least one item
//   - every item has a non-empty ID
//   - no duplicate item IDs
//   - clip items require AssetURI
//   - live items require Source
//   - only known item types are accepted
func ValidatePlaylist(pl *Playlist) error {
	if len(pl.Items) == 0 {
		return ErrEmptyPlaylist
	}

	seen := make(map[string]bool, len(pl.Items))
	for i, item := range pl.Items {
		if item.ID == "" {
			return fmt.Errorf("item[%d]: missing ID", i)
		}
		if seen[item.ID] {
			return fmt.Errorf("item[%d]: duplicate ID %q", i, item.ID)
		}
		seen[item.ID] = true

		switch item.Type {
		case ItemClip, ItemSlate:
			if item.AssetURI == "" {
				return fmt.Errorf("item[%d] (%s): clip/slate requires asset_uri", i, item.ID)
			}
		case ItemLive:
			if item.Source == "" {
				return fmt.Errorf("item[%d] (%s): live item requires source", i, item.ID)
			}
		default:
			return fmt.Errorf("item[%d] (%s): unknown item type %q", i, item.ID, item.Type)
		}
	}

	return nil
}

// LoadPlaylist reads a JSON playlist file from disk, unmarshals it,
// and validates the result.
func LoadPlaylist(path string) (*Playlist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read playlist: %w", err)
	}

	var pl Playlist
	if err := json.Unmarshal(data, &pl); err != nil {
		return nil, fmt.Errorf("parse playlist: %w", err)
	}

	if err := ValidatePlaylist(&pl); err != nil {
		return nil, fmt.Errorf("validate playlist: %w", err)
	}

	return &pl, nil
}

// SavePlaylist marshals a playlist to indented JSON and writes it to disk.
func SavePlaylist(path string, pl *Playlist) error {
	data, err := json.MarshalIndent(pl, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal playlist: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write playlist: %w", err)
	}

	return nil
}
