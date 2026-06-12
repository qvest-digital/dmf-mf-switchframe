package caption

import (
	"encoding/binary"
	"time"
)

// Timecode represents an SMPTE 12M timecode value.
type Timecode struct {
	Hours      int
	Minutes    int
	Seconds    int
	Frames     int
	DropFrame  bool // true for 29.97/59.94
	CntDropped bool // true on first frame after a DF skip
}

// FromS12MBytes parses raw AV_FRAME_DATA_S12M_TIMECODE side data — as produced
// by FFmpeg's decoder and surfaced by codec.FFmpegDecoder.LastTimecodeBytes —
// into a Timecode. The buffer is a host-endian uint32 array: element 0 is the
// count of timecodes present (1-3), each following element is a packed SMPTE
// ST 12-1 word. Only the first timecode is decoded. Returns ok=false for
// empty/malformed input (caller should then emit no timecode for the frame).
//
// This is the inverse of libavutil's av_timecode_make_smpte_tc_string2 packing.
// All fields are BCD (tens in the high nibble, units in the low):
//
//	bits  0- 5  hours    (mask 0x3f)
//	bits  8-14  minutes  (mask 0x7f)
//	bits 16-22  seconds  (mask 0x7f)
//	bits 24-29  frames   (mask 0x3f)
//	bit  30     drop-frame flag
func FromS12MBytes(b []byte) (Timecode, bool) {
	if len(b) < 8 { // need the count word plus at least one timecode word
		return Timecode{}, false
	}
	if binary.NativeEndian.Uint32(b[0:4]) == 0 { // count of valid timecodes
		return Timecode{}, false
	}
	return fromSMPTE12M(binary.NativeEndian.Uint32(b[4:8])), true
}

// fromSMPTE12M unpacks one packed SMPTE ST 12-1 timecode word into a Timecode.
func fromSMPTE12M(w uint32) Timecode {
	return Timecode{
		Hours:     bcd2int(w & 0x3f),
		Minutes:   bcd2int((w >> 8) & 0x7f),
		Seconds:   bcd2int((w >> 16) & 0x7f),
		Frames:    bcd2int((w >> 24) & 0x3f),
		DropFrame: w&(1<<30) != 0,
	}
}

// bcd2int converts a packed BCD value (tens in the high nibble, units in the
// low nibble) to its integer value, e.g. 0x48 -> 48.
func bcd2int(v uint32) int {
	return int((v>>4)&0x0f)*10 + int(v&0x0f)
}

// UTCToTimecode converts a UTC wall-clock time to SMPTE 12M timecode
// at the given frame rate (fpsNum/fpsDen).
//
// Drop-frame is used for 29.97fps (30000/1001) and 59.94fps (60000/1001).
// All other rates use non-drop-frame, including 23.976fps where DF is
// not defined by SMPTE 12M.
func UTCToTimecode(t time.Time, fpsNum, fpsDen int) Timecode {
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	nanos := t.Sub(midnight).Nanoseconds()
	if nanos < 0 {
		nanos = 0
	}

	nomFPS := (fpsNum + fpsDen/2) / fpsDen
	totalFrames := nanos * int64(fpsNum) / (int64(fpsDen) * 1_000_000_000)

	isDF := fpsDen == 1001 && nomFPS%30 == 0
	if isDF {
		return framesToDropFrame(totalFrames, nomFPS)
	}
	return framesToNonDropFrame(totalFrames, nomFPS)
}

func framesToNonDropFrame(totalFrames int64, fps int) Timecode {
	f := int(totalFrames)
	ff := f % fps
	f /= fps
	ss := f % 60
	f /= 60
	mm := f % 60
	hh := (f / 60) % 24
	return Timecode{Hours: hh, Minutes: mm, Seconds: ss, Frames: ff}
}

// framesToDropFrame uses the standard SMPTE 12M drop-frame algorithm.
// For 29.97fps: frames 0,1 dropped at each non-decade minute.
// For 59.94fps: frames 0,1,2,3 dropped at each non-decade minute.
func framesToDropFrame(totalFrames int64, nomFPS int) Timecode {
	dropFrames := nomFPS / 15
	framesPerMin := nomFPS*60 - dropFrames
	framesPer10Min := framesPerMin*10 + dropFrames

	F := int(totalFrames)
	d := F / framesPer10Min
	m := F % framesPer10Min

	var Fp int
	if m < dropFrames {
		Fp = F + 9*dropFrames*d
	} else {
		Fp = F + 9*dropFrames*d + dropFrames*((m-dropFrames)/framesPerMin)
	}

	ff := Fp % nomFPS
	ss := (Fp / nomFPS) % 60
	mm := (Fp / (nomFPS * 60)) % 60
	hh := (Fp / (nomFPS * 3600)) % 24

	cntDropped := ff == dropFrames && ss == 0 && mm%10 != 0

	return Timecode{
		Hours: hh, Minutes: mm, Seconds: ss, Frames: ff,
		DropFrame: true, CntDropped: cntDropped,
	}
}

// countingType returns the H.264 counting_type value for a frame rate.
//   - 1: non-drop-frame
//   - 4: drop-frame (29.97, 59.94)
func countingType(fpsNum, fpsDen int) int {
	nomFPS := (fpsNum + fpsDen/2) / fpsDen
	if fpsDen == 1001 && nomFPS%30 == 0 {
		return 4
	}
	return 1
}
