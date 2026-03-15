package demo

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

// GenerateStingerZip creates a zip of PNG frames for a diagonal-wipe stinger.
// A dark blue-gray diagonal bar sweeps from top-left to bottom-right.
// Returns zip bytes compatible with stinger.Store.Upload().
func GenerateStingerZip(width, height, numFrames int) ([]byte, error) {
	if width <= 0 || height <= 0 || numFrames <= 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d, %d frames", width, height, numFrames)
	}
	if width%2 != 0 || height%2 != 0 {
		return nil, fmt.Errorf("dimensions must be even for YUV420: %dx%d", width, height)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	maxDiag := float64(width + height)
	const softEdge = 20.0
	// Dark blue-gray fill color
	fill := color.NRGBA{R: 30, G: 35, B: 50, A: 255}

	for i := 0; i < numFrames; i++ {
		var progress float64
		if numFrames > 1 {
			progress = float64(i) / float64(numFrames-1)
		}
		frontPos := progress * maxDiag

		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				diag := float64(x + y)
				if diag < frontPos-softEdge {
					// Behind front: fully opaque
					img.SetNRGBA(x, y, fill)
				} else if diag < frontPos {
					// Soft edge gradient
					alpha := 1.0 - (frontPos-diag)/softEdge
					if alpha < 0 {
						alpha = 0
					}
					img.SetNRGBA(x, y, color.NRGBA{
						R: fill.R, G: fill.G, B: fill.B,
						A: uint8(alpha * 255),
					})
				}
				// else: fully transparent (zero value)
			}
		}

		name := fmt.Sprintf("frame_%03d.png", i)
		w, err := zw.Create(name)
		if err != nil {
			return nil, fmt.Errorf("create zip entry %s: %w", name, err)
		}
		if err := png.Encode(w, img); err != nil {
			return nil, fmt.Errorf("encode PNG %s: %w", name, err)
		}
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}

	return buf.Bytes(), nil
}
