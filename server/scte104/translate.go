package scte104

import (
	"fmt"
	"math"
	"time"

	"github.com/zsiec/switchframe/server/scte35"
)

// ToCueMessage translates an SCTE-104 Message into an SCTE-35 CueMessage.
//
// Translation rules:
//   - splice_request (0x0101): Maps to CommandSpliceInsert. SpliceInsertType
//     1,2 -> IsOut=true (cue-out), 3,4 -> IsOut=false (cue-in), 5 -> Cancel.
//     BreakDuration is in 100ms units, converted to time.Duration.
//   - splice_null (0x0102): Maps to CommandSpliceNull.
//   - time_signal (0x0104): Maps to CommandTimeSignal. Segmentation descriptors
//     from any co-located OpSegmentationDescriptorRequest ops are attached.
//   - segmentation_descriptor (0x010B): Attached to a time_signal if present,
//     otherwise creates a standalone time_signal.
func ToCueMessage(msg *Message) (*scte35.CueMessage, error) {
	if msg == nil {
		return nil, fmt.Errorf("scte104: cannot translate nil message")
	}

	if len(msg.Operations) == 0 {
		return nil, fmt.Errorf("scte104: message has no operations")
	}

	// Scan for the primary operation (splice_request, splice_null, or time_signal).
	// Collect any segmentation descriptors separately.
	var primaryOp *Operation
	var segDescs []*SegmentationDescriptorRequest

	for i := range msg.Operations {
		op := &msg.Operations[i]
		switch op.OpID {
		case OpSpliceRequest, OpSpliceNull, OpTimeSignalRequest:
			if primaryOp != nil {
				// Use the first primary operation found.
				continue
			}
			primaryOp = op
		case OpSegmentationDescriptorRequest:
			if sd, ok := op.Data.(*SegmentationDescriptorRequest); ok {
				segDescs = append(segDescs, sd)
			}
		}
	}

	// If no primary op but we have segmentation descriptors, create an
	// implicit time_signal.
	if primaryOp == nil && len(segDescs) > 0 {
		primaryOp = &Operation{OpID: OpTimeSignalRequest}
	}

	if primaryOp == nil {
		return nil, fmt.Errorf("scte104: no translatable operation found")
	}

	switch primaryOp.OpID {
	case OpSpliceRequest:
		return translateSpliceRequest(primaryOp.Data)
	case OpSpliceNull:
		return &scte35.CueMessage{
			CommandType: scte35.CommandSpliceNull,
		}, nil
	case OpTimeSignalRequest:
		return translateTimeSignal(primaryOp.Data, segDescs)
	default:
		return nil, fmt.Errorf("scte104: unsupported primary operation: 0x%04X", primaryOp.OpID)
	}
}

// translateSpliceRequest converts a splice_request operation to a CueMessage.
func translateSpliceRequest(data any) (*scte35.CueMessage, error) {
	srd, ok := data.(*SpliceRequestData)
	if !ok {
		return nil, fmt.Errorf("scte104: splice_request data is %T, expected *SpliceRequestData", data)
	}

	cue := &scte35.CueMessage{
		CommandType:     scte35.CommandSpliceInsert,
		EventID:         srd.SpliceEventID,
		UniqueProgramID: srd.UniqueProgramID,
		AvailNum:        srd.AvailNum,
		AvailsExpected:  srd.AvailsExpected,
		AutoReturn:      srd.AutoReturnFlag,
	}

	switch srd.SpliceInsertType {
	case SpliceStartNormal:
		cue.IsOut = true
		cue.Timing = "scheduled"
	case SpliceStartImmediate:
		cue.IsOut = true
		cue.Timing = "immediate"
	case SpliceEndNormal:
		cue.IsOut = false
		cue.Timing = "scheduled"
	case SpliceEndImmediate:
		cue.IsOut = false
		cue.Timing = "immediate"
	case SpliceCancel:
		cue.SpliceEventCancelIndicator = true
	default:
		return nil, fmt.Errorf("scte104: unknown splice_insert_type: %d", srd.SpliceInsertType)
	}

	// BreakDuration in 100ms units -> time.Duration.
	if srd.BreakDuration > 0 && srd.SpliceInsertType != SpliceCancel {
		dur := time.Duration(srd.BreakDuration) * 100 * time.Millisecond
		cue.BreakDuration = &dur
	}

	return cue, nil
}

// translateTimeSignal converts a time_signal + segmentation descriptors to a CueMessage.
func translateTimeSignal(data any, segDescs []*SegmentationDescriptorRequest) (*scte35.CueMessage, error) {
	cue := &scte35.CueMessage{
		CommandType: scte35.CommandTimeSignal,
		Timing:      "immediate",
	}

	// If we have time_signal data with pre-roll, note it (informational).
	// SCTE-35 time_signal doesn't carry pre-roll directly.
	_ = data // pre-roll is for splicer scheduling, not encoded in SCTE-35

	for _, sd := range segDescs {
		desc := scte35.SegmentationDescriptor{
			SegEventID:                       sd.SegEventID,
			SegmentationType:                 sd.SegmentationTypeID,
			SegmentationEventCancelIndicator: sd.CancelIndicator,
			UPIDType:                         sd.UPIDType,
			SegNum:                           sd.SegNum,
			SegExpected:                      sd.SegExpected,
			SubSegmentNum:                    sd.SubSegmentNum,
			SubSegmentsExpected:              sd.SubSegmentsExpected,
		}

		if len(sd.UPID) > 0 {
			desc.UPID = make([]byte, len(sd.UPID))
			copy(desc.UPID, sd.UPID)
		}

		if sd.DurationTicks > 0 && !sd.CancelIndicator {
			ticks := sd.DurationTicks
			desc.DurationTicks = &ticks
		}

		cue.Descriptors = append(cue.Descriptors, desc)
	}

	return cue, nil
}

