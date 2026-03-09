// Package scte35 wraps the Comcast/scte35-go library into Switchframe's
// internal CueMessage representation for SCTE-35 splice signaling.
package scte35

import (
	"encoding/binary"
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

	// SpliceEventCancelIndicator when true cancels the event identified by EventID.
	// Per the SCTE-35 spec, when set, Program/BreakDuration/OutOfNetworkIndicator
	// fields are absent in the encoded message.
	SpliceEventCancelIndicator bool

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

	// UniqueProgramID identifies the program in the avail.
	UniqueProgramID uint16

	// AvailNum identifies the avail within a group.
	AvailNum uint8

	// AvailsExpected is the total number of avails in the group.
	AvailsExpected uint8

	// Source tracks the origin of this cue: "api", "macro", "scte104", "passthrough".
	// Internal only — not serialized to SCTE-35 binary.
	Source string
}

// SegmentationDescriptor carries segmentation metadata for time_signal commands.
type SegmentationDescriptor struct {
	SegmentationType             uint8   `json:"segmentationType"`
	SegEventID                   uint32  `json:"segEventId"`
	SegmentationEventCancelIndicator bool `json:"segmentationEventCancelIndicator,omitempty"`
	DurationTicks                *uint64 `json:"durationTicks,omitempty"`
	UPIDType                     uint8   `json:"upidType"`
	UPID                         []byte  `json:"upid"`
	SegNum                       uint8   `json:"segNum,omitempty"`
	SegExpected                  uint8   `json:"segExpected,omitempty"`
	SubSegmentNum                uint8   `json:"subSegmentNum,omitempty"`
	SubSegmentsExpected          uint8   `json:"subSegmentsExpected,omitempty"`
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
//
// Note: verification is skipped for splice_insert cancel messages due to a
// decoder bug in Comcast/scte35-go where unique_program_id/avail_num/
// avails_expected are read outside the cancel indicator guard.
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
			SpliceEventID:              m.EventID,
			SpliceEventCancelIndicator: m.SpliceEventCancelIndicator,
		}
		// Per SCTE-35 spec, when cancel indicator is set, Program/BreakDuration/
		// OutOfNetworkIndicator fields are absent.
		if !m.SpliceEventCancelIndicator {
			si.OutOfNetworkIndicator = m.IsOut
			if m.SpliceTimePTS != nil {
				// Scheduled: use PTS-based splice time.
				si.SpliceImmediateFlag = false
				pts := uint64(*m.SpliceTimePTS)
				si.Program = &scte35lib.SpliceInsertProgram{
					SpliceTime: scte35lib.SpliceTime{
						PTSTime: &pts,
					},
				}
			} else {
				// Immediate: no splice time.
				si.SpliceImmediateFlag = true
				si.Program = &scte35lib.SpliceInsertProgram{}
			}
			if m.BreakDuration != nil {
				ticks := scte35lib.DurationToTicks(*m.BreakDuration)
				si.BreakDuration = &scte35lib.BreakDuration{
					AutoReturn: m.AutoReturn,
					Duration:   ticks,
				}
			}
			si.UniqueProgramID = uint32(m.UniqueProgramID)
			si.AvailNum = uint32(m.AvailNum)
			si.AvailsExpected = uint32(m.AvailsExpected)
		}
		// Skip verification for cancel messages (library decoder bug).
		if m.SpliceEventCancelIndicator {
			verify = false
		}
		sis.SpliceCommand = si

	case CommandTimeSignal:
		ts := &scte35lib.TimeSignal{}
		if m.SpliceTimePTS != nil {
			pts := uint64(*m.SpliceTimePTS)
			ts.SpliceTime = scte35lib.SpliceTime{PTSTime: &pts}
		}
		// When SpliceTimePTS is nil, PTSTime stays nil → time_specified_flag=0
		// (avoids encoding PTS=0 with time_specified_flag=1).
		sis.SpliceCommand = ts

		for _, d := range m.Descriptors {
			sd := &scte35lib.SegmentationDescriptor{
				SegmentationEventID:              d.SegEventID,
				SegmentationEventCancelIndicator: d.SegmentationEventCancelIndicator,
			}
			// Per SCTE-35 spec, when cancel indicator is set,
			// SegmentationDuration, UPIDs, and SegmentationTypeID are absent.
			if !d.SegmentationEventCancelIndicator {
				sd.SegmentationTypeID = uint32(d.SegmentationType)
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
				sd.SegmentNum = uint32(d.SegNum)
				sd.SegmentsExpected = uint32(d.SegExpected)
				if d.SubSegmentNum > 0 || d.SubSegmentsExpected > 0 {
					sn := uint32(d.SubSegmentNum)
					se := uint32(d.SubSegmentsExpected)
					sd.SubSegmentNum = &sn
					sd.SubSegmentsExpected = &se
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
//
// Note: splice_insert cancel messages are decoded with a fallback parser
// because Comcast/scte35-go reads unique_program_id/avail_num/avails_expected
// outside the cancel indicator guard, causing buffer overflow on spec-compliant
// cancel messages.
func Decode(data []byte) (*CueMessage, error) {
	sis, err := scte35lib.DecodeBytes(data)
	if err != nil {
		// Check if this is a splice_insert cancel message that the library
		// fails to decode due to its decoder bug.
		if msg, fallbackErr := decodeSpliceInsertCancel(data); fallbackErr == nil {
			return msg, nil
		}
		return nil, fmt.Errorf("scte35 decode: %w", err)
	}

	msg := &CueMessage{}

	switch cmd := sis.SpliceCommand.(type) {
	case *scte35lib.SpliceNull:
		msg.CommandType = CommandSpliceNull

	case *scte35lib.SpliceInsert:
		msg.CommandType = CommandSpliceInsert
		msg.EventID = cmd.SpliceEventID
		msg.SpliceEventCancelIndicator = cmd.SpliceEventCancelIndicator

		// Per SCTE-35 spec, when cancel indicator is set, the remaining
		// fields (OutOfNetworkIndicator, BreakDuration, etc.) are absent.
		if !cmd.SpliceEventCancelIndicator {
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

			msg.UniqueProgramID = uint16(cmd.UniqueProgramID)
			msg.AvailNum = uint8(cmd.AvailNum)
			msg.AvailsExpected = uint8(cmd.AvailsExpected)
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
			SegmentationType:             uint8(segDesc.SegmentationTypeID),
			SegEventID:                   segDesc.SegmentationEventID,
			SegmentationEventCancelIndicator: segDesc.SegmentationEventCancelIndicator,
		}
		if segDesc.SegmentationDuration != nil {
			dur := *segDesc.SegmentationDuration
			d.DurationTicks = &dur
		}
		d.SegNum = uint8(segDesc.SegmentNum)
		d.SegExpected = uint8(segDesc.SegmentsExpected)
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

// decodeSpliceInsertCancel attempts to parse an SCTE-35 splice_insert cancel
// message from raw bytes. This is a fallback for the Comcast/scte35-go library
// bug where the decoder reads unique_program_id/avail_num/avails_expected
// outside the splice_event_cancel_indicator guard.
//
// SCTE-35 splice_info_section bit layout:
//
//	bits 0-7:     table_id (0xFC)
//	bits 8-9:     section_syntax_indicator + private_indicator
//	bits 10-11:   SAP type
//	bits 12-23:   section_length
//	bits 24-31:   protocol_version
//	bits 32:      encrypted_packet_flag
//	bits 33-38:   encryption_algorithm
//	bits 39-71:   pts_adjustment
//	bits 72-79:   cw_index
//	bits 80-91:   tier
//	bits 92-103:  splice_command_length
//	bits 104-111: splice_command_type
//	bits 112+:    splice_command data (splice_insert starts here)
//
// splice_insert cancel data:
//
//	bits 112-143: splice_event_id (32 bits)
//	bit 144:      splice_event_cancel_indicator
//	bits 145-151: reserved
func decodeSpliceInsertCancel(data []byte) (*CueMessage, error) {
	// Minimum: 14 header bytes + 5 command bytes (event_id + cancel) +
	// 2 descriptor_loop_length + 4 CRC = 25 bytes
	if len(data) < 25 {
		return nil, fmt.Errorf("too short for splice_insert cancel")
	}

	// Verify table_id.
	if data[0] != 0xFC {
		return nil, fmt.Errorf("not an SCTE-35 section")
	}

	// Verify splice_command_type is splice_insert (0x05) at byte 13.
	if data[13] != CommandSpliceInsert {
		return nil, fmt.Errorf("not a splice_insert command")
	}

	// Read splice_event_id (big-endian 32 bits at offset 14).
	eventID := binary.BigEndian.Uint32(data[14:18])

	// Read splice_event_cancel_indicator (MSB of byte 18).
	cancelIndicator := (data[18] & 0x80) != 0
	if !cancelIndicator {
		return nil, fmt.Errorf("not a cancel message")
	}

	return &CueMessage{
		CommandType:                 CommandSpliceInsert,
		EventID:                    eventID,
		SpliceEventCancelIndicator: true,
	}, nil
}
