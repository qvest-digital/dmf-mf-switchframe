// Package bxf provides BXF (SMPTE S22) XML types for as-run export and schedule import.
package bxf

import "encoding/xml"

// BxfMessage is the root element of a BXF document.
type BxfMessage struct {
	XMLName xml.Name `xml:"BxfMessage"`
	BxfData BxfData  `xml:"BxfData"`
}

// BxfData contains either as-run or schedule data (or both).
type BxfData struct {
	AsRun    *AsRun    `xml:"AsRun,omitempty"`
	Schedule *Schedule `xml:"Schedule,omitempty"`
}

// AsRun holds a collection of as-run detail entries.
type AsRun struct {
	Details []AsRunDetail `xml:"AsRunDetail"`
}

// AsRunDetail is a single as-run log entry in BXF format.
type AsRunDetail struct {
	Content         Content `xml:"Content"`
	ScheduledTime   string  `xml:"ScheduledTime,omitempty"`
	ActualStartTime string  `xml:"ActualStartTime,omitempty"`
	ActualEndTime   string  `xml:"ActualEndTime,omitempty"`
	ActualDuration  string  `xml:"ActualDuration,omitempty"`
	Status          string  `xml:"Status"`
	Comment         string  `xml:"Comment,omitempty"`
}

// Schedule holds a collection of schedule detail entries.
type Schedule struct {
	Details []ScheduleDetail `xml:"ScheduleDetail"`
}

// ScheduleDetail is a single schedule entry in BXF format.
type ScheduleDetail struct {
	Content           Content `xml:"Content"`
	ScheduledTime     string  `xml:"ScheduledTime,omitempty"`
	ScheduledDuration string  `xml:"ScheduledDuration,omitempty"`
	Type              string  `xml:"Type,omitempty"`
}

// Content identifies a media asset within a BXF entry.
type Content struct {
	ContentId ContentId `xml:"ContentId"`
	Name      string    `xml:"Name"`
	Duration  string    `xml:"Duration,omitempty"`
}

// ContentId holds the house number identifier for a content asset.
type ContentId struct {
	HouseNumber string `xml:"HouseNumber"`
}
