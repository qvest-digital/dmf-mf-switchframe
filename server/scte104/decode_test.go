package scte104

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecode_SOM_SpliceRequest(t *testing.T) {
	t.Parallel()
	// Build a SOM: OpID(2) + splice_request_data(14)
	data := make([]byte, 16)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceRequest)
	data[2] = SpliceStartImmediate                       // splice_insert_type
	binary.BigEndian.PutUint32(data[3:7], 42)            // splice_event_id
	binary.BigEndian.PutUint16(data[7:9], 100)           // unique_program_id
	binary.BigEndian.PutUint16(data[9:11], 5000)         // pre_roll_time
	binary.BigEndian.PutUint16(data[11:13], 300)         // break_duration (100ms units)
	data[13] = 1                                         // avail_num
	data[14] = 2                                         // avails_expected
	data[15] = 0x80                                      // auto_return_flag (bit 7 per SCTE-104 spec)

	msg, err := Decode(data)
	require.NoError(t, err)

	require.Len(t, msg.Operations, 1)

	op := msg.Operations[0]
	require.Equal(t, uint16(OpSpliceRequest), op.OpID)

	require.IsType(t, &SpliceRequestData{}, op.Data)
	srd := op.Data.(*SpliceRequestData)

	require.Equal(t, uint8(SpliceStartImmediate), srd.SpliceInsertType)
	require.Equal(t, uint32(42), srd.SpliceEventID)
	require.Equal(t, uint16(100), srd.UniqueProgramID)
	require.Equal(t, uint16(5000), srd.PreRollTime)
	require.Equal(t, uint16(300), srd.BreakDuration)
	require.Equal(t, uint8(1), srd.AvailNum)
	require.Equal(t, uint8(2), srd.AvailsExpected)
	require.True(t, srd.AutoReturnFlag, "AutoReturnFlag = false, want true")
}

func TestDecode_SOM_SpliceNull(t *testing.T) {
	t.Parallel()
	// SOM: OpID(2) only, no payload.
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceNull)

	msg, err := Decode(data)
	require.NoError(t, err)

	require.Len(t, msg.Operations, 1)

	op := msg.Operations[0]
	require.Equal(t, uint16(OpSpliceNull), op.OpID)
	require.Nil(t, op.Data)
}

func TestDecode_SOM_TimeSignal(t *testing.T) {
	t.Parallel()
	data := make([]byte, 4)
	binary.BigEndian.PutUint16(data[0:2], OpTimeSignalRequest)
	binary.BigEndian.PutUint16(data[2:4], 2000) // pre_roll_time

	msg, err := Decode(data)
	require.NoError(t, err)

	require.Len(t, msg.Operations, 1)

	require.IsType(t, &TimeSignalRequestData{}, msg.Operations[0].Data)
	tsr := msg.Operations[0].Data.(*TimeSignalRequestData)
	require.Equal(t, uint16(2000), tsr.PreRollTime)
}

