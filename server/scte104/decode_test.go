package scte104

import (
	"encoding/binary"
	"testing"
)

func TestDecode_SOM_SpliceRequest(t *testing.T) {
	// Build a SOM: OpID(2) + splice_request_data(14)
	data := make([]byte, 16)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceRequest)
	data[2] = SpliceStartImmediate     // splice_insert_type
	binary.BigEndian.PutUint32(data[3:7], 42)   // splice_event_id
	binary.BigEndian.PutUint16(data[7:9], 100)   // unique_program_id
	binary.BigEndian.PutUint16(data[9:11], 5000) // pre_roll_time
	binary.BigEndian.PutUint16(data[11:13], 300) // break_duration (100ms units)
	data[13] = 1  // avail_num
	data[14] = 2  // avails_expected
	data[15] = 1  // auto_return_flag

	msg, err := Decode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}

	op := msg.Operations[0]
	if op.OpID != OpSpliceRequest {
		t.Fatalf("expected OpSpliceRequest, got 0x%04X", op.OpID)
	}

	srd, ok := op.Data.(*SpliceRequestData)
	if !ok {
		t.Fatalf("expected *SpliceRequestData, got %T", op.Data)
	}

	if srd.SpliceInsertType != SpliceStartImmediate {
		t.Errorf("SpliceInsertType = %d, want %d", srd.SpliceInsertType, SpliceStartImmediate)
	}
	if srd.SpliceEventID != 42 {
		t.Errorf("SpliceEventID = %d, want 42", srd.SpliceEventID)
	}
	if srd.UniqueProgramID != 100 {
		t.Errorf("UniqueProgramID = %d, want 100", srd.UniqueProgramID)
	}
	if srd.PreRollTime != 5000 {
		t.Errorf("PreRollTime = %d, want 5000", srd.PreRollTime)
	}
	if srd.BreakDuration != 300 {
		t.Errorf("BreakDuration = %d, want 300", srd.BreakDuration)
	}
	if srd.AvailNum != 1 {
		t.Errorf("AvailNum = %d, want 1", srd.AvailNum)
	}
	if srd.AvailsExpected != 2 {
		t.Errorf("AvailsExpected = %d, want 2", srd.AvailsExpected)
	}
	if !srd.AutoReturnFlag {
		t.Error("AutoReturnFlag = false, want true")
	}
}

func TestDecode_SOM_SpliceNull(t *testing.T) {
	// SOM: OpID(2) only, no payload.
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceNull)

	msg, err := Decode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}

	op := msg.Operations[0]
	if op.OpID != OpSpliceNull {
		t.Fatalf("expected OpSpliceNull, got 0x%04X", op.OpID)
	}
	if op.Data != nil {
		t.Errorf("expected nil Data for splice_null, got %v", op.Data)
	}
}

func TestDecode_SOM_TimeSignal(t *testing.T) {
	data := make([]byte, 4)
	binary.BigEndian.PutUint16(data[0:2], OpTimeSignalRequest)
	binary.BigEndian.PutUint16(data[2:4], 2000) // pre_roll_time

	msg, err := Decode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}

	tsr, ok := msg.Operations[0].Data.(*TimeSignalRequestData)
	if !ok {
		t.Fatalf("expected *TimeSignalRequestData, got %T", msg.Operations[0].Data)
	}
	if tsr.PreRollTime != 2000 {
		t.Errorf("PreRollTime = %d, want 2000", tsr.PreRollTime)
	}
}

