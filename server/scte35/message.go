// Package scte35 wraps the Comcast/scte35-go library into Switchframe's
// internal CueMessage representation for SCTE-35 splice signaling.
package scte35

import (
	"fmt"
	"time"

	scte35lib "github.com/Comcast/scte35-go/pkg/scte35"
)

// Command type constants matching the SCTE-35 specification.
const (
	CommandSpliceNull   = 0x00
	CommandSpliceInsert = 0x05
	CommandTimeSignal   = 0x06
)

// CueMessage is Switchframe's internal representation of an SCTE-35 cue.
type CueMessage struct {
	// CommandType identifies the splice command (splice_null, splice_insert, time_signal).
	CommandType uint8

	// EventID is the splice_event_id for splice_insert commands.
	EventID uint32

	// IsOut indicates out-of-network (true = cue-out, false = cue-in).
	IsOut bool

	// AutoReturn indicates the splicer should automatically return to network.
	AutoReturn bool

	// BreakDuration is the break duration. Nil means no duration specified.
	BreakDuration *time.Duration

	// Descriptors holds segmentation descriptors (used with time_signal).
	Descriptors []SegmentationDescriptor

	// DeliveryRestrictions optionally specifies delivery restriction flags.
	DeliveryRestrictions *DeliveryRestrictions

	// SpliceTimePTS is the optional splice time in 90 kHz PTS ticks.
	SpliceTimePTS *int64

	// Timing indicates "immediate" or "scheduled" splice mode.
	Timing string
}

// SegmentationDescriptor carries segmentation metadata for time_signal commands.
type SegmentationDescriptor struct {
	SegmentationType    uint8   `json:"segmentationType"`
	SegEventID          uint32  `json:"segEventId"`
	DurationTicks       *uint64 `json:"durationTicks,omitempty"`
	UPIDType            uint8   `json:"upidType"`
	UPID                []byte  `json:"upid"`
	SubSegmentNum       uint8   `json:"subSegmentNum,omitempty"`
	SubSegmentsExpected uint8   `json:"subSegmentsExpected,omitempty"`
}

// DeliveryRestrictions carries delivery restriction flags.
type DeliveryRestrictions struct {
	WebDeliveryAllowed  bool
	NoRegionalBlackout  bool
	ArchiveAllowed      bool
	DeviceRestrictions  uint8
}

// NewSpliceInsert creates a CueMessage for a splice_insert command.
// Pass duration=0 for no break duration (cue-in).
func NewSpliceInsert(eventID uint32, duration time.Duration, isOut bool, autoReturn bool) *CueMessage {
	msg := &CueMessage{
		CommandType: CommandSpliceInsert,
		EventID:     eventID,
		IsOut:       isOut,
		AutoReturn:  autoReturn,
		Timing:      "immediate",
	}
	if duration > 0 {
		msg.BreakDuration = &duration
	}
	return msg
}

// NewTimeSignal creates a CueMessage for a time_signal command with a single
// segmentation descriptor.
func NewTimeSignal(segType uint8, duration time.Duration, upidType uint8, upid []byte) *CueMessage {
	desc := SegmentationDescriptor{
		SegmentationType: segType,
		UPIDType:         upidType,
		UPID:             upid,
	}
	if duration > 0 {
		ticks := scte35lib.DurationToTicks(duration)
		desc.DurationTicks = &ticks
	}
	return &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{desc},
		Timing:      "immediate",
	}
}

// NewTimeSignalMulti creates a CueMessage for a time_signal command with
// multiple segmentation descriptors.
func NewTimeSignalMulti(descriptors []SegmentationDescriptor) *CueMessage {
	return &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: descriptors,
		Timing:      "immediate",
	}
}

// Encode converts this CueMessage into SCTE-35 binary format.
// If verify is true, the encoded bytes are decoded back and CRC-32 is verified.
func (m *CueMessage) Encode(verify bool) ([]byte, error) {
	sis := &scte35lib.SpliceInfoSection{
		Tier:    4095,
		SAPType: scte35lib.SAPTypeNotSpecified,
	}

	switch m.CommandType {
	case CommandSpliceNull:
		sis.SpliceCommand = &scte35lib.SpliceNull{}

	case CommandSpliceInsert:
		si := &scte35lib.SpliceInsert{
			SpliceEventID:         m.EventID,
			OutOfNetworkIndicator: m.IsOut,
			SpliceImmediateFlag:   true,
			Program:               &scte35lib.SpliceInsertProgram{},
		}
		if m.BreakDuration != nil {
			ticks := scte35lib.DurationToTicks(*m.BreakDuration)
			si.BreakDuration = &scte35lib.BreakDuration{
				AutoReturn: m.AutoReturn,
				Duration:   ticks,
			}
		}
		sis.SpliceCommand = si

	case CommandTimeSignal:
		// Use PTS time 0 for immediate signals.
		sis.SpliceCommand = scte35lib.NewTimeSignal(0)

		for _, d := range m.Descriptors {
			sd := &scte35lib.SegmentationDescriptor{
				SegmentationTypeID: uint32(d.SegmentationType),
				SegmentationEventID: d.SegEventID,
			}
			if d.DurationTicks != nil {
				dur := *d.DurationTicks
				sd.SegmentationDuration = &dur
			}
			// Build UPID list.
			if len(d.UPID) > 0 {
				sd.SegmentationUPIDs = []scte35lib.SegmentationUPID{
					scte35lib.NewSegmentationUPID(uint32(d.UPIDType), d.UPID),
				}
			}
			sis.SpliceDescriptors = append(sis.SpliceDescriptors, sd)
		}

	default:
		return nil, fmt.Errorf("unsupported command type: 0x%02x", m.CommandType)
	}

	encoded, err := sis.Encode()
	if err != nil {
		return nil, fmt.Errorf("scte35 encode: %w", err)
	}

	if verify {
		if _, err := scte35lib.DecodeBytes(encoded); err != nil {
			return nil, fmt.Errorf("scte35 verification failed: %w", err)
		}
	}

	return encoded, nil
}