func TestDecode_MOM_MultipleOps(t *testing.T) {
	t.Parallel()
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
	spliceData[0] = SpliceEndImmediate               // splice_insert_type
	binary.BigEndian.PutUint32(spliceData[1:5], 999) // event_id
	binary.BigEndian.PutUint16(spliceData[5:7], 50)  // unique_program_id
	binary.BigEndian.PutUint16(spliceData[7:9], 1000) // pre_roll_time
	binary.BigEndian.PutUint16(spliceData[9:11], 0)  // break_duration
	spliceData[11] = 0                               // avail_num
	spliceData[12] = 0                               // avails_expected
	spliceData[13] = 0                               // auto_return_flag

	// Total MOM body: header(10) + op1(2+2+14) + op2(2+2+0) = 32
	headerSize := 10
	op1Size := 4 + 14
	op2Size := 4 + 0
	messageSize := headerSize + op1Size + op2Size

	buf := make([]byte, 2+messageSize)
	binary.BigEndian.PutUint16(buf[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(buf[2:4], uint16(messageSize))
	buf[4] = 0                                     // protocolVersion
	buf[5] = 5                                     // AS_index
	buf[6] = 3                                     // message_number
	binary.BigEndian.PutUint16(buf[7:9], 1000)     // DPI_PID_index
	buf[9] = 0                                     // SCTE35_protocol_version
	buf[10] = 0                                    // timestamp
	buf[11] = 2                                    // num_ops

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
	require.NoError(t, err)

	require.Equal(t, uint8(5), msg.ASIndex)
	require.Equal(t, uint8(3), msg.MessageNumber)
	require.Equal(t, uint16(1000), msg.DPIPIDIndex)
	require.Len(t, msg.Operations, 2)

	// Verify op 1 is splice_request.
	require.Equal(t, uint16(OpSpliceRequest), msg.Operations[0].OpID)
	require.IsType(t, &SpliceRequestData{}, msg.Operations[0].Data)
	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, uint8(SpliceEndImmediate), srd.SpliceInsertType)
	require.Equal(t, uint32(999), srd.SpliceEventID)

	// Verify op 2 is splice_null.
	require.Equal(t, uint16(OpSpliceNull), msg.Operations[1].OpID)
}

func TestDecode_MOM_SegmentationDescriptor(t *testing.T) {
	t.Parallel()
	// Build a MOM with time_signal + segmentation_descriptor.
	upid := []byte("AD-ID-12345")

	// Spec-compliant wire format per SCTE 104 2021 Table 8-29:
	// seg_event_id(4) + flags(1) + duration(5) + upid_type(1) + upid_length(1) +
	// upid(11) + type_id(1) + seg_num(1) + segs_expected(1) = 26
	segPayload := make([]byte, 15+len(upid))
	binary.BigEndian.PutUint32(segPayload[0:4], 500) // seg_event_id
	segPayload[4] = 0x01                             // flags: cancel=0, reserved=0, program_seg_flag=1
	// duration: 2700000 ticks (30 seconds at 90kHz)
	dur := uint64(2700000)
	segPayload[5] = byte(dur >> 32)
	segPayload[6] = byte(dur >> 24)
	segPayload[7] = byte(dur >> 16)
	segPayload[8] = byte(dur >> 8)
	segPayload[9] = byte(dur)
	segPayload[10] = 0x09            // upid_type (ADI)
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
	require.NoError(t, err)

	require.Len(t, msg.Operations, 2)

	// Verify time_signal.
	require.IsType(t, &TimeSignalRequestData{}, msg.Operations[0].Data)
	tsr := msg.Operations[0].Data.(*TimeSignalRequestData)
	require.Equal(t, uint16(4000), tsr.PreRollTime)

	// Verify segmentation descriptor.
	require.IsType(t, &SegmentationDescriptorRequest{}, msg.Operations[1].Data)
	sd := msg.Operations[1].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint32(500), sd.SegEventID)
	require.Equal(t, uint8(0x34), sd.SegmentationTypeID)
	require.Equal(t, uint64(2700000), sd.DurationTicks)
	require.Equal(t, uint8(0x09), sd.UPIDType)
	require.Equal(t, "AD-ID-12345", string(sd.UPID))
	require.Equal(t, uint8(1), sd.SegNum)
	require.Equal(t, uint8(1), sd.SegExpected)
	require.False(t, sd.CancelIndicator, "CancelIndicator should be false")
}

func TestDecode_SegmentationDescriptor_Cancel(t *testing.T) {
	t.Parallel()
	// SOM with segmentation_descriptor cancel.
	// Per SCTE 104 2021: cancel format is seg_event_id(4) + flags(1) = 5 bytes.
	// Cancel flag is bit 7 of flags byte. No type_id in cancel messages.
	data := make([]byte, 7) // OpID(2) + payload(5)
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	binary.BigEndian.PutUint32(data[2:6], 777) // seg_event_id
	data[6] = 0x80                             // flags: cancel=1 (no type_id)

	msg, err := Decode(data)
	require.NoError(t, err)

	require.IsType(t, &SegmentationDescriptorRequest{}, msg.Operations[0].Data)
	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.True(t, sd.CancelIndicator, "CancelIndicator should be true")
	require.Equal(t, uint32(777), sd.SegEventID)
	// type_id is not present in cancel format, should be zero.
	require.Equal(t, uint8(0), sd.SegmentationTypeID)
}

func TestDecode_TooShort(t *testing.T) {
	t.Parallel()
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
			require.Error(t, err)
		})
	}
}

func TestDecode_UnknownOpID(t *testing.T) {
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data[0:2], 0x9999)

	_, err := Decode(data)
	require.Error(t, err)
}

func TestDecode_SOM_SpliceRequest_TooShort(t *testing.T) {
	// OpID(2) + only 10 bytes (needs 14).
	data := make([]byte, 12)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceRequest)

	_, err := Decode(data)
	require.Error(t, err)
}

func TestDecode_SOM_TimeSignal_TooShort(t *testing.T) {
	// OpID(2) + only 1 byte (needs 2).
	data := make([]byte, 3)
	binary.BigEndian.PutUint16(data[0:2], OpTimeSignalRequest)

	_, err := Decode(data)
	require.Error(t, err)
}

func TestDecode_MOM_TooShortHeader(t *testing.T) {
	// OpID(2) + only 5 bytes (needs 10 for header).
	data := make([]byte, 7)
	binary.BigEndian.PutUint16(data[0:2], OpMultipleOperationMessage)

	_, err := Decode(data)
	require.Error(t, err)
}

