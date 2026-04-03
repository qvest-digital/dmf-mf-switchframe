package mxl

import (
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeV210Frame10bit creates a V210 frame with known 10-bit values.
// The Y values are set to 400 (10-bit range), which is distinguishable from
// the 8-bit-quantized equivalent (400>>2=100, 100<<2=400 — exact in this case,
// but chroma subsampling to 4:2:0 is lossy).
func makeV210Frame10bit(width, height int) []byte {
	// Create a V210 frame from known 10-bit YUV422P10 values.
	chromaW := width / 2
	ySize := width * height
	cbSize := chromaW * height
	crSize := chromaW * height
	totalBytes := (ySize + cbSize + crSize) * 2

	yuv422p10 := make([]byte, totalBytes)
	// Y plane: 400 (distinguishable 10-bit value)
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(yuv422p10[i*2:], 400)
	}
	// Cb plane: 512 (neutral)
	cbOff := ySize * 2
	for i := 0; i < cbSize; i++ {
		binary.LittleEndian.PutUint16(yuv422p10[cbOff+i*2:], 512)
	}
	// Cr plane: 512 (neutral)
	crOff := cbOff + cbSize*2
	for i := 0; i < crSize; i++ {
		binary.LittleEndian.PutUint16(yuv422p10[crOff+i*2:], 512)
	}

	v210, err := YUV422P10ToV210(yuv422p10, width, height)
	if err != nil {
		panic(err)
	}
	return v210
}

// TestSource_Lossless10bit_ProducesLargerFrames verifies that when Lossless10bit
// is set, the MXL source produces YUV422P10LE frames (w*h*4 bytes) instead of
// lossy YUV420P 8-bit frames (w*h*3/2 bytes).
//
// This catches Fix 2: MXL source always used V210ToYUV420pInto (lossy 8-bit)
// even when the pipeline is 10-bit professional mode.
func TestSource_Lossless10bit_ProducesLargerFrames(t *testing.T) {
	const width = 12 // must be divisible by 6 for V210
	const height = 2

	var received struct {
		mu     sync.Mutex
		frames [][]byte
		widths []int
	}

	videoGrains := []mockGrain{
		{data: makeV210Frame10bit(width, height), info: GrainInfo{
			Index: 1, GrainSize: uint32(len(makeV210Frame10bit(width, height))),
			TotalSlices: 1, ValidSlices: 1,
		}},
	}
	flow := newMockDiscreteReader(videoGrains, FlowConfig{
		Format:    DataFormatVideo,
		GrainRate: Rational{30, 1},
	})

	src := NewSource(SourceConfig{
		FlowName:      "cam1",
		Width:         width,
		Height:        height,
		Lossless10bit: true,
		OnRawVideo: func(key string, yuv []byte, w, h int, pts int64) {
			received.mu.Lock()
			defer received.mu.Unlock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			received.frames = append(received.frames, cp)
			received.widths = append(received.widths, w)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	src.Start(ctx, flow, nil)

	deadline := time.After(2 * time.Second)
	for {
		received.mu.Lock()
		n := len(received.frames)
		received.mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: no frames received")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	src.Stop()

	received.mu.Lock()
	defer received.mu.Unlock()

	// YUV422P10LE frame size: (w*h + 2*(w/2)*h) * 2 = w*h*4
	expected10bitSize := width * height * 4
	// YUV420P 8-bit frame size: w*h*3/2
	expected8bitSize := width * height * 3 / 2

	require.NotEmpty(t, received.frames, "should have received at least one frame")

	frameSize := len(received.frames[0])
	assert.Equal(t, expected10bitSize, frameSize,
		"Lossless10bit should produce YUV422P10LE (%d bytes), not YUV420P 8-bit (%d bytes)",
		expected10bitSize, expected8bitSize)
}

// TestSource_Default8bit_ProducesSmallFrames verifies the default (non-10bit)
// path still produces 8-bit YUV420P frames.
func TestSource_Default8bit_ProducesSmallFrames(t *testing.T) {
	const width = 12
	const height = 2

	var received struct {
		mu     sync.Mutex
		frames [][]byte
	}

	videoGrains := []mockGrain{
		{data: makeV210Frame(width, height), info: GrainInfo{
			Index: 1, GrainSize: uint32(len(makeV210Frame(width, height))),
			TotalSlices: 1, ValidSlices: 1,
		}},
	}
	flow := newMockDiscreteReader(videoGrains, FlowConfig{
		Format:    DataFormatVideo,
		GrainRate: Rational{30, 1},
	})

	src := NewSource(SourceConfig{
		FlowName: "cam1",
		Width:    width,
		Height:   height,
		// Lossless10bit not set — default 8-bit path.
		OnRawVideo: func(key string, yuv []byte, w, h int, pts int64) {
			received.mu.Lock()
			defer received.mu.Unlock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			received.frames = append(received.frames, cp)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	src.Start(ctx, flow, nil)

	deadline := time.After(2 * time.Second)
	for {
		received.mu.Lock()
		n := len(received.frames)
		received.mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: no frames received")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	src.Stop()

	received.mu.Lock()
	defer received.mu.Unlock()

	expected8bitSize := width*height + 2*(width/2)*(height/2)
	require.NotEmpty(t, received.frames)
	assert.Equal(t, expected8bitSize, len(received.frames[0]),
		"default mode should produce YUV420P 8-bit frames")
}
