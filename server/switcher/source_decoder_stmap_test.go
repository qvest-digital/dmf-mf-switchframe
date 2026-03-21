package switcher

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/transition"
)

// patternMockDecoder returns a YUV frame where the Y plane is filled with
// sequential column values: column 0 = 10, column 1 = 20, column 2 = 30, etc.
// This makes it easy to verify spatial warps (e.g., horizontal flip).
type patternMockDecoder struct {
	width, height int
}

func (d *patternMockDecoder) Decode(data []byte) ([]byte, int, int, error) {
	size := d.width * d.height * 3 / 2
	yuv := make([]byte, size)
	// Fill Y plane with column-dependent values.
	for y := 0; y < d.height; y++ {
		for x := 0; x < d.width; x++ {
			yuv[y*d.width+x] = byte((x + 1) * 10)
		}
	}
	// Cb/Cr planes: fill with column-dependent values at half resolution.
	ySize := d.width * d.height
	cw := d.width / 2
	ch := d.height / 2
	for cy := 0; cy < ch; cy++ {
		for cx := 0; cx < cw; cx++ {
			yuv[ySize+cy*cw+cx] = byte((cx + 1) * 20)
			yuv[ySize+cw*ch+cy*cw+cx] = byte((cx + 1) * 30)
		}
	}
	return yuv, d.width, d.height, nil
}

func (d *patternMockDecoder) Close() {}

func TestSourceDecoder_AppliesSTMap(t *testing.T) {
	const w, h = 4, 2

	// Create a registry with a horizontal flip map assigned to "test-source".
	reg := stmap.NewRegistry()
	m, err := stmap.NewSTMap("hflip", w, h)
	require.NoError(t, err)

	// Build horizontal flip: reversed S, identity T.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			m.S[idx] = (float32(w-1-x) + 0.5) / float32(w)
			m.T[idx] = (float32(y) + 0.5) / float32(h)
		}
	}
	require.NoError(t, reg.Store(m))
	require.NoError(t, reg.AssignSource("test-source", "hflip"))

	factory := func() (transition.VideoDecoder, error) {
		return &patternMockDecoder{width: w, height: h}, nil
	}

	var mu sync.Mutex
	var received *ProcessingFrame
	callback := func(key string, pf *ProcessingFrame) {
		mu.Lock()
		received = pf
		mu.Unlock()
	}

	sd := newSourceDecoder("test-source", factory, callback, nil, nil, reg, nil)
	require.NotNil(t, sd)
	defer sd.Close()

	// Send a keyframe to trigger decode.
	frame := &media.VideoFrame{
		PTS:        3000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:        []byte{0x67, 0x42, 0x00, 0x0A},
		PPS:        []byte{0x68, 0xCE, 0x38, 0x80},
	}
	sd.Send(frame, time.Now().UnixNano())

	// Wait for the decode loop to process.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return received != nil
	}, time.Second, 5*time.Millisecond)

	mu.Lock()
	pf := received
	mu.Unlock()

	// The Y plane should be horizontally flipped.
	// Original column values: 10, 20, 30, 40
	// After hflip: 40, 30, 20, 10
	require.Equal(t, w, pf.Width)
	require.Equal(t, h, pf.Height)

	// Check first row of Y plane.
	row0 := pf.YUV[0:w]
	require.Equal(t, byte(40), row0[0], "Y[0,0] should be 40 after hflip")
	require.Equal(t, byte(30), row0[1], "Y[0,1] should be 30 after hflip")
	require.Equal(t, byte(20), row0[2], "Y[0,2] should be 20 after hflip")
	require.Equal(t, byte(10), row0[3], "Y[0,3] should be 10 after hflip")
}

func TestSourceDecoder_NoSTMapRegistry(t *testing.T) {
	const w, h = 4, 2

	factory := func() (transition.VideoDecoder, error) {
		return &patternMockDecoder{width: w, height: h}, nil
	}

	var mu sync.Mutex
	var received *ProcessingFrame
	callback := func(key string, pf *ProcessingFrame) {
		mu.Lock()
		received = pf
		mu.Unlock()
	}

	// nil registry — no stmap applied.
	sd := newSourceDecoder("test-source", factory, callback, nil, nil, nil, nil)
	require.NotNil(t, sd)
	defer sd.Close()

	frame := &media.VideoFrame{
		PTS:        3000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:        []byte{0x67, 0x42, 0x00, 0x0A},
		PPS:        []byte{0x68, 0xCE, 0x38, 0x80},
	}
	sd.Send(frame, time.Now().UnixNano())

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return received != nil
	}, time.Second, 5*time.Millisecond)

	mu.Lock()
	pf := received
	mu.Unlock()

	// Original column values should be preserved: 10, 20, 30, 40.
	row0 := pf.YUV[0:w]
	require.Equal(t, byte(10), row0[0], "Y[0,0] should be 10 (no stmap)")
	require.Equal(t, byte(20), row0[1], "Y[0,1] should be 20 (no stmap)")
	require.Equal(t, byte(30), row0[2], "Y[0,2] should be 30 (no stmap)")
	require.Equal(t, byte(40), row0[3], "Y[0,3] should be 40 (no stmap)")
}

func TestSourceDecoder_STMapNoAssignment(t *testing.T) {
	const w, h = 4, 2

	// Registry exists but no map assigned to this source.
	reg := stmap.NewRegistry()
	m, err := stmap.NewSTMap("hflip", w, h)
	require.NoError(t, err)
	require.NoError(t, reg.Store(m))
	// Note: NOT calling reg.AssignSource — source has no assignment.

	factory := func() (transition.VideoDecoder, error) {
		return &patternMockDecoder{width: w, height: h}, nil
	}

	var mu sync.Mutex
	var received *ProcessingFrame
	callback := func(key string, pf *ProcessingFrame) {
		mu.Lock()
		received = pf
		mu.Unlock()
	}

	sd := newSourceDecoder("test-source", factory, callback, nil, nil, reg, nil)
	require.NotNil(t, sd)
	defer sd.Close()

	frame := &media.VideoFrame{
		PTS:        3000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:        []byte{0x67, 0x42, 0x00, 0x0A},
		PPS:        []byte{0x68, 0xCE, 0x38, 0x80},
	}
	sd.Send(frame, time.Now().UnixNano())

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return received != nil
	}, time.Second, 5*time.Millisecond)

	mu.Lock()
	pf := received
	mu.Unlock()

	// Original column values should be preserved (no assignment = no warp).
	row0 := pf.YUV[0:w]
	require.Equal(t, byte(10), row0[0], "Y[0,0] should be 10 (no assignment)")
	require.Equal(t, byte(20), row0[1], "Y[0,1] should be 20 (no assignment)")
	require.Equal(t, byte(30), row0[2], "Y[0,2] should be 30 (no assignment)")
	require.Equal(t, byte(40), row0[3], "Y[0,3] should be 40 (no assignment)")
}

func TestSwitcher_SetSTMapRegistry(t *testing.T) {
	s := &Switcher{}
	require.Nil(t, s.stmapRegistry)

	reg := stmap.NewRegistry()
	s.SetSTMapRegistry(reg)
	require.Equal(t, reg, s.stmapRegistry)
}
