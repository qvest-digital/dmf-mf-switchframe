// Package scte104 implements SCTE-104 message types, binary encoding/decoding,
// and bidirectional translation to/from SCTE-35 CueMessages.
//
// SCTE-104 is the automation-to-splicer protocol used by broadcast automation
// systems to trigger ad insertion events. Messages are carried over TCP or serial
// connections and translated to SCTE-35 splice commands for MPEG-TS output.
package scte104

import "fmt"

// Operation ID constants per SCTE-104 specification.
const (
	// OpSpliceRequest initiates a splice_insert event.
	OpSpliceRequest uint16 = 0x0101

	// OpSpliceNull is a heartbeat/keepalive operation.
	OpSpliceNull uint16 = 0x0102

	// OpTimeSignalRequest triggers a time_signal command.
	OpTimeSignalRequest uint16 = 0x0104

	// OpSegmentationDescriptorRequest carries segmentation metadata,
	// typically paired with a time_signal.
	OpSegmentationDescriptorRequest uint16 = 0x010B

	// OpMultipleOperationMessage wraps multiple operations in a single message.
	OpMultipleOperationMessage uint16 = 0xFFFF
)

// SpliceInsertType constants for splice_request_data.
const (
	SpliceStartNormal    uint8 = 1
	SpliceStartImmediate uint8 = 2
	SpliceEndNormal      uint8 = 3
	SpliceEndImmediate   uint8 = 4
	SpliceCancel         uint8 = 5
)

// Message represents a decoded SCTE-104 message.
type Message struct {
	// ProtocolVersion is the SCTE-104 protocol version (typically 0).
	ProtocolVersion uint8

	// ASIndex identifies the automation system.
	ASIndex uint8

	// MessageNumber is a sequence number for message tracking.
	MessageNumber uint8

	// DPIPIDIndex identifies the DPI PID.
	DPIPIDIndex uint16

	// Operations contains the list of operations in this message.
	Operations []Operation
}

// Operation represents a single SCTE-104 operation within a message.
type Operation struct {
	// OpID identifies the operation type.
	OpID uint16

	// Data holds the operation-specific payload.
	// It is one of: *SpliceRequestData, *TimeSignalRequestData,
	// *SegmentationDescriptorRequest, or nil (for splice_null).
	Data any
}

// SpliceRequestData carries parameters for a splice_request operation (OpID 0x0101).
type SpliceRequestData struct {
	// SpliceInsertType determines the splice action (start/end/cancel).
	SpliceInsertType uint8

	// SpliceEventID uniquely identifies the splice event.
	SpliceEventID uint32

	// UniqueProgramID identifies the program within the avail.
	UniqueProgramID uint16

	// PreRollTime is the advance warning time in milliseconds.
	PreRollTime uint16

	// BreakDuration is the break duration in tenths of a second (100ms units).
	BreakDuration uint16

	// AvailNum identifies the avail within a group.
	AvailNum uint8

	// AvailsExpected is the total number of avails in the group.
	AvailsExpected uint8

	// AutoReturnFlag indicates automatic return to network programming.
	AutoReturnFlag bool
}

// TimeSignalRequestData carries parameters for a time_signal_request operation (OpID 0x0104).
type TimeSignalRequestData struct {
	// PreRollTime is the advance warning time in milliseconds.
	PreRollTime uint16
}

// SegmentationDescriptorRequest carries segmentation metadata (OpID 0x010B).
//
// Field layout mirrors SCTE 104 Table 8-29 (insert_segmentation_descriptor_request),
// which is byte-aligned and differs from the SCTE-35 segmentation_descriptor()
// bit layout: there is no program_segmentation_flag, no component loop and no
// 40-bit 90 kHz duration in SCTE 104 — duration is a 16-bit SECONDS field.
type SegmentationDescriptorRequest struct {
	// SegEventID uniquely identifies the segmentation event.
	SegEventID uint32

	// SegmentationTypeID identifies the type of segmentation (per SCTE-35 table).
	SegmentationTypeID uint8

	// Duration is the segmentation duration in SECONDS (16-bit wire field).
	// Translation to/from SCTE-35's 90 kHz tick duration happens in translate.go.
	Duration uint16

	// DurationExtensionFrames extends Duration by a number of video frames
	// for sub-second precision.
	DurationExtensionFrames uint8

	// UPIDType identifies the type of UPID.
	UPIDType uint8

	// UPID is the unique program identifier.
	UPID []byte

	// SegNum is the segment number within the segmentation event.
	SegNum uint8

	// SegExpected is the expected number of segments.
	SegExpected uint8

	// CancelIndicator when true cancels the segmentation event.
	// On the wire this is a full byte with value 0 or 1.
	CancelIndicator bool

	// DeliveryNotRestricted when true indicates no delivery restrictions;
	// the four fields below are only meaningful when it is false.
	DeliveryNotRestricted bool

	// WebDeliveryAllowed indicates web delivery is permitted.
	WebDeliveryAllowed bool

	// NoRegionalBlackout indicates no regional blackout applies.
	NoRegionalBlackout bool

	// ArchiveAllowed indicates recording/archiving is permitted.
	ArchiveAllowed bool

	// DeviceRestrictions is the 2-bit SCTE-35 device restrictions value
	// carried as a full byte in SCTE 104.
	DeviceRestrictions uint8
}

// String returns a human-readable description of the operation.
func (o Operation) String() string {
	switch o.OpID {
	case OpSpliceRequest:
		return "splice_request"
	case OpSpliceNull:
		return "splice_null"
	case OpTimeSignalRequest:
		return "time_signal_request"
	case OpSegmentationDescriptorRequest:
		return "segmentation_descriptor_request"
	default:
		return fmt.Sprintf("unknown_op(0x%04X)", o.OpID)
	}
}

// isKnownSingleOpID returns true if the opID is a known single-operation ID
// (not the MOM wrapper).
func isKnownSingleOpID(opID uint16) bool {
	switch opID {
	case OpSpliceRequest, OpSpliceNull, OpTimeSignalRequest, OpSegmentationDescriptorRequest:
		return true
	default:
		return false
	}
}
