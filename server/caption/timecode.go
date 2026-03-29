package caption

import "time"

// Timecode represents an SMPTE 12M timecode value.
type Timecode struct {
	Hours      int
	Minutes    int
	Seconds    int
	Frames     int
	DropFrame  bool // true for 29.97/59.94
	CntDropped bool // true on first frame after a DF skip
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