func TestDecode_MOM_TruncatedOperation(t *testing.T) {
	// MOM with num_ops=1 but no operation data.
	data := make([]byte, 12)
	binary.BigEndian.PutUint16(data[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(data[2:4], 10) // messageSize
	data[11] = 1                              // num_ops=1 but no data follows

	_, err := Decode(data)
	require.Error(t, err)
}

func TestDecode_MOM_OperationDataTruncated(t *testing.T) {
	// MOM with an operation whose data_length exceeds remaining bytes.
	data := make([]byte, 16)
	binary.BigEndian.PutUint16(data[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(data[2:4], 14) // messageSize
	data[11] = 1                              // num_ops=1

	// Operation header at offset 12.
	binary.BigEndian.PutUint16(data[12:14], OpSpliceRequest)
	binary.BigEndian.PutUint16(data[14:16], 100) // data_length=100 but we only have 0 bytes

	_, err := Decode(data)
	require.Error(t, err)
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
	require.NoError(t, err)

	require.Len(t, msg.Operations, 2)
	require.Equal(t, uint16(0x0200), msg.Operations[0].OpID)
	require.Nil(t, msg.Operations[0].Data)
	require.Equal(t, uint16(OpSpliceNull), msg.Operations[1].OpID)
}

func TestDecode_SegmentationDescriptor_TooShort(t *testing.T) {
	// SOM with segmentation_descriptor but only 3 bytes (needs 5 minimum).
	data := make([]byte, 5) // OpID(2) + 3 bytes
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)

	_, err := Decode(data)
	require.Error(t, err)
}

func TestDecode_SegmentationDescriptor_NonCancel_TooShort(t *testing.T) {
	// SOM with non-cancel segmentation_descriptor but insufficient bytes for
	// the non-cancel fields.
	data := make([]byte, 9) // OpID(2) + seg_event_id(4) + flags(1) + 2 bytes (needs 7 more for duration+upid header)
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	binary.BigEndian.PutUint32(data[2:6], 100)
	data[6] = 0x01 // flags: program_seg_flag=1, no cancel

	_, err := Decode(data)
	require.Error(t, err)
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
			require.NoError(t, err)

			srd := msg.Operations[0].Data.(*SpliceRequestData)
			require.Equal(t, tt.insertType, srd.SpliceInsertType)
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
	require.NoError(t, err)

	require.Equal(t, uint8(7), msg.ASIndex)
	require.Equal(t, uint8(11), msg.MessageNumber)
	require.Equal(t, uint16(3000), msg.DPIPIDIndex)
	require.Len(t, msg.Operations, 1)
	require.Equal(t, uint16(OpSpliceNull), msg.Operations[0].OpID)
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
	require.NoError(t, err)

	require.Equal(t, uint8(7), msg.ASIndex)
	require.Equal(t, uint8(11), msg.MessageNumber)
	require.Equal(t, uint16(3000), msg.DPIPIDIndex)
	require.Len(t, msg.Operations, 1)
	require.Equal(t, uint16(OpSpliceNull), msg.Operations[0].OpID)
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
	require.NoError(t, err)

	require.Equal(t, uint8(7), msg.ASIndex)
	require.Equal(t, uint8(11), msg.MessageNumber)
	require.Equal(t, uint16(3000), msg.DPIPIDIndex)
	require.Len(t, msg.Operations, 1)
	require.Equal(t, uint16(OpSpliceNull), msg.Operations[0].OpID)
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
	require.NoError(t, err)

	require.Equal(t, uint8(7), msg.ASIndex)
	require.Equal(t, uint8(11), msg.MessageNumber)
	require.Equal(t, uint16(3000), msg.DPIPIDIndex)
	require.Len(t, msg.Operations, 1)
	require.Equal(t, uint16(OpSpliceNull), msg.Operations[0].OpID)
}

func TestDecode_MOM_TimeTypeUnknown(t *testing.T) {
	// Unknown time_type should fall back to 1-byte (just the type field).
	ts := []byte{0xFF}
	buf := buildMOMWithTimestamp(ts)

	msg, err := Decode(buf)
	require.NoError(t, err)

	require.Len(t, msg.Operations, 1)
	require.Equal(t, uint16(OpSpliceNull), msg.Operations[0].OpID)
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
	spliceData[13] = 0x80 // auto_return_flag (bit 7 per SCTE-104 spec)

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
	require.NoError(t, err)

	require.Len(t, msg.Operations, 1)

	require.IsType(t, &SpliceRequestData{}, msg.Operations[0].Data)
	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, uint32(42), srd.SpliceEventID)
	require.Equal(t, uint16(5000), srd.PreRollTime)
}

func TestDecode_MOM_TimeType1_TooShortForTimestamp(t *testing.T) {
	// MOM with time_type=1 (needs 7 bytes for timestamp) but truncated.
	// Build a buffer that has time_type=1 but not enough remaining bytes.
	buf := make([]byte, 14) // OpID(2) + messageSize(2) + headers(6) + time_type(1) + 1 byte (need 6 more)
	binary.BigEndian.PutUint16(buf[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(buf[2:4], 12) // messageSize
	buf[10] = 0x01                           // time_type = 1 (UTC, needs 7 bytes total)

	_, err := Decode(buf)
	require.Error(t, err)
}

func TestDecode_SegmentationDescriptor_EmptyUPID(t *testing.T) {
	// Non-cancel seg descriptor with zero-length UPID.
	// Per SCTE 104 2021: seg_event_id(4) + flags(1) + duration(5) + upid_type(1) +
	// upid_length(1) + type_id(1) + seg_num(1) + segs_expected(1) = 15
	payload := make([]byte, 15)
	binary.BigEndian.PutUint32(payload[0:4], 123)
	payload[4] = 0x01  // flags: program_seg_flag=1
	payload[10] = 0x01 // upid_type
	payload[11] = 0    // upid_length = 0
	payload[12] = 0x30 // segmentation_type_id (after upid)
	payload[13] = 1    // seg_num
	payload[14] = 1    // segs_expected

	data := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	copy(data[2:], payload)

	msg, err := Decode(data)
	require.NoError(t, err)

	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint32(123), sd.SegEventID)
	require.Len(t, sd.UPID, 0)
}

func TestDecode_SOM_FullHeader(t *testing.T) {
	// Build a full SOM with the 12-byte header before the operation data.
	spliceData := make([]byte, 14)
	spliceData[0] = SpliceStartImmediate              // splice_insert_type
	binary.BigEndian.PutUint32(spliceData[1:5], 77)   // splice_event_id
	binary.BigEndian.PutUint16(spliceData[5:7], 200)  // unique_program_id
	binary.BigEndian.PutUint16(spliceData[7:9], 3000) // pre_roll_time
	binary.BigEndian.PutUint16(spliceData[9:11], 600) // break_duration
	spliceData[11] = 3                                // avail_num
	spliceData[12] = 4                                // avails_expected
	spliceData[13] = 0x80                             // auto_return_flag (bit 7 per SCTE-104 spec)

	headerSize := 12
	payloadSize := headerSize + len(spliceData) // 12 + 14 = 26

	// Full message: OpID(2) + payload(26)
	buf := make([]byte, 2+payloadSize)
	binary.BigEndian.PutUint16(buf[0:2], OpSpliceRequest) // OpID

	// Payload starts at buf[2]:
	payload := buf[2:]
	binary.BigEndian.PutUint16(payload[0:2], uint16(payloadSize-2)) // messageSize = 24 (spec: excludes self)
	binary.BigEndian.PutUint16(payload[2:4], 0)                     // result (ignored)
	binary.BigEndian.PutUint16(payload[4:6], 0)                     // result_extension (ignored)
	payload[6] = 2                                                  // protocol_version
	payload[7] = 10                                                 // AS_index
	payload[8] = 5                                                  // message_number
	binary.BigEndian.PutUint16(payload[9:11], 4000)                 // DPI_PID_index
	payload[11] = 0                                                 // SCTE35_protocol_version
	copy(payload[12:], spliceData)

	msg, err := Decode(buf)
	require.NoError(t, err)

	// Verify header fields are populated.
	require.Equal(t, uint8(2), msg.ProtocolVersion)
	require.Equal(t, uint8(10), msg.ASIndex)
	require.Equal(t, uint8(5), msg.MessageNumber)
	require.Equal(t, uint16(4000), msg.DPIPIDIndex)

	// Verify operation data is parsed correctly.
	require.Len(t, msg.Operations, 1)
	op := msg.Operations[0]
	require.Equal(t, uint16(OpSpliceRequest), op.OpID)
	require.IsType(t, &SpliceRequestData{}, op.Data)
	srd := op.Data.(*SpliceRequestData)
	require.Equal(t, uint8(SpliceStartImmediate), srd.SpliceInsertType)
	require.Equal(t, uint32(77), srd.SpliceEventID)
	require.Equal(t, uint16(200), srd.UniqueProgramID)
	require.Equal(t, uint16(3000), srd.PreRollTime)
	require.Equal(t, uint16(600), srd.BreakDuration)
	require.Equal(t, uint8(3), srd.AvailNum)
	require.Equal(t, uint8(4), srd.AvailsExpected)
	require.True(t, srd.AutoReturnFlag, "AutoReturnFlag = false, want true")
}

func TestDecode_SOM_AbbreviatedVANC_Regression(t *testing.T) {
	// Verify that abbreviated SOM (VANC format) still works correctly.
	data := make([]byte, 16)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceRequest)
	data[2] = SpliceEndImmediate                 // splice_insert_type
	binary.BigEndian.PutUint32(data[3:7], 12345) // splice_event_id
	binary.BigEndian.PutUint16(data[7:9], 500)   // unique_program_id
	binary.BigEndian.PutUint16(data[9:11], 2000) // pre_roll_time
	binary.BigEndian.PutUint16(data[11:13], 150) // break_duration
	data[13] = 2                                 // avail_num
	data[14] = 3                                 // avails_expected
	data[15] = 0                                 // auto_return_flag = false

	msg, err := Decode(data)
	require.NoError(t, err)

	// Header fields should be zero (not populated from abbreviated format).
	require.Equal(t, uint8(0), msg.ProtocolVersion)
	require.Equal(t, uint8(0), msg.ASIndex)
	require.Equal(t, uint8(0), msg.MessageNumber)
	require.Equal(t, uint16(0), msg.DPIPIDIndex)

	// Verify operation data is parsed correctly from the direct payload.
	require.Len(t, msg.Operations, 1)
	require.IsType(t, &SpliceRequestData{}, msg.Operations[0].Data)
	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, uint8(SpliceEndImmediate), srd.SpliceInsertType)
	require.Equal(t, uint32(12345), srd.SpliceEventID)
	require.Equal(t, uint16(500), srd.UniqueProgramID)
	require.Equal(t, uint16(2000), srd.PreRollTime)
	require.Equal(t, uint16(150), srd.BreakDuration)
	require.Equal(t, uint8(2), srd.AvailNum)
	require.Equal(t, uint8(3), srd.AvailsExpected)
	require.False(t, srd.AutoReturnFlag, "AutoReturnFlag = true, want false")
}

func TestDecode_SOM_FullHeader_SpliceNull(t *testing.T) {
	// Full SOM with time_signal (2 bytes of operation data).
	payloadSize := 14

	buf := make([]byte, 2+payloadSize)
	binary.BigEndian.PutUint16(buf[0:2], OpTimeSignalRequest) // OpID

	payload := buf[2:]
	binary.BigEndian.PutUint16(payload[0:2], uint16(payloadSize-2)) // messageSize = 12
	binary.BigEndian.PutUint16(payload[2:4], 0)                     // result
	binary.BigEndian.PutUint16(payload[4:6], 0)                     // result_extension
	payload[6] = 1                                                  // protocol_version
	payload[7] = 3                                                  // AS_index
	payload[8] = 9                                                  // message_number
	binary.BigEndian.PutUint16(payload[9:11], 500)                  // DPI_PID_index
	payload[11] = 0                                                 // SCTE35_protocol_version
	binary.BigEndian.PutUint16(payload[12:14], 7000)                // pre_roll_time

	msg, err := Decode(buf)
	require.NoError(t, err)

	require.Equal(t, uint8(1), msg.ProtocolVersion)
	require.Equal(t, uint8(3), msg.ASIndex)
	require.Equal(t, uint8(9), msg.MessageNumber)
	require.Equal(t, uint16(500), msg.DPIPIDIndex)

	require.Len(t, msg.Operations, 1)
	require.IsType(t, &TimeSignalRequestData{}, msg.Operations[0].Data)
	tsr := msg.Operations[0].Data.(*TimeSignalRequestData)
	require.Equal(t, uint16(7000), tsr.PreRollTime)
}

func TestDecode_SegmentationDescriptor_SpecFormat(t *testing.T) {
	// Spec-compliant wire format per SCTE 104 2021 Table 8-29.
	upid := []byte("AD-ID-99")
	dur := uint64(2700000) // 30 seconds at 90kHz

	segPayload := make([]byte, 15+len(upid))
	binary.BigEndian.PutUint32(segPayload[0:4], 500) // seg_event_id
	segPayload[4] = 0x01                             // flags: cancel=0, reserved=0, program_seg_flag=1
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
	require.NoError(t, err)

	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint32(500), sd.SegEventID)
	require.Equal(t, uint8(0x34), sd.SegmentationTypeID)
	require.Equal(t, uint64(2700000), sd.DurationTicks)
	require.Equal(t, uint8(0x09), sd.UPIDType)
	require.Equal(t, "AD-ID-99", string(sd.UPID))
	require.Equal(t, uint8(1), sd.SegNum)
	require.Equal(t, uint8(1), sd.SegExpected)
	require.True(t, sd.ProgramSegmentationFlag, "ProgramSegmentationFlag should be true")
	require.False(t, sd.CancelIndicator, "CancelIndicator should be false")
}