// Decode parses SCTE-35 binary data into a CueMessage.
// CRC-32 is validated automatically by the underlying library.
func Decode(data []byte) (*CueMessage, error) {
	sis, err := scte35lib.DecodeBytes(data)
	if err != nil {
		return nil, fmt.Errorf("scte35 decode: %w", err)
	}

	msg := &CueMessage{}

	switch cmd := sis.SpliceCommand.(type) {
	case *scte35lib.SpliceNull:
		msg.CommandType = CommandSpliceNull

	case *scte35lib.SpliceInsert:
		msg.CommandType = CommandSpliceInsert
		msg.EventID = cmd.SpliceEventID
		msg.IsOut = cmd.OutOfNetworkIndicator

		if cmd.SpliceImmediateFlag {
			msg.Timing = "immediate"
		} else {
			msg.Timing = "scheduled"
		}

		if cmd.BreakDuration != nil {
			msg.AutoReturn = cmd.BreakDuration.AutoReturn
			dur := scte35lib.TicksToDuration(cmd.BreakDuration.Duration)
			msg.BreakDuration = &dur
		}

		// Extract splice time PTS if specified.
		if cmd.Program != nil && cmd.Program.SpliceTime.PTSTime != nil {
			pts := int64(*cmd.Program.SpliceTime.PTSTime)
			msg.SpliceTimePTS = &pts
		}

	case *scte35lib.TimeSignal:
		msg.CommandType = CommandTimeSignal
		if cmd.SpliceTime.PTSTime != nil {
			msg.Timing = "scheduled"
		} else {
			msg.Timing = "immediate"
		}

		// Extract splice time PTS if specified.
		if cmd.SpliceTime.PTSTime != nil {
			pts := int64(*cmd.SpliceTime.PTSTime)
			msg.SpliceTimePTS = &pts
		}

	default:
		return nil, fmt.Errorf("unsupported splice command type: %T", sis.SpliceCommand)
	}

	// Extract segmentation descriptors.
	for _, sd := range sis.SpliceDescriptors {
		segDesc, ok := sd.(*scte35lib.SegmentationDescriptor)
		if !ok {
			continue
		}
		d := SegmentationDescriptor{
			SegmentationType: uint8(segDesc.SegmentationTypeID),
			SegEventID:       segDesc.SegmentationEventID,
		}
		if segDesc.SegmentationDuration != nil {
			dur := *segDesc.SegmentationDuration
			d.DurationTicks = &dur
		}
		if segDesc.SubSegmentNum != nil {
			d.SubSegmentNum = uint8(*segDesc.SubSegmentNum)
		}
		if segDesc.SubSegmentsExpected != nil {
			d.SubSegmentsExpected = uint8(*segDesc.SubSegmentsExpected)
		}
		// Extract UPID from the first segmentation UPID if present.
		if len(segDesc.SegmentationUPIDs) > 0 {
			upid := segDesc.SegmentationUPIDs[0]
			d.UPIDType = uint8(upid.Type)
			d.UPID = []byte(upid.Value)
		}
		msg.Descriptors = append(msg.Descriptors, d)
	}

	// Extract delivery restrictions from the first segmentation descriptor.
	for _, sd := range sis.SpliceDescriptors {
		segDesc, ok := sd.(*scte35lib.SegmentationDescriptor)
		if !ok {
			continue
		}
		if segDesc.DeliveryRestrictions != nil {
			msg.DeliveryRestrictions = &DeliveryRestrictions{
				WebDeliveryAllowed: segDesc.DeliveryRestrictions.WebDeliveryAllowedFlag,
				NoRegionalBlackout: segDesc.DeliveryRestrictions.NoRegionalBlackoutFlag,
				ArchiveAllowed:     segDesc.DeliveryRestrictions.ArchiveAllowedFlag,
				DeviceRestrictions: uint8(segDesc.DeliveryRestrictions.DeviceRestrictions),
			}
			break
		}
	}

	return msg, nil
}