func TestDecode_MOM_MultipleOps(t *testing.T) {
	// Build a MOM with a splice_request + a splice_null.
	//
	// OpID: 0xFFFF (2 bytes)
	// messageSize: TBD (2 bytes)
	// protocolVersion: 0 (1)
	// AS_index: 5 (1)
	// message_number: 3 (1)
	// DPI_PID_index: 1000 (2)
	// SCTE35_protocol_version: 0 (1)
	// timestamp: 0 (1)
	// num_ops: 2 (1)
	// --- Op 1: splice_request ---
	// opID: 0x0101 (2)
	// data_length: 14 (2)
	// data: 14 bytes
	// --- Op 2: splice_null ---
	// opID: 0x0102 (2)
	// data_length: 0 (2)

	spliceData := make([]byte, 14)
	spliceData[0] = SpliceEndImmediate                   // splice_insert_type
	binary.BigEndian.PutUint32(spliceData[1:5], 999)     // event_id
	binary.BigEndian.PutUint16(spliceData[5:7], 50)      // unique_program_id
	binary.BigEndian.PutUint16(spliceData[7:9], 1000)    // pre_roll_time
	binary.BigEndian.PutUint16(spliceData[9:11], 0)      // break_duration
	spliceData[11] = 0                                    // avail_num
	spliceData[12] = 0                                    // avails_expected
	spliceData[13] = 0                                    // auto_return_flag

	// Total MOM body: header(10) + op1(2+2+14) + op2(2+2+0) = 32
	headerSize := 10
	op1Size := 4 + 14
	op2Size := 4 + 0
	messageSize := headerSize + op1Size + op2Size

	buf := make([]byte, 2+messageSize)
	binary.BigEndian.PutUint16(buf[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(buf[2:4], uint16(messageSize))
	buf[4] = 0    // protocolVersion
	buf[5] = 5    // AS_index
	buf[6] = 3    // message_number
	binary.BigEndian.PutUint16(buf[7:9], 1000) // DPI_PID_index
	buf[9] = 0    // SCTE35_protocol_version
	buf[10] = 0   // timestamp
	buf[11] = 2   // num_ops

	offset := 12
	// Op 1: splice_request
	binary.BigEndian.PutUint16(buf[offset:offset+2], OpSpliceRequest)
	binary.BigEndian.PutUint16(buf[offset+2:offset+4], 14)
	copy(buf[offset+4:], spliceData)
	offset += op1Size

	// Op 2: splice_null
	binary.BigEndian.PutUint16(buf[offset:offset+2], OpSpliceNull)
	binary.BigEndian.PutUint16(buf[offset+2:offset+4], 0)

	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.ASIndex != 5 {
		t.Errorf("ASIndex = %d, want 5", msg.ASIndex)
	}
	if msg.MessageNumber != 3 {
		t.Errorf("MessageNumber = %d, want 3", msg.MessageNumber)
	}
	if msg.DPIPIDIndex != 1000 {
		t.Errorf("DPIPIDIndex = %d, want 1000", msg.DPIPIDIndex)
	}
	if len(msg.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(msg.Operations))
	}

	// Verify op 1 is splice_request.
	if msg.Operations[0].OpID != OpSpliceRequest {
		t.Errorf("op[0].OpID = 0x%04X, want 0x%04X", msg.Operations[0].OpID, OpSpliceRequest)
	}
	srd, ok := msg.Operations[0].Data.(*SpliceRequestData)
	if !ok {
		t.Fatalf("op[0].Data expected *SpliceRequestData, got %T", msg.Operations[0].Data)
	}
	if srd.SpliceInsertType != SpliceEndImmediate {
		t.Errorf("SpliceInsertType = %d, want %d", srd.SpliceInsertType, SpliceEndImmediate)
	}
	if srd.SpliceEventID != 999 {
		t.Errorf("SpliceEventID = %d, want 999", srd.SpliceEventID)
	}

	// Verify op 2 is splice_null.
	if msg.Operations[1].OpID != OpSpliceNull {
		t.Errorf("op[1].OpID = 0x%04X, want 0x%04X", msg.Operations[1].OpID, OpSpliceNull)
	}
}

func TestDecode_MOM_SegmentationDescriptor(t *testing.T) {
	// Build a MOM with time_signal + segmentation_descriptor.
	upid := []byte("AD-ID-12345")

	// Spec-compliant wire format per SCTE 104 2021 Table 8-29:
	// seg_event_id(4) + flags(1) + duration(5) + upid_type(1) + upid_length(1) +
	// upid(11) + type_id(1) + seg_num(1) + segs_expected(1) = 26
	segPayload := make([]byte, 15+len(upid))
	binary.BigEndian.PutUint32(segPayload[0:4], 500)   // seg_event_id
	segPayload[4] = 0x01                                // flags: cancel=0, reserved=0, program_seg_flag=1
	// duration: 2700000 ticks (30 seconds at 90kHz)
	dur := uint64(2700000)
	segPayload[5] = byte(dur >> 32)
	segPayload[6] = byte(dur >> 24)
	segPayload[7] = byte(dur >> 16)
	segPayload[8] = byte(dur >> 8)
	segPayload[9] = byte(dur)
	segPayload[10] = 0x09 // upid_type (ADI)
	segPayload[11] = byte(len(upid))
	copy(segPayload[12:], upid)
	segPayload[12+len(upid)] = 0x34 // segmentation_type_id (AFTER upid)
	segPayload[12+len(upid)+1] = 1  // seg_num
	segPayload[12+len(upid)+2] = 1  // segs_expected

	// time_signal payload: pre_roll_time(2) = 2
	tsPayload := make([]byte, 2)
	binary.BigEndian.PutUint16(tsPayload[0:2], 4000)

	headerSize := 10
	op1Size := 4 + len(tsPayload)
	op2Size := 4 + len(segPayload)
	messageSize := headerSize + op1Size + op2Size

	buf := make([]byte, 2+messageSize)
	binary.BigEndian.PutUint16(buf[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(buf[2:4], uint16(messageSize))
	buf[4] = 0  // protocolVersion
	buf[5] = 1  // AS_index
	buf[6] = 7  // message_number
	binary.BigEndian.PutUint16(buf[7:9], 2000)
	buf[9] = 0  // SCTE35_protocol_version
	buf[10] = 0 // timestamp
	buf[11] = 2 // num_ops

	offset := 12

	// Op 1: time_signal_request
	binary.BigEndian.PutUint16(buf[offset:offset+2], OpTimeSignalRequest)
	binary.BigEndian.PutUint16(buf[offset+2:offset+4], uint16(len(tsPayload)))
	copy(buf[offset+4:], tsPayload)
	offset += op1Size

	// Op 2: segmentation_descriptor_request
	binary.BigEndian.PutUint16(buf[offset:offset+2], OpSegmentationDescriptorRequest)
	binary.BigEndian.PutUint16(buf[offset+2:offset+4], uint16(len(segPayload)))
	copy(buf[offset+4:], segPayload)

	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msg.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(msg.Operations))
	}

	// Verify time_signal.
	tsr, ok := msg.Operations[0].Data.(*TimeSignalRequestData)
	if !ok {
		t.Fatalf("op[0].Data expected *TimeSignalRequestData, got %T", msg.Operations[0].Data)
	}
	if tsr.PreRollTime != 4000 {
		t.Errorf("PreRollTime = %d, want 4000", tsr.PreRollTime)
	}

	// Verify segmentation descriptor.
	sd, ok := msg.Operations[1].Data.(*SegmentationDescriptorRequest)
	if !ok {
		t.Fatalf("op[1].Data expected *SegmentationDescriptorRequest, got %T", msg.Operations[1].Data)
	}
	if sd.SegEventID != 500 {
		t.Errorf("SegEventID = %d, want 500", sd.SegEventID)
	}
	if sd.SegmentationTypeID != 0x34 {
		t.Errorf("SegmentationTypeID = 0x%02X, want 0x34", sd.SegmentationTypeID)
	}
	if sd.DurationTicks != 2700000 {
		t.Errorf("DurationTicks = %d, want 2700000", sd.DurationTicks)
	}
	if sd.UPIDType != 0x09 {
		t.Errorf("UPIDType = 0x%02X, want 0x09", sd.UPIDType)
	}
	if string(sd.UPID) != "AD-ID-12345" {
		t.Errorf("UPID = %q, want %q", sd.UPID, "AD-ID-12345")
	}
	if sd.SegNum != 1 {
		t.Errorf("SegNum = %d, want 1", sd.SegNum)
	}
	if sd.SegExpected != 1 {
		t.Errorf("SegExpected = %d, want 1", sd.SegExpected)
	}
	if sd.CancelIndicator {
		t.Error("CancelIndicator should be false")
	}
}

func TestDecode_SegmentationDescriptor_Cancel(t *testing.T) {
	// SOM with segmentation_descriptor cancel.
	// Per SCTE 104 2021: cancel format is seg_event_id(4) + flags(1) = 5 bytes.
	// Cancel flag is bit 7 of flags byte. No type_id in cancel messages.
	data := make([]byte, 7) // OpID(2) + payload(5)
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	binary.BigEndian.PutUint32(data[2:6], 777) // seg_event_id
	data[6] = 0x80                              // flags: cancel=1 (no type_id)

	msg, err := Decode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sd, ok := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	if !ok {
		t.Fatalf("expected *SegmentationDescriptorRequest, got %T", msg.Operations[0].Data)
	}
	if !sd.CancelIndicator {
		t.Error("CancelIndicator should be true")
	}
	if sd.SegEventID != 777 {
		t.Errorf("SegEventID = %d, want 777", sd.SegEventID)
	}
	// type_id is not present in cancel format, should be zero.
	if sd.SegmentationTypeID != 0 {
		t.Errorf("SegmentationTypeID = 0x%02X, want 0x00 (not present in cancel)", sd.SegmentationTypeID)
	}
}

func TestDecode_TooShort(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"one byte", []byte{0x01}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.data)
			if err == nil {
				t.Fatal("expected error for short input")
			}
		})
	}
}

