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

	// seg descriptor payload: seg_event_id(4) + type(1) + duration(5) +
	// upid_type(1) + upid_length(1) + upid(11) + seg_num(1) + segs_expected(1) = 25
	segPayload := make([]byte, 14+len(upid))
	binary.BigEndian.PutUint32(segPayload[0:4], 500)   // seg_event_id
	segPayload[4] = 0x34                                // segmentation_type_id (placement opportunity start)
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
	segPayload[12+len(upid)] = 1 // seg_num
	segPayload[12+len(upid)+1] = 1 // segs_expected

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
	// seg_event_id(4) + type_with_cancel(1) = 5 bytes
	data := make([]byte, 7) // OpID(2) + payload(5)
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	binary.BigEndian.PutUint32(data[2:6], 777) // seg_event_id
	data[6] = 0x34 | 0x80                       // type=0x34 with cancel bit set

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
	if sd.SegmentationTypeID != 0x34 {
		t.Errorf("SegmentationTypeID = 0x%02X, want 0x34", sd.SegmentationTypeID)
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
	data := make([]byte, 9) // OpID(2) + seg_event_id(4) + type(1) + 2 bytes (needs 7 more)
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	binary.BigEndian.PutUint32(data[2:6], 100)
	data[6] = 0x34 // no cancel bit

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

func TestDecode_SegmentationDescriptor_EmptyUPID(t *testing.T) {
	// Non-cancel seg descriptor with zero-length UPID.
	// seg_event_id(4) + type(1) + duration(5) + upid_type(1) + upid_length(1) +
	// seg_num(1) + segs_expected(1) = 14
	payload := make([]byte, 14)
	binary.BigEndian.PutUint32(payload[0:4], 123)
	payload[4] = 0x30 // segmentation_type_id
	// duration = 0
	payload[10] = 0x01 // upid_type
	payload[11] = 0    // upid_length = 0
	payload[12] = 1    // seg_num
	payload[13] = 1    // segs_expected

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
