package caption

import (
	"testing"
	"time"
)

func makeUTC(h, m, s, ns int) time.Time {
	return time.Date(2026, 3, 29, h, m, s, ns, time.UTC)
}

// nanosForFrame returns the nanosecond offset for a given frame number,
// using ceiling division so the result always lands on frame N (not N-1).
func nanosForFrame(frame int64, fpsNum, fpsDen int64) int64 {
	num := frame * fpsDen * 1_000_000_000
	return (num + fpsNum - 1) / fpsNum
}

// --- NDF tests ---

func TestUTCToTimecode_NDF_30fps_Midnight(t *testing.T) {
	tc := UTCToTimecode(makeUTC(0, 0, 0, 0), 30, 1)
	if tc.Hours != 0 || tc.Minutes != 0 || tc.Seconds != 0 || tc.Frames != 0 {
		t.Errorf("got %d:%02d:%02d:%02d, want 00:00:00:00", tc.Hours, tc.Minutes, tc.Seconds, tc.Frames)
	}
	if tc.DropFrame {
		t.Error("30fps should be NDF")
	}
}

func TestUTCToTimecode_NDF_30fps_OneFrame(t *testing.T) {
	// 1/30s = 33333333.33ns; use ceiling to land on frame 1
	tc := UTCToTimecode(makeUTC(0, 0, 0, 33333334), 30, 1)
	if tc.Frames != 1 {
		t.Errorf("frames = %d, want 1", tc.Frames)
	}
}

func TestUTCToTimecode_NDF_30fps_OneSecond(t *testing.T) {
	tc := UTCToTimecode(makeUTC(0, 0, 1, 0), 30, 1)
	if tc.Seconds != 1 || tc.Frames != 0 {
		t.Errorf("got %d:%02d:%02d:%02d, want 00:00:01:00", tc.Hours, tc.Minutes, tc.Seconds, tc.Frames)
	}
}

func TestUTCToTimecode_NDF_25fps(t *testing.T) {
	tc := UTCToTimecode(makeUTC(12, 30, 45, 0), 25, 1)
	if tc.Hours != 12 || tc.Minutes != 30 || tc.Seconds != 45 || tc.Frames != 0 {
		t.Errorf("got %d:%02d:%02d:%02d, want 12:30:45:00", tc.Hours, tc.Minutes, tc.Seconds, tc.Frames)
	}
	if tc.DropFrame {
		t.Error("25fps should be NDF")
	}
}

func TestUTCToTimecode_NDF_60fps(t *testing.T) {
	tc := UTCToTimecode(makeUTC(0, 0, 0, 500000000), 60, 1)
	if tc.Frames != 30 {
		t.Errorf("frames = %d, want 30", tc.Frames)
	}
}

func TestUTCToTimecode_NDF_24fps(t *testing.T) {
	tc := UTCToTimecode(makeUTC(1, 0, 0, 0), 24, 1)
	if tc.Hours != 1 || tc.Minutes != 0 || tc.Seconds != 0 || tc.Frames != 0 {
		t.Errorf("got %d:%02d:%02d:%02d, want 01:00:00:00", tc.Hours, tc.Minutes, tc.Seconds, tc.Frames)
	}
}

func TestUTCToTimecode_NDF_23976(t *testing.T) {
	tc := UTCToTimecode(makeUTC(0, 0, 0, 0), 24000, 1001)
	if tc.DropFrame {
		t.Error("23.976fps should be NDF (DF not defined for 24fps)")
	}
}

func TestUTCToTimecode_NDF_MidnightWrap(t *testing.T) {
	// 29/30s = 966666666.67ns; use ceiling to land on frame 29
	tc := UTCToTimecode(makeUTC(23, 59, 59, 966666667), 30, 1)
	if tc.Hours != 23 || tc.Minutes != 59 || tc.Seconds != 59 || tc.Frames != 29 {
		t.Errorf("got %d:%02d:%02d:%02d, want 23:59:59:29", tc.Hours, tc.Minutes, tc.Seconds, tc.Frames)
	}
}

// --- DF tests ---

func TestUTCToTimecode_DF_2997_Midnight(t *testing.T) {
	tc := UTCToTimecode(makeUTC(0, 0, 0, 0), 30000, 1001)
	if tc.Hours != 0 || tc.Minutes != 0 || tc.Seconds != 0 || tc.Frames != 0 {
		t.Errorf("got %d:%02d:%02d;%02d, want 00:00:00;00", tc.Hours, tc.Minutes, tc.Seconds, tc.Frames)
	}
	if !tc.DropFrame {
		t.Error("29.97fps should be DF")
	}
}

