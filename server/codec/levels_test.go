//go:build cgo && !noffmpeg

package codec

import "testing"

func TestAVCLevel(t *testing.T) {
	for _, tc := range []struct {
		w, h, num, den int
		want           int
	}{
		{1280, 720, 30, 1, 31},
		// 720p above 30 fps is past level 3.1's macroblock rate; NVENC
		// refuses to open a level 3.1 encoder for it.
		{1280, 720, 50, 1, 32},
		{1280, 720, 60000, 1001, 32},
		{852, 480, 50, 1, 31},
		{1920, 1080, 30000, 1001, 40},
		{1920, 1080, 50, 1, 42},
		{1920, 1080, 60, 1, 42},
		{3840, 2160, 30, 1, 51},
		{3840, 2160, 60, 1, 52},
	} {
		if got := avcLevel(tc.w, tc.h, tc.num, tc.den); got != tc.want {
			t.Errorf("avcLevel(%dx%d@%d/%d) = %d, want %d", tc.w, tc.h, tc.num, tc.den, got, tc.want)
		}
	}
}

func TestHEVCLevel(t *testing.T) {
	for _, tc := range []struct {
		w, h, num, den int
		want           int
	}{
		{1280, 720, 30, 1, 93},
		{1280, 720, 50, 1, 120},
		{1920, 1080, 30, 1, 120},
		{1920, 1080, 60, 1, 123},
		{3840, 2160, 60, 1, 153},
	} {
		if got := hevcLevel(tc.w, tc.h, tc.num, tc.den); got != tc.want {
			t.Errorf("hevcLevel(%dx%d@%d/%d) = %d, want %d", tc.w, tc.h, tc.num, tc.den, got, tc.want)
		}
	}
}