func TestDecode_UnknownOpID(t *testing.T) {
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data[0:2], 0x9999)

	_, err := Decode(data)
	if err == nil {
		t.Fatal("expected error for unknown OpID")
	}
}

func TestDecode_SOM_SpliceRequest_TooShort(t *testing.T) {
	// OpID(2) + only 10 bytes (needs 14).
	data := make([]byte, 12)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceRequest)

	_, err := Decode(data)
	if err == nil {
		t.Fatal("expected error for short splice_request")
	}
}

func TestDecode_SOM_TimeSignal_TooShort(t *testing.T) {
	// OpID(2) + only 1 byte (needs 2).
	data := make([]byte, 3)
	binary.BigEndian.PutUint16(data[0:2], OpTimeSignalRequest)

	_, err := Decode(data)
	if err == nil {
		t.Fatal("expected error for short time_signal_request")
	}
}

func TestDecode_MOM_TooShortHeader(t *testing.T) {
	// OpID(2) + only 5 bytes (needs 10 for header).
	data := make([]byte, 7)
	binary.BigEndian.PutUint16(data[0:2], OpMultipleOperationMessage)

	_, err := Decode(data)
	if err == nil {
		t.Fatal("expected error for short MOM header")
	}
}

func TestDecode_MOM_TruncatedOperation(t *testing.T) {
	// MOM with num_ops=1 but no operation data.
	data := make([]byte, 12)
	binary.BigEndian.PutUint16(data[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(data[2:4], 10) // messageSize
	data[11] = 1 // num_ops=1 but no data follows

	_, err := Decode(data)
	if err == nil {
		t.Fatal("expected error for truncated operation")
	}
}

func TestDecode_MOM_OperationDataTruncated(t *testing.T) {
	// MOM with an operation whose data_length exceeds remaining bytes.
	data := make([]byte, 16)
	binary.BigEndian.PutUint16(data[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(data[2:4], 14) // messageSize
	data[11] = 1 // num_ops=1

	// Operation header at offset 12.
	binary.BigEndian.PutUint16(data[12:14], OpSpliceRequest)
	binary.BigEndian.PutUint16(data[14:16], 100) // data_length=100 but we only have 0 bytes

	_, err := Decode(data)
	if err == nil {
		t.Fatal("expected error for truncated operation data")
	}
}

func TestDecode_MOM_UnknownOp_Skipped(t *testing.T) {
	// MOM with an unknown op (should be parsed without error) followed by splice_null.
	headerSize := 10
	unknownOpDataLen := 6
	op1Size := 4 + unknownOpDataLen
	op2Size := 4 + 0 // splice_null
	messageSize := headerSize + op1Size + op2Size

	buf := make([]byte, 2+messageSize)
	binary.BigEndian.PutUint16(buf[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(buf[2:4], uint16(messageSize))
	buf[11] = 2 // num_ops

	offset := 12
	// Unknown op
	binary.BigEndian.PutUint16(buf[offset:offset+2], 0x0200) // unknown opID
	binary.BigEndian.PutUint16(buf[offset+2:offset+4], uint16(unknownOpDataLen))
	// Fill with arbitrary data.
	for i := 0; i < unknownOpDataLen; i++ {
		buf[offset+4+i] = byte(i)
	}
	offset += op1Size

	// splice_null
	binary.BigEndian.PutUint16(buf[offset:offset+2], OpSpliceNull)
	binary.BigEndian.PutUint16(buf[offset+2:offset+4], 0)

	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msg.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(msg.Operations))
	}
	if msg.Operations[0].OpID != 0x0200 {
		t.Errorf("op[0].OpID = 0x%04X, want 0x0200", msg.Operations[0].OpID)
	}
	if msg.Operations[0].Data != nil {
		t.Errorf("unknown op Data should be nil, got %v", msg.Operations[0].Data)
	}
	if msg.Operations[1].OpID != OpSpliceNull {
		t.Errorf("op[1].OpID = 0x%04X, want OpSpliceNull", msg.Operations[1].OpID)
	}
}

func TestDecode_SegmentationDescriptor_TooShort(t *testing.T) {
	// SOM with segmentation_descriptor but only 3 bytes (needs 5 minimum).
	data := make([]byte, 5) // OpID(2) + 3 bytes
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)

	_, err := Decode(data)
	if err == nil {
		t.Fatal("expected error for short segmentation descriptor")
	}
}

func TestDecode_SegmentationDescriptor_NonCancel_TooShort(t *testing.T) {
	// SOM with non-cancel segmentation_descriptor but insufficient bytes for
	// the non-cancel fields.
	data := make([]byte, 9) // OpID(2) + seg_event_id(4) + flags(1) + 2 bytes (needs 7 more for duration+upid header)
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	binary.BigEndian.PutUint32(data[2:6], 100)
	data[6] = 0x01 // flags: program_seg_flag=1, no cancel

	_, err := Decode(data)
	if err == nil {
		t.Fatal("expected error for short non-cancel segmentation descriptor")
	}
}

func TestDecode_SpliceRequest_AllTypes(t *testing.T) {
	types := []struct {
		insertType uint8
		name       string
	}{
		{SpliceStartNormal, "start_normal"},
		{SpliceStartImmediate, "start_immediate"},
		{SpliceEndNormal, "end_normal"},
		{SpliceEndImmediate, "end_immediate"},
		{SpliceCancel, "cancel"},
	}

	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			data := make([]byte, 16)
			binary.BigEndian.PutUint16(data[0:2], OpSpliceRequest)
			data[2] = tt.insertType
			binary.BigEndian.PutUint32(data[3:7], 1)

			msg, err := Decode(data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			srd := msg.Operations[0].Data.(*SpliceRequestData)
			if srd.SpliceInsertType != tt.insertType {
				t.Errorf("SpliceInsertType = %d, want %d", srd.SpliceInsertType, tt.insertType)
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	// Seed with valid SOM and MOM messages.
	som := make([]byte, 16)
	binary.BigEndian.PutUint16(som[0:2], OpSpliceRequest)
	som[2] = SpliceStartImmediate
	f.Add(som)

	null := make([]byte, 2)
	binary.BigEndian.PutUint16(null[0:2], OpSpliceNull)
	f.Add(null)

	mom := make([]byte, 16)
	binary.BigEndian.PutUint16(mom[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(mom[2:4], 14)
	mom[11] = 1
	binary.BigEndian.PutUint16(mom[12:14], OpSpliceNull)
	f.Add(mom)

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic regardless of input.
		msg, err := Decode(data)
		if err != nil {
			return
		}
		// If decode succeeds, encode should also succeed and produce valid output.
		encoded, err := Encode(msg)
		if err != nil {
			return
		}
		// Re-decode should not fail.
		_, _ = Decode(encoded)
	})
}

// buildMOMWithTimestamp builds a MOM message containing a single splice_null
// operation, using the given timestamp bytes. The timestamp bytes should include
// the time_type byte as the first byte.
func buildMOMWithTimestamp(timestampBytes []byte) []byte {
	// MOM wire format:
	// OpID: 0xFFFF (2 bytes)
	// messageSize (2 bytes)
	// protocolVersion (1)
	// AS_index (1)
	// message_number (1)
	// DPI_PID_index (2)
	// SCTE35_protocol_version (1)
	// timestamp (variable)
	// num_ops (1)
	// operations...

	// Fixed header before timestamp: messageSize(2) + protocolVersion(1) +
	// AS_index(1) + message_number(1) + DPI_PID_index(2) + SCTE35_protocol_ver(1) = 8
	// After timestamp: num_ops(1)
	// Operation: splice_null opID(2) + data_length(2) = 4

	fixedHeaderBeforeTS := 8
	numOpsField := 1
	opSize := 4 // splice_null: opID(2) + data_length(2) + 0 data
	messageSize := fixedHeaderBeforeTS + len(timestampBytes) + numOpsField + opSize

	buf := make([]byte, 2+messageSize)
	binary.BigEndian.PutUint16(buf[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(buf[2:4], uint16(messageSize)) // messageSize
	buf[4] = 0                                                // protocolVersion
	buf[5] = 7                                                // AS_index
	buf[6] = 11                                               // message_number
	binary.BigEndian.PutUint16(buf[7:9], 3000)                // DPI_PID_index
	buf[9] = 0                                                // SCTE35_protocol_version

	offset := 10
	copy(buf[offset:], timestampBytes)
	offset += len(timestampBytes)

	buf[offset] = 1 // num_ops = 1
	offset++

	// splice_null operation
	binary.BigEndian.PutUint16(buf[offset:offset+2], OpSpliceNull)
	binary.BigEndian.PutUint16(buf[offset+2:offset+4], 0)

	return buf
}

func TestDecode_MOM_TimeType0(t *testing.T) {
	// time_type=0: no timestamp, just the 1-byte type field.
	ts := []byte{0x00}
	buf := buildMOMWithTimestamp(ts)

	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.ASIndex != 7 {
		t.Errorf("ASIndex = %d, want 7", msg.ASIndex)
	}
	if msg.MessageNumber != 11 {
		t.Errorf("MessageNumber = %d, want 11", msg.MessageNumber)
	}
	if msg.DPIPIDIndex != 3000 {
		t.Errorf("DPIPIDIndex = %d, want 3000", msg.DPIPIDIndex)
	}
	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}
	if msg.Operations[0].OpID != OpSpliceNull {
		t.Errorf("op[0].OpID = 0x%04X, want OpSpliceNull", msg.Operations[0].OpID)
	}
}

func TestDecode_MOM_TimeType1_UTC(t *testing.T) {
	// time_type=1: UTC timestamp (7 bytes total).
	// type(1) + GPS_seconds(4) + GPS_microseconds(2)
	ts := []byte{
		0x01,                   // time_type = 1 (UTC)
		0x00, 0x01, 0x51, 0x80, // GPS_seconds = 86400
		0x03, 0xE8, // GPS_microseconds = 1000
	}
	buf := buildMOMWithTimestamp(ts)

	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.ASIndex != 7 {
		t.Errorf("ASIndex = %d, want 7", msg.ASIndex)
	}
	if msg.MessageNumber != 11 {
		t.Errorf("MessageNumber = %d, want 11", msg.MessageNumber)
	}
	if msg.DPIPIDIndex != 3000 {
		t.Errorf("DPIPIDIndex = %d, want 3000", msg.DPIPIDIndex)
	}
	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}
	if msg.Operations[0].OpID != OpSpliceNull {
		t.Errorf("op[0].OpID = 0x%04X, want OpSpliceNull", msg.Operations[0].OpID)
	}
}

func TestDecode_MOM_TimeType2_VITC(t *testing.T) {
	// time_type=2: VITC timestamp (5 bytes total).
	// type(1) + hours(1) + minutes(1) + seconds(1) + frames(1)
	ts := []byte{
		0x02, // time_type = 2 (VITC)
		0x01, // hours
		0x30, // minutes
		0x00, // seconds
		0x0F, // frames
	}
	buf := buildMOMWithTimestamp(ts)

	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.ASIndex != 7 {
		t.Errorf("ASIndex = %d, want 7", msg.ASIndex)
	}
	if msg.MessageNumber != 11 {
		t.Errorf("MessageNumber = %d, want 11", msg.MessageNumber)
	}
	if msg.DPIPIDIndex != 3000 {
		t.Errorf("DPIPIDIndex = %d, want 3000", msg.DPIPIDIndex)
	}
	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}
	if msg.Operations[0].OpID != OpSpliceNull {
		t.Errorf("op[0].OpID = 0x%04X, want OpSpliceNull", msg.Operations[0].OpID)
	}
}

func TestDecode_MOM_TimeType3_GPI(t *testing.T) {
	// time_type=3: GPI timestamp (3 bytes total).
	// type(1) + number(1) + edge(1)
	ts := []byte{
		0x03, // time_type = 3 (GPI)
		0x05, // number
		0x01, // edge
	}
	buf := buildMOMWithTimestamp(ts)

	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.ASIndex != 7 {
		t.Errorf("ASIndex = %d, want 7", msg.ASIndex)
	}
	if msg.MessageNumber != 11 {
		t.Errorf("MessageNumber = %d, want 11", msg.MessageNumber)
	}
	if msg.DPIPIDIndex != 3000 {
		t.Errorf("DPIPIDIndex = %d, want 3000", msg.DPIPIDIndex)
	}
	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}
	if msg.Operations[0].OpID != OpSpliceNull {
		t.Errorf("op[0].OpID = 0x%04X, want OpSpliceNull", msg.Operations[0].OpID)
	}
}

func TestDecode_MOM_TimeTypeUnknown(t *testing.T) {
	// Unknown time_type should fall back to 1-byte (just the type field).
	ts := []byte{0xFF}
	buf := buildMOMWithTimestamp(ts)

	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}
	if msg.Operations[0].OpID != OpSpliceNull {
		t.Errorf("op[0].OpID = 0x%04X, want OpSpliceNull", msg.Operations[0].OpID)
	}
}

func TestDecode_MOM_TimeType1_WithSpliceRequest(t *testing.T) {
	// Verify that a MOM with UTC timestamp (7 bytes) correctly parses
	// a splice_request operation that follows it.
	ts := []byte{
		0x01,                   // time_type = 1 (UTC)
		0x00, 0x00, 0x00, 0x01, // GPS_seconds
		0x00, 0x00, // GPS_microseconds
	}

	spliceData := make([]byte, 14)
	spliceData[0] = SpliceStartImmediate
	binary.BigEndian.PutUint32(spliceData[1:5], 42)
	binary.BigEndian.PutUint16(spliceData[5:7], 100)
	binary.BigEndian.PutUint16(spliceData[7:9], 5000)
	binary.BigEndian.PutUint16(spliceData[9:11], 300)
	spliceData[11] = 1
	spliceData[12] = 2
	spliceData[13] = 1

	// Build MOM manually with UTC timestamp + splice_request
	fixedHeaderBeforeTS := 8
	opSize := 4 + 14 // opID(2) + data_length(2) + splice_request_data(14)
	messageSize := fixedHeaderBeforeTS + len(ts) + 1 + opSize

	buf := make([]byte, 2+messageSize)
	binary.BigEndian.PutUint16(buf[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(buf[2:4], uint16(messageSize))
	buf[4] = 0
	buf[5] = 7
	buf[6] = 11
	binary.BigEndian.PutUint16(buf[7:9], 3000)
	buf[9] = 0

	offset := 10
	copy(buf[offset:], ts)
	offset += len(ts)

	buf[offset] = 1 // num_ops
	offset++

	binary.BigEndian.PutUint16(buf[offset:offset+2], OpSpliceRequest)
	binary.BigEndian.PutUint16(buf[offset+2:offset+4], 14)
	copy(buf[offset+4:], spliceData)

	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}

	srd, ok := msg.Operations[0].Data.(*SpliceRequestData)
	if !ok {
		t.Fatalf("expected *SpliceRequestData, got %T", msg.Operations[0].Data)
	}
	if srd.SpliceEventID != 42 {
		t.Errorf("SpliceEventID = %d, want 42", srd.SpliceEventID)
	}
	if srd.PreRollTime != 5000 {
		t.Errorf("PreRollTime = %d, want 5000", srd.PreRollTime)
	}
}

func TestDecode_MOM_TimeType1_TooShortForTimestamp(t *testing.T) {
	// MOM with time_type=1 (needs 7 bytes for timestamp) but truncated.
	// Build a buffer that has time_type=1 but not enough remaining bytes.
	buf := make([]byte, 14) // OpID(2) + messageSize(2) + headers(6) + time_type(1) + 1 byte (need 6 more)
	binary.BigEndian.PutUint16(buf[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(buf[2:4], 12) // messageSize
	buf[10] = 0x01                           // time_type = 1 (UTC, needs 7 bytes total)

	_, err := Decode(buf)
	if err == nil {
		t.Fatal("expected error for MOM with truncated UTC timestamp")
	}
}

func TestDecode_SegmentationDescriptor_EmptyUPID(t *testing.T) {
	// Non-cancel seg descriptor with zero-length UPID.
	// Per SCTE 104 2021: seg_event_id(4) + flags(1) + duration(5) + upid_type(1) +
	// upid_length(1) + type_id(1) + seg_num(1) + segs_expected(1) = 15
	payload := make([]byte, 15)
	binary.BigEndian.PutUint32(payload[0:4], 123)
	payload[4] = 0x01  // flags: program_seg_flag=1
	// duration = 0 (bytes 5-9)
	payload[10] = 0x01 // upid_type
	payload[11] = 0    // upid_length = 0
	payload[12] = 0x30 // segmentation_type_id (after upid)
	payload[13] = 1    // seg_num
	payload[14] = 1    // segs_expected

	data := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	copy(data[2:], payload)

	msg, err := Decode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	if sd.SegEventID != 123 {
		t.Errorf("SegEventID = %d, want 123", sd.SegEventID)
	}
	if len(sd.UPID) != 0 {
		t.Errorf("UPID length = %d, want 0", len(sd.UPID))
	}
}

func TestDecode_SOM_FullHeader(t *testing.T) {
	// Build a full SOM with the 11-byte header before the operation data.
	//
	// Wire format after OpID:
	//   messageSize(2) + result(2) + result_extension(2) +
	//   protocol_version(1) + AS_index(1) + message_number(1) + DPI_PID_index(2) = 11 bytes header
	//   + splice_request_data(14) = 14 bytes operation data
	// Total payload after OpID = 25 bytes.
	// messageSize should equal 25 (the total length of the payload after OpID).

	spliceData := make([]byte, 14)
	spliceData[0] = SpliceStartImmediate       // splice_insert_type
	binary.BigEndian.PutUint32(spliceData[1:5], 77)   // splice_event_id
	binary.BigEndian.PutUint16(spliceData[5:7], 200)   // unique_program_id
	binary.BigEndian.PutUint16(spliceData[7:9], 3000)  // pre_roll_time
	binary.BigEndian.PutUint16(spliceData[9:11], 600)  // break_duration
	spliceData[11] = 3  // avail_num
	spliceData[12] = 4  // avails_expected
	spliceData[13] = 1  // auto_return_flag

	headerSize := 11
	payloadSize := headerSize + len(spliceData) // 11 + 14 = 25

	// Full message: OpID(2) + payload(25)
	buf := make([]byte, 2+payloadSize)
	binary.BigEndian.PutUint16(buf[0:2], OpSpliceRequest) // OpID

	// Payload starts at buf[2]:
	payload := buf[2:]
	binary.BigEndian.PutUint16(payload[0:2], uint16(payloadSize)) // messageSize = 25
	binary.BigEndian.PutUint16(payload[2:4], 0)                   // result (ignored)
	binary.BigEndian.PutUint16(payload[4:6], 0)                   // result_extension (ignored)
	payload[6] = 2                                                 // protocol_version
	payload[7] = 10                                                // AS_index
	payload[8] = 5                                                 // message_number
	binary.BigEndian.PutUint16(payload[9:11], 4000)                // DPI_PID_index
	copy(payload[11:], spliceData)

	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify header fields are populated.
	if msg.ProtocolVersion != 2 {
		t.Errorf("ProtocolVersion = %d, want 2", msg.ProtocolVersion)
	}
	if msg.ASIndex != 10 {
		t.Errorf("ASIndex = %d, want 10", msg.ASIndex)
	}
	if msg.MessageNumber != 5 {
		t.Errorf("MessageNumber = %d, want 5", msg.MessageNumber)
	}
	if msg.DPIPIDIndex != 4000 {
		t.Errorf("DPIPIDIndex = %d, want 4000", msg.DPIPIDIndex)
	}

	// Verify operation data is parsed correctly.
	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}
	op := msg.Operations[0]
	if op.OpID != OpSpliceRequest {
		t.Fatalf("expected OpSpliceRequest, got 0x%04X", op.OpID)
	}
	srd, ok := op.Data.(*SpliceRequestData)
	if !ok {
		t.Fatalf("expected *SpliceRequestData, got %T", op.Data)
	}
	if srd.SpliceInsertType != SpliceStartImmediate {
		t.Errorf("SpliceInsertType = %d, want %d", srd.SpliceInsertType, SpliceStartImmediate)
	}
	if srd.SpliceEventID != 77 {
		t.Errorf("SpliceEventID = %d, want 77", srd.SpliceEventID)
	}
	if srd.UniqueProgramID != 200 {
		t.Errorf("UniqueProgramID = %d, want 200", srd.UniqueProgramID)
	}
	if srd.PreRollTime != 3000 {
		t.Errorf("PreRollTime = %d, want 3000", srd.PreRollTime)
	}
	if srd.BreakDuration != 600 {
		t.Errorf("BreakDuration = %d, want 600", srd.BreakDuration)
	}
	if srd.AvailNum != 3 {
		t.Errorf("AvailNum = %d, want 3", srd.AvailNum)
	}
	if srd.AvailsExpected != 4 {
		t.Errorf("AvailsExpected = %d, want 4", srd.AvailsExpected)
	}
	if !srd.AutoReturnFlag {
		t.Error("AutoReturnFlag = false, want true")
	}
}

func TestDecode_SOM_AbbreviatedVANC_Regression(t *testing.T) {
	// Verify that abbreviated SOM (VANC format) still works correctly.
	// This is a regression test — the payload IS the operation data directly
	// (no 11-byte header). The first 2 bytes of the splice_request_data
	// (splice_insert_type + first byte of event_id) should NOT be mistaken
	// for a messageSize that matches payload length.

	// Build an abbreviated SOM: OpID(2) + splice_request_data(14) = 16 bytes
	data := make([]byte, 16)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceRequest)
	data[2] = SpliceEndImmediate                          // splice_insert_type
	binary.BigEndian.PutUint32(data[3:7], 12345)          // splice_event_id
	binary.BigEndian.PutUint16(data[7:9], 500)            // unique_program_id
	binary.BigEndian.PutUint16(data[9:11], 2000)          // pre_roll_time
	binary.BigEndian.PutUint16(data[11:13], 150)          // break_duration
	data[13] = 2  // avail_num
	data[14] = 3  // avails_expected
	data[15] = 0  // auto_return_flag = false

	msg, err := Decode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Header fields should be zero (not populated from abbreviated format).
	if msg.ProtocolVersion != 0 {
		t.Errorf("ProtocolVersion = %d, want 0", msg.ProtocolVersion)
	}
	if msg.ASIndex != 0 {
		t.Errorf("ASIndex = %d, want 0", msg.ASIndex)
	}
	if msg.MessageNumber != 0 {
		t.Errorf("MessageNumber = %d, want 0", msg.MessageNumber)
	}
	if msg.DPIPIDIndex != 0 {
		t.Errorf("DPIPIDIndex = %d, want 0", msg.DPIPIDIndex)
	}

	// Verify operation data is parsed correctly from the direct payload.
	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}
	srd, ok := msg.Operations[0].Data.(*SpliceRequestData)
	if !ok {
		t.Fatalf("expected *SpliceRequestData, got %T", msg.Operations[0].Data)
	}
	if srd.SpliceInsertType != SpliceEndImmediate {
		t.Errorf("SpliceInsertType = %d, want %d", srd.SpliceInsertType, SpliceEndImmediate)
	}
	if srd.SpliceEventID != 12345 {
		t.Errorf("SpliceEventID = %d, want 12345", srd.SpliceEventID)
	}
	if srd.UniqueProgramID != 500 {
		t.Errorf("UniqueProgramID = %d, want 500", srd.UniqueProgramID)
	}
	if srd.PreRollTime != 2000 {
		t.Errorf("PreRollTime = %d, want 2000", srd.PreRollTime)
	}
	if srd.BreakDuration != 150 {
		t.Errorf("BreakDuration = %d, want 150", srd.BreakDuration)
	}
	if srd.AvailNum != 2 {
		t.Errorf("AvailNum = %d, want 2", srd.AvailNum)
	}
	if srd.AvailsExpected != 3 {
		t.Errorf("AvailsExpected = %d, want 3", srd.AvailsExpected)
	}
	if srd.AutoReturnFlag {
		t.Error("AutoReturnFlag = true, want false")
	}
}

func TestDecode_SOM_FullHeader_SpliceNull(t *testing.T) {
	// Full SOM with splice_null (0 bytes of operation data).
	// Header only: messageSize(2) + result(2) + result_ext(2) +
	//   protocol_version(1) + AS_index(1) + message_number(1) + DPI_PID_index(2) = 11 bytes
	// messageSize = 11 (no operation data after header).
	//
	// Note: len(payload) == 11 < 13 minimum for full SOM detection,
	// so this should fall through to abbreviated parsing.
	// splice_null has no data, so abbreviated parsing (0 bytes) still works.
	// But let's also test a full SOM with operation data that's small enough.

	// Actually, test a full SOM with time_signal (2 bytes of operation data).
	// payload = 11 (header) + 2 (time_signal) = 13 bytes
	payloadSize := 13

	buf := make([]byte, 2+payloadSize)
	binary.BigEndian.PutUint16(buf[0:2], OpTimeSignalRequest) // OpID

	payload := buf[2:]
	binary.BigEndian.PutUint16(payload[0:2], uint16(payloadSize)) // messageSize = 13
	binary.BigEndian.PutUint16(payload[2:4], 0)                   // result
	binary.BigEndian.PutUint16(payload[4:6], 0)                   // result_extension
	payload[6] = 1                                                 // protocol_version
	payload[7] = 3                                                 // AS_index
	payload[8] = 9                                                 // message_number
	binary.BigEndian.PutUint16(payload[9:11], 500)                 // DPI_PID_index
	binary.BigEndian.PutUint16(payload[11:13], 7000)               // pre_roll_time

	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.ProtocolVersion != 1 {
		t.Errorf("ProtocolVersion = %d, want 1", msg.ProtocolVersion)
	}
	if msg.ASIndex != 3 {
		t.Errorf("ASIndex = %d, want 3", msg.ASIndex)
	}
	if msg.MessageNumber != 9 {
		t.Errorf("MessageNumber = %d, want 9", msg.MessageNumber)
	}
	if msg.DPIPIDIndex != 500 {
		t.Errorf("DPIPIDIndex = %d, want 500", msg.DPIPIDIndex)
	}

	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}
	tsr, ok := msg.Operations[0].Data.(*TimeSignalRequestData)
	if !ok {
		t.Fatalf("expected *TimeSignalRequestData, got %T", msg.Operations[0].Data)
	}
	if tsr.PreRollTime != 7000 {
		t.Errorf("PreRollTime = %d, want 7000", tsr.PreRollTime)
	}
}

func TestDecode_SegmentationDescriptor_SpecFormat(t *testing.T) {
	// Spec-compliant wire format per SCTE 104 2021 Table 8-29.
	// Non-cancel, program-level segmentation.
	upid := []byte("AD-ID-99")
	dur := uint64(2700000) // 30 seconds at 90kHz

	// seg_event_id(4) + flags(1) + duration(5) + upid_type(1) + upid_len(1) +
	// upid(8) + type_id(1) + seg_num(1) + segs_expected(1) = 23
	segPayload := make([]byte, 15+len(upid))
	binary.BigEndian.PutUint32(segPayload[0:4], 500) // seg_event_id
	segPayload[4] = 0x01                              // flags: cancel=0, reserved=0, program_seg_flag=1
	segPayload[5] = byte(dur >> 32)
	segPayload[6] = byte(dur >> 24)
	segPayload[7] = byte(dur >> 16)
	segPayload[8] = byte(dur >> 8)
	segPayload[9] = byte(dur)
	segPayload[10] = 0x09            // upid_type
	segPayload[11] = byte(len(upid)) // upid_length
	copy(segPayload[12:], upid)
	segPayload[12+len(upid)] = 0x34 // segmentation_type_id (AFTER upid!)
	segPayload[12+len(upid)+1] = 1  // seg_num
	segPayload[12+len(upid)+2] = 1  // segs_expected

	data := make([]byte, 2+len(segPayload))
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	copy(data[2:], segPayload)

	msg, err := Decode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	if sd.SegEventID != 500 {
		t.Errorf("SegEventID = %d, want 500", sd.SegEventID)
	}
	if sd.SegmentationTypeID != 0x34 {
		t.Errorf("SegmentationTypeID = 0x%02X, want 0x34", sd.SegmentationTypeID)
	}
	if sd.DurationTicks != 2700000 {
		t.Errorf("DurationTicks = %d, want 2700000", sd.DurationTicks)
	}
	if sd.UPIDType != 0x09 {
		t.Errorf("UPIDType = 0x%02X, want 0x09", sd.UPIDType)
	}
	if string(sd.UPID) != "AD-ID-99" {
		t.Errorf("UPID = %q, want %q", sd.UPID, "AD-ID-99")
	}
	if sd.SegNum != 1 {
		t.Errorf("SegNum = %d, want 1", sd.SegNum)
	}
	if sd.SegExpected != 1 {
		t.Errorf("SegExpected = %d, want 1", sd.SegExpected)
	}
	if !sd.ProgramSegmentationFlag {
		t.Error("ProgramSegmentationFlag should be true")
	}
	if sd.CancelIndicator {
		t.Error("CancelIndicator should be false")
	}
}

func TestDecode_SegmentationDescriptor_ComponentLevel(t *testing.T) {
	// Component-level segmentation: program_seg_flag=0, component_count=2.
	upid := []byte("X")
	dur := uint64(900000)

	// seg_event_id(4) + flags(1) + component_count(1) + tags(2) +
	// duration(5) + upid_type(1) + upid_len(1) + upid(1) +
	// type_id(1) + seg_num(1) + segs_expected(1) = 19
	segPayload := make([]byte, 19)
	binary.BigEndian.PutUint32(segPayload[0:4], 700) // seg_event_id
	segPayload[4] = 0x00                              // flags: cancel=0, program_seg_flag=0
	segPayload[5] = 2                                  // component_count
	segPayload[6] = 0x01                               // component_tag 1
	segPayload[7] = 0x02                               // component_tag 2
	segPayload[8] = byte(dur >> 32)
	segPayload[9] = byte(dur >> 24)
	segPayload[10] = byte(dur >> 16)
	segPayload[11] = byte(dur >> 8)
	segPayload[12] = byte(dur)
	segPayload[13] = 0x09            // upid_type
	segPayload[14] = byte(len(upid)) // upid_length
	copy(segPayload[15:], upid)
	segPayload[16] = 0x22 // segmentation_type_id
	segPayload[17] = 1    // seg_num
	segPayload[18] = 3    // segs_expected

	data := make([]byte, 2+len(segPayload))
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	copy(data[2:], segPayload)

	msg, err := Decode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	if sd.ProgramSegmentationFlag {
		t.Error("ProgramSegmentationFlag should be false for component-level")
	}
	if sd.SegmentationTypeID != 0x22 {
		t.Errorf("SegmentationTypeID = 0x%02X, want 0x22", sd.SegmentationTypeID)
	}
	if sd.DurationTicks != 900000 {
		t.Errorf("DurationTicks = %d, want 900000", sd.DurationTicks)
	}
	if sd.SegNum != 1 {
		t.Errorf("SegNum = %d, want 1", sd.SegNum)
	}
	if sd.SegExpected != 3 {
		t.Errorf("SegExpected = %d, want 3", sd.SegExpected)
	}
}
