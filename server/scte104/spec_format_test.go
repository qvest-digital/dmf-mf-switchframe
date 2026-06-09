package scte104

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---- Finding #17: insert_segmentation_descriptor_request wire format ----
//
// SCTE 104 Table 8-29 layout (byte-aligned, all fields always present):
//
//	segmentation_event_id               4 bytes
//	segmentation_event_cancel_indicator 1 byte (full byte, value 0/1)
//	duration                            2 bytes (SECONDS, not 90 kHz ticks)
//	segmentation_upid_type              1 byte
//	segmentation_upid_length            1 byte
//	segmentation_upid                   N bytes
//	segmentation_type_id                1 byte
//	segment_num                         1 byte
//	segments_expected                   1 byte
//	duration_extension_frames           1 byte
//	delivery_not_restricted_flag        1 byte
//	web_delivery_allowed_flag           1 byte
//	no_regional_blackout_flag           1 byte
//	archive_allowed_flag                1 byte
//	device_restrictions                 1 byte

// buildSpecSegDescriptorPayload builds a spec-compliant Table 8-29 payload.
func buildSpecSegDescriptorPayload(eventID uint32, cancel byte, durationSec uint16,
	upidType byte, upid []byte, typeID, segNum, segsExpected byte,
	extFrames byte, restrictions [5]byte) []byte {

	p := make([]byte, 0, 18+len(upid))
	var id [4]byte
	binary.BigEndian.PutUint32(id[:], eventID)
	p = append(p, id[:]...)
	p = append(p, cancel)
	var dur [2]byte
	binary.BigEndian.PutUint16(dur[:], durationSec)
	p = append(p, dur[:]...)
	p = append(p, upidType, byte(len(upid)))
	p = append(p, upid...)
	p = append(p, typeID, segNum, segsExpected, extFrames)
	p = append(p, restrictions[:]...)
	return p
}

func TestDecode_SegmentationDescriptor_SpecTable829(t *testing.T) {
	t.Parallel()
	// A typical provider-placement-opportunity-start descriptor as a
	// spec-compliant automation system (e.g. libklvanc) would emit it:
	// non-cancel, 300 second duration, ADI UPID "ABC", type 0x34.
	payload := buildSpecSegDescriptorPayload(
		0x12345678, // segmentation_event_id
		0x00,       // cancel_indicator = 0 (full byte)
		300,        // duration = 300 SECONDS
		0x09,       // upid_type (ADI)
		[]byte("ABC"),
		0x34,                   // segmentation_type_id
		1,                      // segment_num
		4,                      // segments_expected
		0,                      // duration_extension_frames
		[5]byte{0, 1, 1, 1, 3}, // restricted: web=1, blackout=1, archive=1, device=3
	)

	data := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	copy(data[2:], payload)

	msg, err := Decode(data)
	require.NoError(t, err)
	require.Len(t, msg.Operations, 1)

	require.IsType(t, &SegmentationDescriptorRequest{}, msg.Operations[0].Data)
	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint32(0x12345678), sd.SegEventID)
	require.False(t, sd.CancelIndicator, "cancel byte 0x00 must decode as non-cancel")
	require.Equal(t, uint8(0x09), sd.UPIDType)
	require.Equal(t, "ABC", string(sd.UPID))
	require.Equal(t, uint8(0x34), sd.SegmentationTypeID,
		"segmentation_type_id must come from the spec position (after the UPID)")
	require.Equal(t, uint8(1), sd.SegNum)
	require.Equal(t, uint8(4), sd.SegExpected)

	// The 2-byte SECONDS duration must translate to 90 kHz ticks for SCTE-35.
	cue, err := ToCueMessage(msg)
	require.NoError(t, err)
	require.Len(t, cue.Descriptors, 1)
	require.NotNil(t, cue.Descriptors[0].DurationTicks, "duration must survive translation")
	require.Equal(t, uint64(300)*90000, *cue.Descriptors[0].DurationTicks,
		"300 s duration must become 27,000,000 90kHz ticks")
}

func TestDecode_SegmentationDescriptor_SpecCancel(t *testing.T) {
	t.Parallel()
	// Spec cancel messages use a FULL byte with value 1 — and keep the
	// remaining (ignored) fields in place per Table 8-29.
	payload := buildSpecSegDescriptorPayload(
		777,  // segmentation_event_id
		0x01, // cancel_indicator = 1 (full byte, NOT bit 7)
		0, 0, nil, 0, 0, 0, 0, [5]byte{1, 0, 0, 0, 0},
	)

	data := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(data[0:2], OpSegmentationDescriptorRequest)
	copy(data[2:], payload)

	msg, err := Decode(data)
	require.NoError(t, err)

	sd := msg.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.True(t, sd.CancelIndicator, "cancel byte 0x01 must decode as cancel")
	require.Equal(t, uint32(777), sd.SegEventID)
}

