package mxl

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// DemoVideoReader generates V210 test pattern frames at a fixed frame rate.
// Implements DiscreteReader for use with Source orchestrator in demo mode.
// Width must be divisible by 6, height must be even.
type DemoVideoReader struct {
	width, height int
	colorIdx      int // base hue index (different per source)
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
	return &DemoVideoReader{
		width:    width,
		height:   height,
		colorIdx: colorIdx,
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
	yuv := generateDemoYUV420p(d.width, d.height, d.colorIdx, idx)
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

// DemoAudioReader generates silent (zero-filled) stereo PCM audio.
// Implements ContinuousReader.
type DemoAudioReader struct {
	sampleRate int
	channels   int
	interval   time.Duration

	index  atomic.Uint64
	closed atomic.Bool
}

// NewDemoAudioReader creates an audio reader that generates silence at
// the given sample rate and channel count.
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

	d.index.Add(uint64(count))

	// Generate silence (de-interleaved).
	channels := make([][]float32, d.channels)
	for ch := range channels {
		channels[ch] = make([]float32, count)
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
		{180, 170, 50},  // cyan-ish
		{100, 85, 212},  // magenta-ish
		{210, 16, 146},  // yellow-ish
		{80, 190, 120},  // teal-ish
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
