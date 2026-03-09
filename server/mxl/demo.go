package mxl

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// DemoPattern selects the test pattern for DemoVideoReader.
type DemoPattern int

const (
	// PatternColorBars generates a horizontally-swept color bar pattern.
	PatternColorBars DemoPattern = iota
	// PatternGreenScreen generates a BT.709 green background with moving
	// white foreground elements (lower third + logo), suitable for chroma key demo.
	PatternGreenScreen
)

// DemoVideoReader generates V210 test pattern frames at a fixed frame rate.
// Implements DiscreteReader for use with Source orchestrator in demo mode.
// Width must be divisible by 6, height must be even.
type DemoVideoReader struct {
	width, height int
	colorIdx      int // base hue index (different per source)
	pattern       DemoPattern
	interval      time.Duration

	mu     sync.Mutex
	index  uint64
	closed bool
}

// NewDemoVideoReader creates a video reader that generates colored test
// patterns at the given dimensions and frame rate. colorIdx selects the
// base color (0=cyan, 1=magenta, 2=yellow, etc.) so multiple sources
// look visually distinct.
func NewDemoVideoReader(width, height int, fps float64, colorIdx int) *DemoVideoReader {
	return NewDemoVideoReaderWithPattern(width, height, fps, colorIdx, PatternColorBars)
}

// NewDemoVideoReaderWithPattern creates a video reader with a specific test pattern.
func NewDemoVideoReaderWithPattern(width, height int, fps float64, colorIdx int, pattern DemoPattern) *DemoVideoReader {
	return &DemoVideoReader{
		width:    width,
		height:   height,
		colorIdx: colorIdx,
		pattern:  pattern,
		interval: time.Duration(float64(time.Second) / fps),
	}
}

func (d *DemoVideoReader) ReadGrain(_ uint64, _ uint64) ([]byte, GrainInfo, error) {
	// Pace at frame rate.
	time.Sleep(d.interval)

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, GrainInfo{}, fmt.Errorf("mxl: demo reader closed")
	}
	d.index++
	idx := d.index
	d.mu.Unlock()

	// Generate YUV420p test pattern, then convert to V210.
	var yuv []byte
	switch d.pattern {
	case PatternGreenScreen:
		yuv = generateGreenScreenYUV420p(d.width, d.height, idx)
	default:
		yuv = generateDemoYUV420p(d.width, d.height, d.colorIdx, idx)
	}
	v210, err := YUV420pToV210(yuv, d.width, d.height)
	if err != nil {
		return nil, GrainInfo{}, fmt.Errorf("mxl: demo V210 conversion: %w", err)
	}

	return v210, GrainInfo{
		Index:       idx,
		GrainSize:   uint32(len(v210)),
		TotalSlices: 1,
		ValidSlices: 1,
	}, nil
}

func (d *DemoVideoReader) ConfigInfo() FlowConfig {
	return FlowConfig{
		Format:    DataFormatVideo,
		GrainRate: Rational{30, 1},
	}
}

func (d *DemoVideoReader) HeadIndex() (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.index, nil
}

func (d *DemoVideoReader) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

// DemoAudioReader generates a stereo test tone (440 Hz sine wave at -20 dBFS).
// Implements ContinuousReader.
type DemoAudioReader struct {
	sampleRate int
	channels   int
	interval   time.Duration

	index  atomic.Uint64
	closed atomic.Bool
}

// NewDemoAudioReader creates an audio reader that generates a 440 Hz sine tone at
// the given sample rate and channel count. The tone is at -20 dBFS so it's
// audible but not obnoxiously loud.
func NewDemoAudioReader(sampleRate, channels int) *DemoAudioReader {
	// ~23ms per read (1024 samples at 48kHz), matching AAC frame size.
	return &DemoAudioReader{
		sampleRate: sampleRate,
		channels:   channels,
		interval:   time.Duration(float64(time.Second) * 1024 / float64(sampleRate)),
	}
}

func (d *DemoAudioReader) ReadSamples(_ uint64, count int, _ uint64) ([][]float32, error) {
	time.Sleep(d.interval)

	if d.closed.Load() {
		return nil, fmt.Errorf("mxl: demo audio reader closed")
	}

	sampleOffset := d.index.Add(uint64(count)) - uint64(count)

	// Generate 440 Hz sine tone at -20 dBFS (de-interleaved).
	const (
		freq      = 440.0
		amplitude = 0.1 // -20 dBFS
	)
	channels := make([][]float32, d.channels)
	for ch := range channels {
		channels[ch] = make([]float32, count)
	}
	for i := 0; i < count; i++ {
		t := float64(sampleOffset+uint64(i)) / float64(d.sampleRate)
		sample := float32(amplitude * math.Sin(2*math.Pi*freq*t))
		for ch := range channels {
			channels[ch][i] = sample
		}
	}
	return channels, nil
}

func (d *DemoAudioReader) ConfigInfo() FlowConfig {
	return FlowConfig{
		Format:       DataFormatAudio,
		GrainRate:    Rational{int64(d.sampleRate), 1},
		ChannelCount: uint32(d.channels),
	}
}

func (d *DemoAudioReader) HeadIndex() (uint64, error) {
	return d.index.Load(), nil
}

func (d *DemoAudioReader) Close() error {
	d.closed.Store(true)
	return nil
}