func TestDecode_SegmentationDescriptor_ComponentLevel(t *testing.T) {
	// Component-level segmentation: program_seg_flag=0, component_count=2.
	upid := []byte("X")
	dur := uint64(900000)

	segPayload := make([]byte, 19)
	binary.BigEndian.PutUint32(segPayload[0:4], 700) // seg_event_id
	segPayload[4] = 0x00                             // flags: cancel=0, program_seg_flag=0
	segPayload[5] = 2                                // component_count
	segPayload[6] = 0x01                             // component_tag 1
	segPayload[7] = 0x02                             // component_tag 2
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
	require.NoError(t, err)

	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.False(t, sd.ProgramSegmentationFlag, "ProgramSegmentationFlag should be false for component-level")
	require.Equal(t, uint8(0x22), sd.SegmentationTypeID)
	require.Equal(t, uint64(900000), sd.DurationTicks)
	require.Equal(t, uint8(1), sd.SegNum)
	require.Equal(t, uint8(3), sd.SegExpected)
}

func TestDecode_MOM_MessageSizeMismatch(t *testing.T) {
	// MOM with messageSize that doesn't match either convention.
	headerSize := 10
	opSize := 4 + 0 // splice_null
	messageSize := headerSize + opSize

	buf := make([]byte, 2+messageSize)
	binary.BigEndian.PutUint16(buf[0:2], OpMultipleOperationMessage)
	binary.BigEndian.PutUint16(buf[2:4], 9999) // intentionally wrong messageSize
	buf[11] = 1                                // num_ops

	offset := 12
	binary.BigEndian.PutUint16(buf[offset:offset+2], OpSpliceNull)
	binary.BigEndian.PutUint16(buf[offset+2:offset+4], 0)

	msg, err := Decode(buf)
	require.NoError(t, err)

	require.Len(t, msg.Operations, 1)
	require.Equal(t, uint16(OpSpliceNull), msg.Operations[0].OpID)
}