func TestEncodeDecode_SegmentationDescriptor_SpecWireBytes(t *testing.T) {
	t.Parallel()
	// Encode a segmentation descriptor and verify the operation payload on
	// the wire follows Table 8-29 exactly.
	upid := []byte("AD-1")
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:            0x0A0B0C0D,
					SegmentationTypeID:    0x30,
					Duration:              45, // seconds
					UPIDType:              0x09,
					UPID:                  upid,
					SegNum:                1,
					SegExpected:           2,
					DeliveryNotRestricted: true,
				},
			},
		},
	}

	data, err := Encode(msg)
	require.NoError(t, err)

	// MOM header is 12 bytes, operation header 4 bytes.
	opPayload := data[16:]
	require.Len(t, opPayload, 18+len(upid), "Table 8-29 payload is 18+N bytes")

	require.Equal(t, uint32(0x0A0B0C0D), binary.BigEndian.Uint32(opPayload[0:4]), "segmentation_event_id")
	require.Equal(t, byte(0), opPayload[4], "cancel_indicator full byte")
	require.Equal(t, uint16(45), binary.BigEndian.Uint16(opPayload[5:7]), "duration in seconds")
	require.Equal(t, byte(0x09), opPayload[7], "upid_type")
	require.Equal(t, byte(len(upid)), opPayload[8], "upid_length")
	require.Equal(t, upid, opPayload[9:9+len(upid)], "upid")
	o := 9 + len(upid)
	require.Equal(t, byte(0x30), opPayload[o], "segmentation_type_id")
	require.Equal(t, byte(1), opPayload[o+1], "segment_num")
	require.Equal(t, byte(2), opPayload[o+2], "segments_expected")
	require.Equal(t, byte(0), opPayload[o+3], "duration_extension_frames")
	require.Equal(t, byte(1), opPayload[o+4], "delivery_not_restricted_flag")
	require.Equal(t, byte(0), opPayload[o+5], "web_delivery_allowed_flag")
	require.Equal(t, byte(0), opPayload[o+6], "no_regional_blackout_flag")
	require.Equal(t, byte(0), opPayload[o+7], "archive_allowed_flag")
	require.Equal(t, byte(0), opPayload[o+8], "device_restrictions")

	// And it round-trips.
	decoded, err := Decode(data)
	require.NoError(t, err)
	sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint32(0x0A0B0C0D), sd.SegEventID)
	require.Equal(t, uint16(45), sd.Duration)
	require.Equal(t, uint8(0x30), sd.SegmentationTypeID)
	require.True(t, sd.DeliveryNotRestricted)
}

// ---- Finding #29: splice_request auto_return_flag is a full byte ----

func TestDecode_AutoReturnFlag_SpecFullByteValue1(t *testing.T) {
	t.Parallel()
	// Per SCTE 104 Table 8-5, auto_return_flag is a full 8-bit field whose
	// value is 1 to request auto-return. A spec-compliant sender writes 0x01.
	data := make([]byte, 16) // OpID(2) + splice_request_data(14)
	binary.BigEndian.PutUint16(data[0:2], OpSpliceRequest)
	data[2] = SpliceStartImmediate           // splice_insert_type
	binary.BigEndian.PutUint32(data[3:7], 1) // splice_event_id
	data[15] = 0x01                          // auto_return_flag = 1 (spec value)

	msg, err := Decode(data)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.True(t, srd.AutoReturnFlag,
		"spec sender's auto_return_flag value 1 must decode as true")
}

func TestEncode_AutoReturnFlag_SpecFullByteValue1(t *testing.T) {
	t.Parallel()
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceStartImmediate,
					SpliceEventID:    1,
					AutoReturnFlag:   true,
				},
			},
		},
	}

	data, err := Encode(msg)
	require.NoError(t, err)

	// splice_request_data starts after MOM header(12) + op header(4);
	// auto_return_flag is byte 13 within the 14-byte payload.
	autoReturnByte := data[12+4+13]
	require.Equal(t, byte(0x01), autoReturnByte,
		"auto_return_flag must be encoded as the full-byte value 1 per SCTE 104")

	// False must encode as 0.
	msg.Operations[0].Data.(*SpliceRequestData).AutoReturnFlag = false
	data2, err := Encode(msg)
	require.NoError(t, err)
	require.Equal(t, byte(0x00), data2[12+4+13])
}

// ---- Finding #88: full Single Operation Message header ----

