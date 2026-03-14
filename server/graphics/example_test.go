package graphics_test

import (
	"fmt"

	"github.com/zsiec/switchframe/server/graphics"
)

func ExampleAlphaBlendRGBA() {
	// 2x2 black frame in YUV420: Y=16 (limited-range black), Cb=128, Cr=128.
	yuv := []byte{
		16, 16, 16, 16, // Y plane (2x2)
		128, // Cb plane (1x1, subsampled)
		128, // Cr plane (1x1, subsampled)
	}

	// Fully opaque white RGBA overlay (2x2).
	rgba := []byte{
		255, 255, 255, 255, // pixel 0: white, alpha=255
		255, 255, 255, 255, // pixel 1
		255, 255, 255, 255, // pixel 2
		255, 255, 255, 255, // pixel 3
	}

	graphics.AlphaBlendRGBA(yuv, rgba, 2, 2, 1.0)

	// After blending, the frame should be limited-range white: Y=235
	// (limited-range BT.709: 16 + ((47+157+16)*255+128)>>8 = 235), Cb=128, Cr=128.
	fmt.Printf("Y=[%d %d %d %d] Cb=%d Cr=%d\n",
		yuv[0], yuv[1], yuv[2], yuv[3], yuv[4], yuv[5])
	// Output:
	// Y=[235 235 235 235] Cb=128 Cr=128
}
