package graphics_test

import (
	"fmt"

	"github.com/zsiec/switchframe/server/graphics"
)

func ExampleAlphaBlendRGBA() {
	// 2x2 black frame in YUV420: Y=0, Cb=128, Cr=128.
	yuv := []byte{
		0, 0, 0, 0, // Y plane (2x2)
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

	// After blending, the frame should be white: Y=254 (integer BT.709
	// coefficients 54+183+18=255 produce 254 for pure white), Cb=128, Cr=128.
	fmt.Printf("Y=[%d %d %d %d] Cb=%d Cr=%d\n",
		yuv[0], yuv[1], yuv[2], yuv[3], yuv[4], yuv[5])
	// Output:
	// Y=[254 254 254 254] Cb=128 Cr=128
}