func TestDecode_SOM_LegacyMessageSize(t *testing.T) {
	// Legacy convention: messageSize = len(payload) (total length, not spec-compliant).
	spliceData := make([]byte, 14)
	spliceData[0] = SpliceStartImmediate
	binary.BigEndian.PutUint32(spliceData[1:5], 88)

	headerSize := 12
	payloadSize := headerSize + len(spliceData) // 26

	buf := make([]byte, 2+payloadSize)
	binary.BigEndian.PutUint16(buf[0:2], OpSpliceRequest)

	payload := buf[2:]
	binary.BigEndian.PutUint16(payload[0:2], uint16(payloadSize)) // legacy: messageSize = total length
	binary.BigEndian.PutUint16(payload[2:4], 0)
	binary.BigEndian.PutUint16(payload[4:6], 0)
	payload[6] = 1 // protocol_version
	payload[7] = 5 // AS_index
	payload[8] = 2 // message_number
	binary.BigEndian.PutUint16(payload[9:11], 1000)
	payload[11] = 0 // SCTE35_protocol_version
	copy(payload[12:], spliceData)

	msg, err := Decode(buf)
	require.NoError(t, err)

	require.Equal(t, uint8(1), msg.ProtocolVersion)
	require.Equal(t, uint8(5), msg.ASIndex)

	require.IsType(t, &SpliceRequestData{}, msg.Operations[0].Data)
	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, uint32(88), srd.SpliceEventID)
}