func TestUTCToTimecode_DF_2997_LastFrameMinute0(t *testing.T) {
	// Frame 1799 at 29.97fps is the last of the 1800-label first minute.
	// TC should be 00:00:59;29.
	nanos := nanosForFrame(1799, 30000, 1001)
	tc := UTCToTimecode(makeUTC(0, 0, 0, 0).Add(time.Duration(nanos)), 30000, 1001)
	if tc.Minutes != 0 || tc.Seconds != 59 || tc.Frames != 29 {
		t.Errorf("got %d:%02d:%02d;%02d, want 00:00:59;29", tc.Hours, tc.Minutes, tc.Seconds, tc.Frames)
	}
	if tc.CntDropped {
		t.Error("should not be cnt_dropped at last frame of minute 0")
	}
}

func TestUTCToTimecode_DF_2997_FirstFrameMinute1(t *testing.T) {
	// Frame 1800 at 29.97fps. TC should be 00:01:00;02 (frames 0,1 dropped).
	nanos := nanosForFrame(1800, 30000, 1001)
	tc := UTCToTimecode(makeUTC(0, 0, 0, 0).Add(time.Duration(nanos)), 30000, 1001)
	if tc.Minutes != 1 || tc.Seconds != 0 || tc.Frames != 2 {
		t.Errorf("got %d:%02d:%02d;%02d, want 00:01:00;02", tc.Hours, tc.Minutes, tc.Seconds, tc.Frames)
	}
	if !tc.CntDropped {
		t.Error("should be cnt_dropped at first frame after drop")
	}
}

func TestUTCToTimecode_DF_2997_DecadeMinuteNoDrop(t *testing.T) {
	// Frame 17982 = start of minute 10 (decade). TC should be 00:10:00;00.
	nanos := nanosForFrame(17982, 30000, 1001)
	tc := UTCToTimecode(makeUTC(0, 0, 0, 0).Add(time.Duration(nanos)), 30000, 1001)
	if tc.Minutes != 10 || tc.Seconds != 0 || tc.Frames != 0 {
		t.Errorf("got %d:%02d:%02d;%02d, want 00:10:00;00", tc.Hours, tc.Minutes, tc.Seconds, tc.Frames)
	}
	if tc.CntDropped {
		t.Error("decade minutes should NOT have cnt_dropped")
	}
}

func TestUTCToTimecode_DF_5994(t *testing.T) {
	tc := UTCToTimecode(makeUTC(0, 0, 0, 0), 60000, 1001)
	if !tc.DropFrame {
		t.Error("59.94fps should be DF")
	}
	if tc.Frames != 0 {
		t.Errorf("frames = %d, want 0 at midnight", tc.Frames)
	}
}

func TestUTCToTimecode_DF_5994_FirstFrameMinute1(t *testing.T) {
	// Frame 3600 at 59.94fps. TC should be 00:01:00;04 (frames 0-3 dropped).
	nanos := nanosForFrame(3600, 60000, 1001)
	tc := UTCToTimecode(makeUTC(0, 0, 0, 0).Add(time.Duration(nanos)), 60000, 1001)
	if tc.Minutes != 1 || tc.Seconds != 0 || tc.Frames != 4 {
		t.Errorf("got %d:%02d:%02d;%02d, want 00:01:00;04", tc.Hours, tc.Minutes, tc.Seconds, tc.Frames)
	}
	if !tc.CntDropped {
		t.Error("should be cnt_dropped")
	}
}

func TestUTCToTimecode_DF_2997_OneHour(t *testing.T) {
	// One hour at 29.97fps = 107892 frames. TC should be 01:00:00;00.
	nanos := nanosForFrame(107892, 30000, 1001)
	tc := UTCToTimecode(makeUTC(0, 0, 0, 0).Add(time.Duration(nanos)), 30000, 1001)
	if tc.Hours != 1 || tc.Minutes != 0 || tc.Seconds != 0 || tc.Frames != 0 {
		t.Errorf("got %d:%02d:%02d;%02d, want 01:00:00;00", tc.Hours, tc.Minutes, tc.Seconds, tc.Frames)
	}
}

func TestCountingType(t *testing.T) {
	tests := []struct {
		fpsNum, fpsDen int
		want           int
	}{
		{30, 1, 1},
		{30000, 1001, 4},
		{60, 1, 1},
		{60000, 1001, 4},
		{25, 1, 1},
		{24, 1, 1},
		{24000, 1001, 1},
	}
	for _, tt := range tests {
		got := countingType(tt.fpsNum, tt.fpsDen)
		if got != tt.want {
			t.Errorf("countingType(%d/%d) = %d, want %d", tt.fpsNum, tt.fpsDen, got, tt.want)
		}
	}
}
