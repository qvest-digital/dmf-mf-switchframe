package stmap

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

// ReadPNG decodes a 16-bit PNG and extracts R and G channels as normalized
// float32 S and T coordinates. The image must have even dimensions.
// 8-bit PNGs are also accepted (but with lower precision).
func ReadPNG(data []byte, name string) (*STMap, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("stmap: decode PNG: %w", err)
	}

	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 {
		return nil, ErrInvalidDimensions
	}

	n := w * h
	s := make([]float32, n)
	t := make([]float32, n)

	switch src := img.(type) {
	case *image.NRGBA64:
		for y := 0; y < h; y++ {
			row := y * w
			for x := 0; x < w; x++ {
				c := src.NRGBA64At(x+bounds.Min.X, y+bounds.Min.Y)
				s[row+x] = float32(c.R) / 65535.0
				t[row+x] = float32(c.G) / 65535.0
			}
		}
	case *image.RGBA64:
		for y := 0; y < h; y++ {
			row := y * w
			for x := 0; x < w; x++ {
				c := src.RGBA64At(x+bounds.Min.X, y+bounds.Min.Y)
				// RGBA64 is premultiplied; un-premultiply if alpha is non-zero.
				if c.A == 0 {
					s[row+x] = 0
					t[row+x] = 0
				} else {
					s[row+x] = float32(c.R) / float32(c.A)
					t[row+x] = float32(c.G) / float32(c.A)
				}
			}
		}
	case *image.NRGBA:
		for y := 0; y < h; y++ {
			row := y * w
			for x := 0; x < w; x++ {
				c := src.NRGBAAt(x+bounds.Min.X, y+bounds.Min.Y)
				s[row+x] = float32(c.R) / 255.0
				t[row+x] = float32(c.G) / 255.0
			}
		}
	case *image.RGBA:
		for y := 0; y < h; y++ {
			row := y * w
			for x := 0; x < w; x++ {
				c := src.RGBAAt(x+bounds.Min.X, y+bounds.Min.Y)
				// RGBA is premultiplied; un-premultiply if alpha is non-zero.
				if c.A == 0 {
					s[row+x] = 0
					t[row+x] = 0
				} else {
					s[row+x] = float32(c.R) / float32(c.A)
					t[row+x] = float32(c.G) / float32(c.A)
				}
			}
		}
	default:
		// Generic fallback: use color.NRGBA64Model.Convert().
		for y := 0; y < h; y++ {
			row := y * w
			for x := 0; x < w; x++ {
				c := color.NRGBA64Model.Convert(src.At(x+bounds.Min.X, y+bounds.Min.Y)).(color.NRGBA64)
				s[row+x] = float32(c.R) / 65535.0
				t[row+x] = float32(c.G) / 65535.0
			}
		}
	}

	return &STMap{
		Name:   name,
		Width:  w,
		Height: h,
		S:      s,
		T:      t,
	}, nil
}