func TestDecode_AutoReturnFlag_Bit7(t *testing.T) {
	// Per SCTE-104 spec, auto_return_flag is bit 7 (MSB) of byte 13.
	data := make([]byte, 16) // OpID(2) + splice_request_data(14)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceRequest)
	data[2] = SpliceStartImmediate           // splice_insert_type
	binary.BigEndian.PutUint32(data[3:7], 1) // splice_event_id
	data[15] = 0x80                          // auto_return_flag: bit 7 set

	msg, err := Decode(data)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.True(t, srd.AutoReturnFlag, "AutoReturnFlag = false, want true (bit 7 = 0x80)")
}

func TestDecode_AutoReturnFlag_Bit0Only_IsFalse(t *testing.T) {
	// If only bit 0 is set (0x01) but bit 7 is not, auto_return_flag should be false.
	data := make([]byte, 16) // OpID(2) + splice_request_data(14)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceRequest)
	data[2] = SpliceStartImmediate           // splice_insert_type
	binary.BigEndian.PutUint32(data[3:7], 1) // splice_event_id
	data[15] = 0x01                          // only bit 0 set, NOT bit 7

	msg, err := Decode(data)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.False(t, srd.AutoReturnFlag, "AutoReturnFlag = true, want false (only bit 0 set, bit 7 required)")
}

