package transition_test

import (
	"fmt"

	"github.com/zsiec/switchframe/server/transition"
)

func ExampleYUV420ToRGB() {
	// 2x2 pure white frame in YUV420 full-range.
	// Y=255 for all pixels, Cb=128 (neutral), Cr=128 (neutral).
	yuv := []byte{
		255, 255, 255, 255, // Y plane (2x2)
		128, // Cb plane (1x1, subsampled)
		128, // Cr plane (1x1, subsampled)
	}
	rgb := make([]byte, 2*2*3)

	transition.YUV420ToRGB(yuv, 2, 2, rgb)

	// With neutral chroma, Y=255 maps to RGB(255, 255, 255).
	fmt.Printf("pixel[0]: R=%d G=%d B=%d\n", rgb[0], rgb[1], rgb[2])
	fmt.Printf("pixel[3]: R=%d G=%d B=%d\n", rgb[9], rgb[10], rgb[11])
	// Output:
	// pixel[0]: R=255 G=255 B=255
	// pixel[3]: R=255 G=255 B=255
}
