package demo

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

// GenerateWhooshStingerZip creates a zip of PNGs + WAV for a diagonal sweep stinger.
// Visual: a semi-transparent gradient bar sweeps diagonally from top-left to bottom-right
// with a wide soft edge (motion blur trail). Audio: frequency sweep whoosh.
func GenerateWhooshStingerZip(width, height, numFrames int) ([]byte, error) {
	if width <= 0 || height <= 0 || numFrames <= 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d, %d frames", width, height, numFrames)
	}
	if width%2 != 0 || height%2 != 0 {
		return nil, fmt.Errorf("dimensions must be even for YUV420: %dx%d", width, height)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	maxDiag := float64(width + height)
	const trailingEdge = 50.0 // wider trailing edge for motion blur feel
	const leadingEdge = 10.0  // sharper leading edge
	fill := color.NRGBA{R: 30, G: 35, B: 50, A: 255}

	for i := 0; i < numFrames; i++ {
		progress := float64(i) / float64(numFrames-1)
		frontPos := progress * (maxDiag + trailingEdge)

		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				diag := float64(x + y)

				if diag < frontPos-trailingEdge {
					// Behind the trailing edge: fully opaque
					img.SetNRGBA(x, y, fill)
				} else if diag < frontPos {
					// Trailing edge: long gradient (motion blur)
					alpha := (frontPos - diag) / trailingEdge
					// Smooth the alpha with a quadratic curve for softer look
					alpha = alpha * alpha
					if alpha > 1.0 {
						alpha = 1.0
					}
					img.SetNRGBA(x, y, color.NRGBA{
						R: fill.R, G: fill.G, B: fill.B,
						A: uint8(alpha * 255),
					})
				} else if diag < frontPos+leadingEdge {
					// Leading edge: sharper gradient ahead of front
					alpha := 1.0 - (diag-frontPos)/leadingEdge
					if alpha < 0 {
						alpha = 0
					}
					img.SetNRGBA(x, y, color.NRGBA{
						R: fill.R, G: fill.G, B: fill.B,
						A: uint8(alpha * 255),
					})
				}
				// else: fully transparent
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

	// Generate and include audio
	durationSec := float64(numFrames) / 30.0
	pcm := SynthesizeWhoosh(48000, 2, durationSec)
	wavData := GenerateWAV(pcm, 48000, 2)
	w, err := zw.Create("audio.wav")
	if err != nil {
		return nil, fmt.Errorf("create audio entry: %w", err)
	}
	if _, err := w.Write(wavData); err != nil {
		return nil, fmt.Errorf("write audio: %w", err)
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}
	return buf.Bytes(), nil
}

// GenerateSlamStingerZip creates a zip of PNGs + WAV for a radial burst stinger.
// Visual: circle expanding from center outward with a bright flash at the start.
// Audio: percussive impact slam.
func GenerateSlamStingerZip(width, height, numFrames int) ([]byte, error) {
	if width <= 0 || height <= 0 || numFrames <= 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d, %d frames", width, height, numFrames)
	}
	if width%2 != 0 || height%2 != 0 {
		return nil, fmt.Errorf("dimensions must be even for YUV420: %dx%d", width, height)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	cx := float64(width) / 2.0
	cy := float64(height) / 2.0
	// Maximum radius needed to cover entire frame from center
	maxRadius := math.Sqrt(cx*cx + cy*cy)
	const edgeSoftness = 30.0
	fill := color.NRGBA{R: 50, G: 25, B: 25, A: 255}
	flashColor := color.NRGBA{R: 240, G: 230, B: 220, A: 255}

	for i := 0; i < numFrames; i++ {
		progress := float64(i) / float64(numFrames-1)
		img := image.NewNRGBA(image.Rect(0, 0, width, height))

		// Flash phase: first 15% of frames
		isFlash := progress < 0.15

		// Current ring radius expands from 0 to maxRadius
		radius := progress * maxRadius

		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				dx := float64(x) - cx
				dy := float64(y) - cy
				dist := math.Sqrt(dx*dx + dy*dy)

				if isFlash {
					// Bright flash from center, fading outward
					flashProgress := progress / 0.15 // 0..1 within flash phase
					flashRadius := flashProgress * maxRadius * 0.4
					if dist < flashRadius {
						// Blend between flash color and fill color based on flash progress
						flashAlpha := 1.0 - dist/flashRadius
						flashAlpha *= (1.0 - flashProgress) // fade flash out over time
						fillAlpha := 1.0
						// Inside the expanding circle: fill + flash overlay
						r := uint8(float64(fill.R)*(1-flashAlpha) + float64(flashColor.R)*flashAlpha)
						g := uint8(float64(fill.G)*(1-flashAlpha) + float64(flashColor.G)*flashAlpha)
						b := uint8(float64(fill.B)*(1-flashAlpha) + float64(flashColor.B)*flashAlpha)
						img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: uint8(fillAlpha * 255)})
					} else if dist < radius {
						// Inside circle but outside flash: normal fill
						img.SetNRGBA(x, y, fill)
					} else if dist < radius+edgeSoftness {
						// Feathered edge
						alpha := 1.0 - (dist-radius)/edgeSoftness
						if alpha < 0 {
							alpha = 0
						}
						img.SetNRGBA(x, y, color.NRGBA{
							R: fill.R, G: fill.G, B: fill.B,
							A: uint8(alpha * 255),
						})
					}
				} else {
					// Normal expanding ring
					if dist < radius {
						// Inside ring: fully opaque
						img.SetNRGBA(x, y, fill)
					} else if dist < radius+edgeSoftness {
						// Feathered edge
						alpha := 1.0 - (dist-radius)/edgeSoftness
						if alpha < 0 {
							alpha = 0
						}
						img.SetNRGBA(x, y, color.NRGBA{
							R: fill.R, G: fill.G, B: fill.B,
							A: uint8(alpha * 255),
						})
					}
					// else: transparent
				}
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

	// Generate and include audio
	durationSec := float64(numFrames) / 30.0
	pcm := SynthesizeSlam(48000, 2, durationSec)
	wavData := GenerateWAV(pcm, 48000, 2)
	w, err := zw.Create("audio.wav")
	if err != nil {
		return nil, fmt.Errorf("create audio entry: %w", err)
	}
	if _, err := w.Write(wavData); err != nil {
		return nil, fmt.Errorf("write audio: %w", err)
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}
	return buf.Bytes(), nil
}