// PreRollMs extracts the pre-roll time in milliseconds from the primary
// operation in a decoded SCTE-104 message. Returns 0 for immediate splice
// types (SpliceStartImmediate, SpliceEndImmediate, SpliceCancel), when no
// pre-roll is present, or for nil/empty messages.
func PreRollMs(msg *Message) int64 {
	if msg == nil {
		return 0
	}
	for i := range msg.Operations {
		op := &msg.Operations[i]
		switch op.OpID {
		case OpSpliceRequest:
			srd, ok := op.Data.(*SpliceRequestData)
			if !ok || srd == nil {
				return 0
			}
			// Immediate and cancel types don't use pre-roll.
			switch srd.SpliceInsertType {
			case SpliceStartImmediate, SpliceEndImmediate, SpliceCancel:
				return 0
			}
			return int64(srd.PreRollTime)
		case OpTimeSignalRequest:
			tsr, ok := op.Data.(*TimeSignalRequestData)
			if !ok || tsr == nil {
				return 0
			}
			return int64(tsr.PreRollTime)
		}
	}
	return 0
}

// FromCueMessage translates an SCTE-35 CueMessage into an SCTE-104 Message.
//
// Translation rules:
//   - CommandSpliceNull -> OpSpliceNull
//   - CommandSpliceInsert -> OpSpliceRequest: Timing="scheduled" uses Normal types,
//     Timing="immediate" (or empty) uses Immediate types. IsOut=true -> SpliceStart*,
//     IsOut=false -> SpliceEnd*, Cancel -> SpliceCancel.
//     BreakDuration divided by 100ms for SCTE-104 units.
//   - CommandTimeSignal -> OpTimeSignalRequest + one OpSegmentationDescriptorRequest
//     per descriptor.
func FromCueMessage(cue *scte35.CueMessage) (*Message, error) {
	if cue == nil {
		return nil, fmt.Errorf("scte104: cannot translate nil CueMessage")
	}

	msg := &Message{}

	switch cue.CommandType {
	case scte35.CommandSpliceNull:
		msg.Operations = []Operation{
			{OpID: OpSpliceNull},
		}

	case scte35.CommandSpliceInsert:
		op, err := fromSpliceInsert(cue)
		if err != nil {
			return nil, err
		}
		msg.Operations = []Operation{op}

	case scte35.CommandTimeSignal:
		ops := fromTimeSignal(cue)
		msg.Operations = ops

	default:
		return nil, fmt.Errorf("scte104: unsupported command type: 0x%02X", cue.CommandType)
	}

	return msg, nil
}

// fromSpliceInsert converts a splice_insert CueMessage to a splice_request Operation.
func fromSpliceInsert(cue *scte35.CueMessage) (Operation, error) {
	srd := &SpliceRequestData{
		SpliceEventID:   cue.EventID,
		UniqueProgramID: cue.UniqueProgramID,
		AvailNum:        cue.AvailNum,
		AvailsExpected:  cue.AvailsExpected,
		AutoReturnFlag:  cue.AutoReturn,
	}

	if cue.SpliceEventCancelIndicator {
		srd.SpliceInsertType = SpliceCancel
	} else if cue.IsOut {
		if cue.Timing == "scheduled" {
			srd.SpliceInsertType = SpliceStartNormal
		} else {
			srd.SpliceInsertType = SpliceStartImmediate
		}
	} else {
		if cue.Timing == "scheduled" {
			srd.SpliceInsertType = SpliceEndNormal
		} else {
			srd.SpliceInsertType = SpliceEndImmediate
		}
	}

	// Convert BreakDuration from time.Duration to 100ms units.
	// Use rounding (+50ms) instead of truncation to avoid losing up to 99ms.
	// Clamp to uint16 max (65535) to prevent silent overflow for breaks > 109 minutes.
	if cue.BreakDuration != nil && !cue.SpliceEventCancelIndicator {
		dur100ms := (*cue.BreakDuration + 50*time.Millisecond) / (100 * time.Millisecond)
		if dur100ms > math.MaxUint16 {
			dur100ms = math.MaxUint16
		}
		srd.BreakDuration = uint16(dur100ms)
	}

	return Operation{
		OpID: OpSpliceRequest,
		Data: srd,
	}, nil
}

// fromTimeSignal converts a time_signal CueMessage to operations.
func fromTimeSignal(cue *scte35.CueMessage) []Operation {
	ops := []Operation{
		{
			OpID: OpTimeSignalRequest,
			Data: &TimeSignalRequestData{
				PreRollTime: 0, // No pre-roll in SCTE-35
			},
		},
	}

	for _, desc := range cue.Descriptors {
		sd := &SegmentationDescriptorRequest{
			SegEventID:              desc.SegEventID,
			SegmentationTypeID:      desc.SegmentationType,
			UPIDType:                desc.UPIDType,
			CancelIndicator:         desc.SegmentationEventCancelIndicator,
			SegNum:                  desc.SegNum,
			SegExpected:             desc.SegExpected,
			SubSegmentNum:           desc.SubSegmentNum,
			SubSegmentsExpected:     desc.SubSegmentsExpected,
			ProgramSegmentationFlag: true,
		}

		if len(desc.UPID) > 0 {
			sd.UPID = make([]byte, len(desc.UPID))
			copy(sd.UPID, desc.UPID)
		}

		if desc.DurationTicks != nil {
			sd.DurationTicks = *desc.DurationTicks
		}

		ops = append(ops, Operation{
			OpID: OpSegmentationDescriptorRequest,
			Data: sd,
		})
	}

	return ops
}