// buildSpecFullSOM builds a spec-compliant full single_operation_message:
//
//	opID(2) + messageSize(2) + result(2) + result_extension(2) +
//	protocol_version(1) + AS_index(1) + message_number(1) + DPI_PID_index(2) +
//	operation data
//
// messageSize is the size of the COMPLETE message including the opID.
func buildSpecFullSOM(opID uint16, protocolVersion, asIndex, msgNum uint8, dpiPIDIndex uint16, opData []byte) []byte {
	total := 2 + 11 + len(opData)
	buf := make([]byte, total)
	binary.BigEndian.PutUint16(buf[0:2], opID)
	binary.BigEndian.PutUint16(buf[2:4], uint16(total)) // spec: complete message size
	binary.BigEndian.PutUint16(buf[4:6], 0xFFFF)        // result (request)
	binary.BigEndian.PutUint16(buf[6:8], 0xFFFF)        // result_extension
	buf[8] = protocolVersion
	buf[9] = asIndex
	buf[10] = msgNum
	binary.BigEndian.PutUint16(buf[11:13], dpiPIDIndex)
	copy(buf[13:], opData)
	return buf
}

func TestDecode_SOM_SpecFullHeader_SpliceRequest(t *testing.T) {
	t.Parallel()
	spliceData := make([]byte, 14)
	spliceData[0] = SpliceStartImmediate              // splice_insert_type
	binary.BigEndian.PutUint32(spliceData[1:5], 77)   // splice_event_id
	binary.BigEndian.PutUint16(spliceData[5:7], 200)  // unique_program_id
	binary.BigEndian.PutUint16(spliceData[7:9], 3000) // pre_roll_time
	binary.BigEndian.PutUint16(spliceData[9:11], 600) // break_duration
	spliceData[11] = 3                                // avail_num
	spliceData[12] = 4                                // avails_expected
	spliceData[13] = 1                                // auto_return_flag (spec full byte)

	buf := buildSpecFullSOM(OpSpliceRequest, 0, 10, 5, 4000, spliceData)

	msg, err := Decode(buf)
	require.NoError(t, err)

	// Header fields must be populated from the SOM header (11 bytes after
	// opID — there is no SCTE35_protocol_version byte in a SOM).
	require.Equal(t, uint8(0), msg.ProtocolVersion)
	require.Equal(t, uint8(10), msg.ASIndex)
	require.Equal(t, uint8(5), msg.MessageNumber)
	require.Equal(t, uint16(4000), msg.DPIPIDIndex)

	require.Len(t, msg.Operations, 1)
	require.IsType(t, &SpliceRequestData{}, msg.Operations[0].Data)
	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, uint8(SpliceStartImmediate), srd.SpliceInsertType)
	require.Equal(t, uint32(77), srd.SpliceEventID)
	require.Equal(t, uint16(200), srd.UniqueProgramID)
	require.Equal(t, uint16(3000), srd.PreRollTime)
	require.Equal(t, uint16(600), srd.BreakDuration)
	require.Equal(t, uint8(3), srd.AvailNum)
	require.Equal(t, uint8(4), srd.AvailsExpected)
	require.True(t, srd.AutoReturnFlag)
}

func TestDecode_SOM_SpecFullHeader_TimeSignal(t *testing.T) {
	t.Parallel()
	opData := make([]byte, 2)
	binary.BigEndian.PutUint16(opData[0:2], 7000) // pre_roll_time

	buf := buildSpecFullSOM(OpTimeSignalRequest, 1, 3, 9, 500, opData)

	msg, err := Decode(buf)
	require.NoError(t, err)

	require.Equal(t, uint8(1), msg.ProtocolVersion)
	require.Equal(t, uint8(3), msg.ASIndex)
	require.Equal(t, uint8(9), msg.MessageNumber)
	require.Equal(t, uint16(500), msg.DPIPIDIndex)

	require.Len(t, msg.Operations, 1)
	require.IsType(t, &TimeSignalRequestData{}, msg.Operations[0].Data)
	tsr := msg.Operations[0].Data.(*TimeSignalRequestData)
	require.Equal(t, uint16(7000), tsr.PreRollTime,
		"operation data must start 11 bytes after the opID, not 12")
}

func TestDecode_SOM_SpecFullHeader_SpliceNull(t *testing.T) {
	t.Parallel()
	// A full-SOM splice_null has an empty operation payload.
	buf := buildSpecFullSOM(OpSpliceNull, 0, 2, 8, 100, nil)

	msg, err := Decode(buf)
	require.NoError(t, err)

	require.Equal(t, uint8(2), msg.ASIndex)
	require.Equal(t, uint8(8), msg.MessageNumber)
	require.Equal(t, uint16(100), msg.DPIPIDIndex)
	require.Len(t, msg.Operations, 1)
	require.Equal(t, OpSpliceNull, msg.Operations[0].OpID)
}