// musicalEllipse describes a floating elliptical shape for the musical stinger.
type musicalEllipse struct {
	// Center position offsets from frame center, as fraction of width/height
	cxFrac, cyFrac float64
	// Radius fractions at start and end of animation
	rxStart, ryStart float64
	rxEnd, ryEnd     float64
	// Alpha multiplier (overall opacity)
	alphaMul float64
}

// GenerateMusicalStingerZip creates a zip of PNGs + WAV for a floating shapes stinger.
// Visual: multiple elliptical shapes grow and float with gaussian-like alpha gradients.
// Audio: major chord sting.
func GenerateMusicalStingerZip(width, height, numFrames int) ([]byte, error) {
	if width <= 0 || height <= 0 || numFrames <= 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d, %d frames", width, height, numFrames)
	}
	if width%2 != 0 || height%2 != 0 {
		return nil, fmt.Errorf("dimensions must be even for YUV420: %dx%d", width, height)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	fill := color.NRGBA{R: 40, G: 30, B: 60, A: 255}

	// Define 4 ellipses with different positions and growth rates
	ellipses := []musicalEllipse{
		{cxFrac: -0.15, cyFrac: -0.10, rxStart: 0.05, ryStart: 0.08, rxEnd: 0.55, ryEnd: 0.65, alphaMul: 0.9},
		{cxFrac: 0.20, cyFrac: 0.15, rxStart: 0.04, ryStart: 0.06, rxEnd: 0.60, ryEnd: 0.50, alphaMul: 0.8},
		{cxFrac: -0.10, cyFrac: 0.20, rxStart: 0.06, ryStart: 0.04, rxEnd: 0.50, ryEnd: 0.55, alphaMul: 0.85},
		{cxFrac: 0.15, cyFrac: -0.15, rxStart: 0.03, ryStart: 0.05, rxEnd: 0.45, ryEnd: 0.60, alphaMul: 0.75},
	}

	w64 := float64(width)
	h64 := float64(height)
	cx := w64 / 2.0
	cy := h64 / 2.0

	for i := 0; i < numFrames; i++ {
		progress := float64(i) / float64(numFrames-1)
		// Use smoothstep for organic growth
		t := progress * progress * (3.0 - 2.0*progress)

		img := image.NewNRGBA(image.Rect(0, 0, width, height))

		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				// Accumulate alpha from all ellipses
				var totalAlpha float64

				for _, e := range ellipses {
					// Ellipse center (shifts slightly with progress for floating effect)
					ecx := cx + e.cxFrac*w64 + e.cxFrac*w64*0.3*t
					ecy := cy + e.cyFrac*h64 + e.cyFrac*h64*0.3*t

					// Interpolate radius
					rx := (e.rxStart + (e.rxEnd-e.rxStart)*t) * w64
					ry := (e.ryStart + (e.ryEnd-e.ryStart)*t) * h64

					if rx < 1 {
						rx = 1
					}
					if ry < 1 {
						ry = 1
					}

					// Normalized distance from ellipse center
					dx := (float64(x) - ecx) / rx
					dy := (float64(y) - ecy) / ry
					distSq := dx*dx + dy*dy

					if distSq < 4.0 { // Only compute within 2 radii (gaussian drops off quickly)
						// Gaussian falloff: exp(-dist^2 * sigma)
						alpha := math.Exp(-distSq*1.5) * e.alphaMul
						totalAlpha += alpha
					}
				}

				if totalAlpha > 0 {
					// Clamp to 1.0
					if totalAlpha > 1.0 {
						totalAlpha = 1.0
					}
					img.SetNRGBA(x, y, color.NRGBA{
						R: fill.R, G: fill.G, B: fill.B,
						A: uint8(totalAlpha * 255),
					})
				}
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

	// Generate and include audio
	durationSec := float64(numFrames) / 30.0
	pcm := SynthesizeMusical(48000, 2, durationSec)
	wavData := GenerateWAV(pcm, 48000, 2)
	w, err := zw.Create("audio.wav")
	if err != nil {
		return nil, fmt.Errorf("create audio entry: %w", err)
	}
	if _, err := w.Write(wavData); err != nil {
		return nil, fmt.Errorf("write audio: %w", err)
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}
	return buf.Bytes(), nil
}
