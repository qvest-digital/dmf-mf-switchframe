package mxl

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The program MXL output writes video at the presets' 1280x720, 1920x1080
// and 3840x2160, at the 2048 and 4096 widths of DCI and the 720 of SD, at
// 8 and 10 bits and at 23.976 to 240 frames a second. 1280, 2048 and 4096
// end each v210 row in a partial group; before
// v210 carried such widths the writer dropped every frame it was handed and
// the flow carried no video at all.
func TestOutput_WritesVideoAtEveryFormat(t *testing.T) {
	sizes := []struct{ w, h int }{{1280, 720}, {1920, 1080}, {3840, 2160}, {2048, 1080}, {4096, 2160}, {720, 576}}
	rates := []Rational{{24000, 1001}, {24, 1}, {25, 1}, {30000, 1001}, {30, 1}, {50, 1}, {60000, 1001}, {60, 1}, {120, 1}, {240, 1}}
	for _, size := range sizes {
		w, h := size.w, size.h
		yuv8 := make([]byte, w*h*3/2)
		for i := range yuv8 {
			yuv8[i] = byte(16 + i%200)
		}
		want8, err := YUV420pToV210(yuv8, w, h)
		if err != nil {
			t.Fatalf("%dx%d: %v", w, h, err)
		}
		yuv10 := yuv422p10Ramp(w, h)
		want10, err := YUV422P10ToV210(yuv10, w, h)
		if err != nil {
			t.Fatalf("%dx%d: %v", w, h, err)
		}
		for _, rate := range rates {
			for _, depth := range []int{8, 10} {
				t.Run(fmt.Sprintf("%dx%d@%d/%d/%dbit", w, h, rate.Numerator, rate.Denominator, depth), func(t *testing.T) {
					mock := &mockDiscreteWriter{}
					out := NewOutput(OutputConfig{Width: w, Height: h, VideoRate: rate})
					out.StartLifecycle(context.Background(), mock, nil)
					t.Cleanup(out.Stop)

					index := CurrentIndex(rate) + 1
					want := want8
					if depth == 10 {
						want = want10
						out.Writer().WriteVideo10bit(yuv10, w, h, framePTS(rate, index))
					} else {
						out.Writer().WriteVideo(yuv8, w, h, framePTS(rate, index))
					}
					deadline := time.Now().Add(2 * time.Second)
					for len(mock.getGrains()) == 0 {
						if time.Now().After(deadline) {
							t.Fatal("no video grain written")
						}
						time.Sleep(time.Millisecond)
					}
					g := mock.getGrains()[0]
					if g.index != index {
						t.Fatalf("grain written at %d, want %d, the index of the frame's PTS", g.index, index)
					}
					if string(g.data) != string(want) {
						t.Fatal("grain is not the frame's v210")
					}
				})
			}
		}
	}
}
