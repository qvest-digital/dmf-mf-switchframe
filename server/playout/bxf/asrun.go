package bxf

import (
	"fmt"

	"github.com/zsiec/switchframe/server/playout"
)

const bxfTimeFormat = "2006-01-02T15:04:05.000"

// ExportAsRun converts a slice of as-run events into a BXF message.
// It pairs item_start and item_end events by ItemID. Non-item events
// (pod operations, splice, manual override, etc.) are excluded from BXF output.
func ExportAsRun(events []playout.AsRunEvent) BxfMessage {
	type itemPair struct {
		start *playout.AsRunEvent
		end   *playout.AsRunEvent
	}

	// Collect start/end pairs by ItemID, preserving insertion order.
	pairs := make(map[string]*itemPair)
	var order []string

	for i := range events {
		ev := &events[i]

		switch ev.Type {
		case playout.AsRunItemStart:
			if _, exists := pairs[ev.ItemID]; !exists {
				pairs[ev.ItemID] = &itemPair{}
				order = append(order, ev.ItemID)
			}
			pairs[ev.ItemID].start = ev

		case playout.AsRunItemEnd:
			if _, exists := pairs[ev.ItemID]; !exists {
				pairs[ev.ItemID] = &itemPair{}
				order = append(order, ev.ItemID)
			}
			pairs[ev.ItemID].end = ev

		default:
			// Skip non-item events.
			continue
		}
	}

	// Build AsRunDetail entries in insertion order.
	details := make([]AsRunDetail, 0, len(order))
	for _, id := range order {
		pair := pairs[id]
		detail := AsRunDetail{
			Status: "Aired",
		}

		// Use start event for content info and start time.
		if pair.start != nil {
			detail.Content = Content{
				ContentId: ContentId{HouseNumber: pair.start.ItemID},
				Name:      pair.start.Title,
			}
			detail.ActualStartTime = pair.start.Timestamp.UTC().Format(bxfTimeFormat)

			if pair.start.ScheduledTS != nil {
				detail.ScheduledTime = pair.start.ScheduledTS.UTC().Format(bxfTimeFormat)
			}
		}

		// Use end event for end time and duration.
		if pair.end != nil {
			// If we had no start, use end event for content info.
			if pair.start == nil {
				detail.Content = Content{
					ContentId: ContentId{HouseNumber: pair.end.ItemID},
					Name:      pair.end.Title,
				}
			}

			detail.ActualEndTime = pair.end.Timestamp.UTC().Format(bxfTimeFormat)

			if pair.end.ActualDurationMs > 0 {
				secs := float64(pair.end.ActualDurationMs) / 1000.0
				detail.ActualDuration = fmt.Sprintf("PT%.3fS", secs)
				detail.Content.Duration = detail.ActualDuration
			}
		}

		details = append(details, detail)
	}

	return BxfMessage{
		BxfData: BxfData{
			AsRun: &AsRun{
				Details: details,
			},
		},
	}
}
