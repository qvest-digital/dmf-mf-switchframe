package bxf

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zsiec/switchframe/server/playout"
)

// ImportPlaylist converts a BXF Schedule into an internal Playlist.
// Returns an error if the message contains no schedule or empty schedule details.
func ImportPlaylist(msg BxfMessage, name string) (*playout.Playlist, error) {
	if msg.BxfData.Schedule == nil || len(msg.BxfData.Schedule.Details) == 0 {
		return nil, errors.New("bxf: no schedule data in BXF message")
	}

	items := make([]playout.PlaylistItem, 0, len(msg.BxfData.Schedule.Details))

	for _, detail := range msg.BxfData.Schedule.Details {
		item := playout.PlaylistItem{
			ID:    detail.Content.ContentId.HouseNumber,
			Title: detail.Content.Name,
			Type:  mapBxfType(detail.Type),
		}

		// Parse duration from ScheduledDuration first, fall back to Content.Duration.
		dur := detail.ScheduledDuration
		if dur == "" {
			dur = detail.Content.Duration
		}
		item.DurationMs = parseBxfDuration(dur)

		// Parse scheduled time.
		if detail.ScheduledTime != "" {
			ts, err := parseBxfTime(detail.ScheduledTime)
			if err == nil {
				item.ScheduledTime = &ts
			}
		}

		items = append(items, item)
	}

	return &playout.Playlist{
		Name:  name,
		Items: items,
	}, nil
}

// parseBxfDuration parses an ISO 8601 duration of the form PT<n>S or PT<n>.<n>S
// and returns milliseconds. Returns 0 for unparseable input.
func parseBxfDuration(d string) int64 {
	if d == "" {
		return 0
	}

	// Strip PT prefix and S suffix.
	s := d
	if !strings.HasPrefix(s, "PT") {
		return 0
	}
	s = s[2:]

	if !strings.HasSuffix(s, "S") {
		return 0
	}
	s = s[:len(s)-1]

	secs, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}

	return int64(secs * 1000)
}

// parseBxfTime parses a BXF timestamp string. It tries several common formats:
//   - 2006-01-02T15:04:05.000
//   - 2006-01-02T15:04:05
//   - 2006-01-02T15:04:05Z
func parseBxfTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("bxf: empty time string")
	}

	formats := []string{
		"2006-01-02T15:04:05.000",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05Z",
	}

	for _, format := range formats {
		t, err := time.Parse(format, s)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("bxf: unable to parse time %q", s)
}

// mapBxfType maps a BXF type string to an internal ItemType.
// The mapping is case-insensitive. Unknown types default to clip.
func mapBxfType(bxfType string) playout.ItemType {
	switch strings.ToLower(bxfType) {
	case "live":
		return playout.ItemLive
	case "program", "show", "spot", "commercial":
		return playout.ItemClip
	default:
		return playout.ItemClip
	}
}