// generateDemoYUV420p creates a YUV420p frame with a moving colored bar
// pattern. The color shifts based on colorIdx (per-source identity) and
// frameNum (animation). The pattern has horizontal bars with a vertical
// sweep line so you can see it's updating in real-time.
func generateDemoYUV420p(width, height, colorIdx int, frameNum uint64) []byte {
	ySize := width * height
	cw := width / 2
	ch := height / 2
	cSize := cw * ch
	buf := make([]byte, ySize+2*cSize)

	yPlane := buf[:ySize]
	cbPlane := buf[ySize : ySize+cSize]
	crPlane := buf[ySize+cSize:]

	// Moving sweep line position (wraps every ~4 seconds at 30fps).
	sweepX := int(frameNum*3) % width

	// Base color: rotate hue based on colorIdx.
	// Use YCbCr values directly to stay in-domain.
	type yuvColor struct{ y, cb, cr byte }
	colors := []yuvColor{
		{180, 170, 50}, // cyan-ish
		{100, 85, 212}, // magenta-ish
		{210, 16, 146}, // yellow-ish
		{80, 190, 120}, // teal-ish
	}
	baseColor := colors[colorIdx%len(colors)]

	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			// Horizontal gradient: Y varies from dark to bright across width.
			baseY := byte(16 + (219 * col / width))

			// Add per-source color tint via Y modulation.
			tint := float64(baseColor.y) / 255.0
			y := byte(float64(baseY)*0.6 + float64(baseColor.y)*0.4)

			// Sweep line: bright white vertical bar.
			dist := col - sweepX
			if dist < 0 {
				dist = -dist
			}
			if dist < 3 {
				y = 235
			}

			_ = tint
			yPlane[row*width+col] = y
		}
	}

	// Chroma planes: constant per-source color.
	// Modulate slightly with a slow sine wave for visual interest.
	phase := float64(frameNum) * 0.05
	cbMod := byte(math.Max(16, math.Min(240, float64(baseColor.cb)+10*math.Sin(phase))))
	crMod := byte(math.Max(16, math.Min(240, float64(baseColor.cr)+10*math.Cos(phase))))

	for i := 0; i < cSize; i++ {
		cbPlane[i] = cbMod
		crPlane[i] = crMod
	}

	return buf
}

// generateGreenScreenYUV420p creates a YUV420p frame with a BT.709 green
// background and white foreground elements suitable for chroma keying:
//   - Solid green background (Y=173, Cb=42, Cr=26 — matches UI "Green Screen" preset)
//   - Moving white rectangle (~width/4 wide, ~height/6 tall) sweeping horizontally
//     near the bottom third — simulates a "lower third" overlay
//   - Static white square in top-right corner — simulates a "logo" bug
func generateGreenScreenYUV420p(width, height int, frameNum uint64) []byte {
	ySize := width * height
	cw := width / 2
	ch := height / 2
	cSize := cw * ch
	buf := make([]byte, ySize+2*cSize)

	yPlane := buf[:ySize]
	cbPlane := buf[ySize : ySize+cSize]
	crPlane := buf[ySize+cSize:]

	// BT.709 green: Y=173, Cb=42, Cr=26 (matches UI green screen preset).
	const (
		greenY  = 173
		greenCb = 42
		greenCr = 26
		whiteY  = 235
		whiteCb = 128
		whiteCr = 128
	)

	// Lower third: rectangle sweeping left-to-right, positioned in bottom quarter.
	lowerW := width / 4
	lowerH := height / 6
	lowerY0 := height - lowerH - height/8 // offset up from very bottom
	lowerY1 := lowerY0 + lowerH
	// Sweep across width, wrapping every ~4 seconds at 30fps.
	lowerX0 := int(frameNum*3) % (width + lowerW) - lowerW
	lowerX1 := lowerX0 + lowerW

	// Logo: static square in top-right corner.
	logoSize := width / 8
	if logoSize < 4 {
		logoSize = 4
	}
	logoX0 := width - logoSize - 4
	logoY0 := 4
	logoX1 := logoX0 + logoSize
	logoY1 := logoY0 + logoSize

	// Fill luma plane.
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			// Check if pixel is in the lower third rectangle.
			inLower := row >= lowerY0 && row < lowerY1 && col >= lowerX0 && col < lowerX1
			// Check if pixel is in the logo.
			inLogo := row >= logoY0 && row < logoY1 && col >= logoX0 && col < logoX1

			if inLower || inLogo {
				yPlane[row*width+col] = whiteY
			} else {
				yPlane[row*width+col] = greenY
			}
		}
	}

	// Fill chroma planes at half resolution.
	for crow := 0; crow < ch; crow++ {
		for ccol := 0; ccol < cw; ccol++ {
			// Map chroma pixel to luma (2x2 block).
			lumaRow := crow * 2
			lumaCol := ccol * 2

			inLower := lumaRow >= lowerY0 && lumaRow < lowerY1 && lumaCol >= lowerX0 && lumaCol < lowerX1
			inLogo := lumaRow >= logoY0 && lumaRow < logoY1 && lumaCol >= logoX0 && lumaCol < logoX1

			idx := crow*cw + ccol
			if inLower || inLogo {
				cbPlane[idx] = whiteCb
				crPlane[idx] = whiteCr
			} else {
				cbPlane[idx] = greenCb
				crPlane[idx] = greenCr
			}
		}
	}

	return buf
}