func TestDecode_SOM_FullHeader_SegmentationDescriptor(t *testing.T) {
	// Full SOM with 12-byte header + segmentation_descriptor_request operation.
	upid := []byte("ABCD")
	dur := uint64(1800000) // 20 seconds at 90kHz

	segPayload := make([]byte, 15+len(upid))
	binary.BigEndian.PutUint32(segPayload[0:4], 1234) // seg_event_id
	segPayload[4] = 0x01                              // flags: program_seg_flag=1
	segPayload[5] = byte(dur >> 32)
	segPayload[6] = byte(dur >> 24)
	segPayload[7] = byte(dur >> 16)
	segPayload[8] = byte(dur >> 8)
	segPayload[9] = byte(dur)
	segPayload[10] = 0x09            // upid_type
	segPayload[11] = byte(len(upid)) // upid_length
	copy(segPayload[12:], upid)
	segPayload[12+len(upid)] = 0x34 // segmentation_type_id
	segPayload[12+len(upid)+1] = 2  // seg_num
	segPayload[12+len(upid)+2] = 4  // segs_expected

	headerSize := 12
	payloadSize := headerSize + len(segPayload) // 12 + 19 = 31

	buf := make([]byte, 2+payloadSize)
	binary.BigEndian.PutUint16(buf[0:2], OpSegmentationDescriptorRequest) // OpID

	payload := buf[2:]
	binary.BigEndian.PutUint16(payload[0:2], uint16(payloadSize-2)) // messageSize (spec convention)
	binary.BigEndian.PutUint16(payload[2:4], 0)                     // result
	binary.BigEndian.PutUint16(payload[4:6], 0)                     // result_extension
	payload[6] = 3                                                  // protocol_version
	payload[7] = 8                                                  // AS_index
	payload[8] = 15                                                 // message_number
	binary.BigEndian.PutUint16(payload[9:11], 6000)                 // DPI_PID_index
	payload[11] = 0                                                 // SCTE35_protocol_version
	copy(payload[12:], segPayload)

	msg, err := Decode(buf)
	require.NoError(t, err)

	// Verify header fields are populated (full SOM path taken).
	require.Equal(t, uint8(3), msg.ProtocolVersion)
	require.Equal(t, uint8(8), msg.ASIndex)
	require.Equal(t, uint8(15), msg.MessageNumber)
	require.Equal(t, uint16(6000), msg.DPIPIDIndex)

	// Verify segmentation descriptor data.
	require.Len(t, msg.Operations, 1)
	require.Equal(t, uint16(OpSegmentationDescriptorRequest), msg.Operations[0].OpID)
	require.IsType(t, &SegmentationDescriptorRequest{}, msg.Operations[0].Data)
	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint32(1234), sd.SegEventID)
	require.Equal(t, uint8(0x34), sd.SegmentationTypeID)
	require.Equal(t, uint64(1800000), sd.DurationTicks)
	require.Equal(t, "ABCD", string(sd.UPID))
	require.Equal(t, uint8(2), sd.SegNum)
	require.Equal(t, uint8(4), sd.SegExpected)
	require.True(t, sd.ProgramSegmentationFlag, "ProgramSegmentationFlag should be true")
}

func TestDecode_SOM_AmbiguousMessageSize(t *testing.T) {
	// When messageSize matches neither spec (len-2) nor legacy (len) convention,
	// decodeSOM falls through to the abbreviated format.
	data := make([]byte, 16)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceRequest)
	data[2] = SpliceStartImmediate                // splice_insert_type
	binary.BigEndian.PutUint32(data[3:7], 55555)  // splice_event_id
	binary.BigEndian.PutUint16(data[7:9], 300)    // unique_program_id
	binary.BigEndian.PutUint16(data[9:11], 1500)  // pre_roll_time
	binary.BigEndian.PutUint16(data[11:13], 200)  // break_duration
	data[13] = 1                                  // avail_num
	data[14] = 5                                  // avails_expected
	data[15] = 0x80                               // auto_return_flag (bit 7)

	// Verify the first 2 bytes of payload (data[2:4]) do NOT match either convention.
	payloadLen := 14
	pseudoMsgSize := int(binary.BigEndian.Uint16(data[2:4]))
	require.True(t, pseudoMsgSize != payloadLen-2 && pseudoMsgSize != payloadLen,
		"test setup error: pseudo messageSize %d matches a convention (payload len %d)",
		pseudoMsgSize, payloadLen)

	msg, err := Decode(data)
	require.NoError(t, err)

	// Abbreviated path: header fields should be zero.
	require.Equal(t, uint8(0), msg.ProtocolVersion)
	require.Equal(t, uint8(0), msg.ASIndex)
	require.Equal(t, uint8(0), msg.MessageNumber)
	require.Equal(t, uint16(0), msg.DPIPIDIndex)

	// Verify operation data parsed correctly from direct payload.
	require.Len(t, msg.Operations, 1)
	require.IsType(t, &SpliceRequestData{}, msg.Operations[0].Data)
	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, uint8(SpliceStartImmediate), srd.SpliceInsertType)
	require.Equal(t, uint32(55555), srd.SpliceEventID)
	require.Equal(t, uint16(300), srd.UniqueProgramID)
	require.Equal(t, uint16(1500), srd.PreRollTime)
	require.Equal(t, uint16(200), srd.BreakDuration)
	require.Equal(t, uint8(1), srd.AvailNum)
	require.Equal(t, uint8(5), srd.AvailsExpected)
	require.True(t, srd.AutoReturnFlag, "AutoReturnFlag = false, want true")
}

