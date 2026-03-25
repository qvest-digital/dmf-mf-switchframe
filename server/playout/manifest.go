package playout

import (
	"encoding/json"
	"fmt"
	"os"
)

// validSecondaryEventTypes is the set of allowed secondary event type strings.
var validSecondaryEventTypes = map[string]bool{
	"graphics_on":     true,
	"graphics_off":    true,
	"graphics_update": true,
	"audio_level":     true,
	"audio_mute":      true,
	"audio_afv":       true,
	"caption_mode":    true,
	"caption_text":    true,
	"scte35_cue":      true,
}

// ValidateSecondaryEvent checks that a secondary event is well-formed.
func ValidateSecondaryEvent(ev *SecondaryEvent) error {
	if !validSecondaryEventTypes[ev.Type] {
		return fmt.Errorf("secondary event: unknown type %q", ev.Type)
	}

	if ev.Anchor != "start" && ev.Anchor != "end" {
		return fmt.Errorf("secondary event: anchor must be \"start\" or \"end\", got %q", ev.Anchor)
	}

	switch ev.Type {
	case "audio_level", "audio_mute", "audio_afv":
		if ev.SourceKey == "" {
			return fmt.Errorf("secondary event %q: source_key required", ev.Type)
		}
	case "caption_mode":
		if ev.CaptionMode == "" {
			return fmt.Errorf("secondary event caption_mode: caption_mode field required")
		}
	case "caption_text":
		if ev.CaptionText == "" {
			return fmt.Errorf("secondary event caption_text: caption_text field required")
		}
	case "scte35_cue":
		if ev.SCTE35 == nil {
			return fmt.Errorf("secondary event scte35_cue: scte35 data required")
		}
	}

	return nil
}

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

		for j := range item.SecondaryEvents {
			if err := ValidateSecondaryEvent(&item.SecondaryEvents[j]); err != nil {
				return fmt.Errorf("item[%d] (%s): secondary_events[%d]: %w", i, item.ID, j, err)
			}
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
