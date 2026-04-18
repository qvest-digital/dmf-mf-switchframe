//go:build cgo && darwin

package gpu

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

// p210FillKeyBridge models the production case where the GPU source
// manager has a P210-formatted fill in its cache (because the program is
// running 10-bit 4:2:2 mode), and the keyer asks for it via GPUFill.
type p210FillKeyBridge struct {
	hasKeys bool
	keys    []EnabledKeySnapshot
	fill    *GPUFrame
}

func (m *p210FillKeyBridge) HasEnabledKeysWithFills() bool             { return m.hasKeys }
func (m *p210FillKeyBridge) HasEnabledKeys() bool                      { return m.hasKeys }
func (m *p210FillKeyBridge) SnapshotEnabledKeys() []EnabledKeySnapshot { return m.keys }
func (m *p210FillKeyBridge) GPUFill(string) *GPUFrame                  { return m.fill }

// TestGPUKeyNode_P210_ChromaKey validates that the GPU upstream key node
// correctly applies a chroma key on a P210 (10-bit 4:2:2) program frame.
//
// Setup mirrors a real green-screen workflow:
//   - Program: a "background" (Y=600 mid-bright, neutral chroma)
//   - Fill: the foreground source where most of the frame is "green"
//     (matches the key chroma → mask=0 → program shows through), with a
//     centre rectangle at neutral chroma representing the on-screen
//     subject (mask=255 → fill replaces the program).
//
// Without the P210 chroma_key kernel + dispatch, this hits the NV12
// kernel against a P210 buffer: the alpha mask comes back as garbage
// and Y values across the whole frame are mangled, which is what the
// user reported as "crashes / green artifacts" in 10-bit mode. With the
// P210 path wired, every output Y sample takes one of two known values.
func TestGPUKeyNode_P210_ChromaKey(t *testing.T) {
	const w, h = 32, 32
	ctx := testContext(t)

	pool, err := NewP210FramePool(ctx, w, h, 6)
	require.NoError(t, err)
	defer pool.Close()

	const keyCb8, keyCr8 uint8 = 44, 34 // BT.709 green-ish

	// Build the fill frame: most pixels = "green" (key chroma) so they
	// get mask=0 (program shows through). A central rectangle has neutral
	// chroma so mask=255 (fill replaces program there).
	fill, err := pool.Acquire()
	require.NoError(t, err)
	fillYUV := make([]byte, video.YUV422_10bitFrameSize(w, h))
	const fillY uint16 = 200
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(fillYUV[i*2:], fillY)
	}
	cps := (w / 2) * h * 2
	cbStart := w * h * 2
	crStart := cbStart + cps
	subjectX0, subjectX1 := w/4, 3*w/4
	subjectY0, subjectY1 := h/4, 3*h/4
	for cy := 0; cy < h; cy++ {
		for cx := 0; cx < w/2; cx++ {
			lx := cx * 2
			cb := uint16(keyCb8) << 2
			cr := uint16(keyCr8) << 2
			if cy >= subjectY0 && cy < subjectY1 && lx >= subjectX0 && lx < subjectX1 {
				cb, cr = 512, 512 // neutral in subject region
			}
			off := (cy*(w/2) + cx) * 2
			binary.LittleEndian.PutUint16(fillYUV[cbStart+off:], cb)
			binary.LittleEndian.PutUint16(fillYUV[crStart+off:], cr)
		}
	}
	require.NoError(t, Upload10(ctx, fill, fillYUV, w, h))

	bridge := &p210FillKeyBridge{
		hasKeys: true,
		fill:    fill,
		keys: []EnabledKeySnapshot{
			{
				SourceKey:  "cam1",
				Type:       "chroma",
				KeyCb:      keyCb8,
				KeyCr:      keyCr8,
				Similarity: 0.15,
				Smoothness: 0.05,
				FillW:      w,
				FillH:      h,
			},
		},
	}

	node := NewGPUKeyNode(ctx, pool, bridge).(*gpuKeyNode)
	defer func() { _ = node.Close() }()
	require.NoError(t, node.Configure(w, h, 0))

	// Program frame: uniform Y=600, neutral chroma.
	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()
	require.Equal(t, GPUFormatP210, frame.Format)

	const programY uint16 = 600
	yuv10 := make([]byte, video.YUV422_10bitFrameSize(w, h))
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv10[i*2:], programY)
	}
	for i := 0; i < cps; i += 2 {
		binary.LittleEndian.PutUint16(yuv10[cbStart+i:], 512)
		binary.LittleEndian.PutUint16(yuv10[crStart+i:], 512)
	}
	require.NoError(t, Upload10(ctx, frame, yuv10, w, h))

	require.NoError(t, node.ProcessGPU(frame))

	out := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, out, w, h))

	// Walk pixels: outside the subject rectangle the green key should
	// have produced mask=0 → program kept (Y=600). Inside, mask=255 →
	// fill substituted (Y=200). Anything else means the keyer corrupted
	// the frame.
	greenKept, subjectReplaced, anomalies := 0, 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			yv := binary.LittleEndian.Uint16(out[(y*w+x)*2:])
			inSubject := y >= subjectY0 && y < subjectY1 && x >= subjectX0 && x < subjectX1
			switch {
			case !inSubject && yv == programY:
				greenKept++
			case inSubject && yv == fillY:
				subjectReplaced++
			default:
				anomalies++
			}
		}
	}

	assert.Zero(t, anomalies,
		"%d of %d Y samples have unexpected values; "+
			"green kept=%d subject replaced=%d (P210 chroma key path is broken)",
		anomalies, w*h, greenKept, subjectReplaced)
	t.Logf("P210 chroma key: green region kept program Y on %d pixels, "+
		"subject region took fill Y on %d pixels", greenKept, subjectReplaced)
}