func TestDecode_SOM_SpliceNull_NoHeader(t *testing.T) {
	// splice_null with 0-byte payload after the OpID.
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceNull)

	msg, err := Decode(data)
	require.NoError(t, err)

	// Abbreviated path: header fields should all be zero.
	require.Equal(t, uint8(0), msg.ProtocolVersion)
	require.Equal(t, uint8(0), msg.ASIndex)
	require.Equal(t, uint8(0), msg.MessageNumber)
	require.Equal(t, uint16(0), msg.DPIPIDIndex)

	require.Len(t, msg.Operations, 1)
	op := msg.Operations[0]
	require.Equal(t, uint16(OpSpliceNull), op.OpID)
	require.Nil(t, op.Data)
}

func TestDecode_SegmentationDescriptor_SubSegments(t *testing.T) {
	// Build an abbreviated SOM segmentation_descriptor_request with sub-segment fields.
	payload := make([]byte, 17)
	binary.BigEndian.PutUint32(payload[0:4], 5000) // seg_event_id
	payload[4] = 0x01                              // flags: program_segmentation_flag=1
	// duration: 900000 ticks (10 seconds at 90kHz) as 5-byte big-endian
	payload[5] = 0
	payload[6] = 0
	binary.BigEndian.PutUint16(payload[7:9], 0x0DBA)
	payload[9] = 0xC0
	binary.BigEndian.PutUint32(payload[5:9], 0x000DBBA0>>8)
	payload[9] = byte(0x000DBBA0 & 0xFF)
	// Fix: use proper 5-byte encoding
	dur := uint64(900000)
	payload[5] = byte(dur >> 32)
	payload[6] = byte(dur >> 24)
	payload[7] = byte(dur >> 16)
	payload[8] = byte(dur >> 8)
	payload[9] = byte(dur)
	payload[10] = 0x09 // upid_type
	payload[11] = 0    // upid_length = 0
	payload[12] = 0x34 // segmentation_type_id
	payload[13] = 2    // seg_num
	payload[14] = 4    // segs_expected
	payload[15] = 3    // sub_segment_num
	payload[16] = 6    // sub_segments_expected

	// Prepend OpID for abbreviated SOM.
	data := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	copy(data[2:], payload)

	msg, err := Decode(data)
	require.NoError(t, err)

	require.Len(t, msg.Operations, 1)

	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint32(5000), sd.SegEventID)
	require.Equal(t, uint8(3), sd.SubSegmentNum)
	require.Equal(t, uint8(6), sd.SubSegmentsExpected)
}

func TestDecode_SegmentationDescriptor_NoSubSegments_Backward_Compatible(t *testing.T) {
	// Verify that messages without sub-segment fields still decode correctly.
	payload := make([]byte, 15)
	binary.BigEndian.PutUint32(payload[0:4], 42)
	payload[4] = 0x01  // flags: program_segmentation_flag=1
	payload[10] = 0x09 // upid_type
	payload[11] = 0    // upid_length = 0
	payload[12] = 0x34 // segmentation_type_id
	payload[13] = 1    // seg_num
	payload[14] = 2    // segs_expected

	data := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	copy(data[2:], payload)

	msg, err := Decode(data)
	require.NoError(t, err)

	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint8(0), sd.SubSegmentNum)
	require.Equal(t, uint8(0), sd.SubSegmentsExpected)
}

// Bug 14: An abbreviated splice_request payload whose first 2 bytes accidentally
// encode a value matching len(payload)-2 or len(payload) should NOT be falsely
// parsed as a full SOM with header. The protocolVersion check (byte[6] > 15)
// guards against this.
func TestDecode_SOM_AbbreviatedNotFalsePositiveSOM(t *testing.T) {
	payload := make([]byte, 16) // OpID(2) + 14 bytes
	binary.BigEndian.PutUint16(payload[0:2], OpSpliceRequest)

	// Abbreviated payload starts at payload[2:].
	abbrev := payload[2:]
	// Set first 2 bytes to 12 (= len(abbrev)-2 = 14-2), triggering the
	// messageSize length-match heuristic.
	binary.BigEndian.PutUint16(abbrev[0:2], 12) // messageSize match!

	// In a real SOM, abbrev[6] is protocol_version. Set to 0xFF (invalid
	// protocol version, > 15) to test that the fix rejects SOM interpretation.
	abbrev[6] = 0xFF

	msg, err := Decode(payload)
	require.NoError(t, err)

	require.Len(t, msg.Operations, 1)

	require.IsType(t, &SpliceRequestData{}, msg.Operations[0].Data)
	srd := msg.Operations[0].Data.(*SpliceRequestData)

	// In abbreviated mode, splice_insert_type is the first byte of the payload.
	require.Equal(t, uint8(0x00), srd.SpliceInsertType)

	// splice_event_id = bytes [1:5] = 0x0C000000
	wantEventID := uint32(0x0C000000)
	require.Equal(t, wantEventID, srd.SpliceEventID)

	// The message should NOT have SOM header fields populated.
	require.Equal(t, uint8(0), msg.ProtocolVersion)
}