// TestGPUKeyNode_P210_LumaKey validates the P210 luma keyer path. The
// fill has a horizontal split: top half is dark (Y=80, masked out by the
// luma key range) and bottom half is bright (Y=880, kept). Bottom half
// pixels should be replaced by fill; top half should keep the program.
func TestGPUKeyNode_P210_LumaKey(t *testing.T) {
	const w, h = 32, 32
	ctx := testContext(t)

	pool, err := NewP210FramePool(ctx, w, h, 6)
	require.NoError(t, err)
	defer pool.Close()

	fill, err := pool.Acquire()
	require.NoError(t, err)
	fillYUV := make([]byte, video.YUV422_10bitFrameSize(w, h))
	const darkY uint16 = 80
	const brightY uint16 = 880
	for y := 0; y < h; y++ {
		yv := darkY
		if y >= h/2 {
			yv = brightY
		}
		for x := 0; x < w; x++ {
			binary.LittleEndian.PutUint16(fillYUV[(y*w+x)*2:], yv)
		}
	}
	cps := (w / 2) * h * 2
	for i := 0; i < cps; i += 2 {
		binary.LittleEndian.PutUint16(fillYUV[w*h*2+i:], 512)
		binary.LittleEndian.PutUint16(fillYUV[w*h*2+cps+i:], 512)
	}
	require.NoError(t, Upload10(ctx, fill, fillYUV, w, h))

	// LumaKey config: keep only Y values in [120, 240] (8-bit). Dark Y=20
	// (= 80>>2) → out-of-range → mask=0 → program kept. Bright Y=220
	// (= 880>>2) → in-range → mask=255 → fill kept.
	bridge := &p210FillKeyBridge{
		hasKeys: true,
		fill:    fill,
		keys: []EnabledKeySnapshot{
			{
				SourceKey: "cam1",
				Type:      "luma",
				LowClip:   120,
				HighClip:  240,
				Softness:  2,
				FillW:     w,
				FillH:     h,
			},
		},
	}

	node := NewGPUKeyNode(ctx, pool, bridge).(*gpuKeyNode)
	defer func() { _ = node.Close() }()
	require.NoError(t, node.Configure(w, h, 0))

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	const programY uint16 = 600
	yuv10 := make([]byte, video.YUV422_10bitFrameSize(w, h))
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv10[i*2:], programY)
	}
	for i := 0; i < cps; i += 2 {
		binary.LittleEndian.PutUint16(yuv10[w*h*2+i:], 512)
		binary.LittleEndian.PutUint16(yuv10[w*h*2+cps+i:], 512)
	}
	require.NoError(t, Upload10(ctx, frame, yuv10, w, h))

	require.NoError(t, node.ProcessGPU(frame))

	out := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, out, w, h))

	topKeptProgram, bottomTookFill, anomalies := 0, 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			yv := binary.LittleEndian.Uint16(out[(y*w+x)*2:])
			switch {
			case y < h/2 && yv == programY:
				topKeptProgram++
			case y >= h/2 && yv == brightY:
				bottomTookFill++
			default:
				anomalies++
			}
		}
	}

	assert.Zero(t, anomalies,
		"%d of %d Y samples have unexpected values; "+
			"top kept=%d bottom replaced=%d (P210 luma key path is broken)",
		anomalies, w*h, topKeptProgram, bottomTookFill)
}
