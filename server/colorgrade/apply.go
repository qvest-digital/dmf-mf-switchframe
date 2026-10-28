package colorgrade

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
)

// BT.709 luma weights, matching BT709_Y_* in the GPU kernels.
const (
	lumaR = 0.2126
	lumaG = 0.7152
	lumaB = 0.0722
)

// Grader applies a 3D LUT to planar YUV frames on the CPU. The arithmetic
// is that of the GPU color grade kernels: limited-range BT.709 YCbCr to RGB,
// trilinear LUT lookup, a blend with the ungraded RGB by intensity, and back
// to YCbCr. Chroma is graded once per chroma sample, from the luma sample at
// its top-left position, as the GPU kernels do.
//
// A Grader is immutable once built and safe for concurrent use.
type Grader struct {
	size int
	data []float32 // R,G,B triplets, red-major, size^3*3
	// luma holds the BT.709 luma of each LUT node. Luma is linear in R, G
	// and B, so interpolating it equals the luma of the interpolated node
	// colours, and a luma-only sample costs one channel instead of three.
	luma []float32

	memo atomic.Pointer[memo8] // 8-bit results, see ApplyYUV420
}

// NewGrader validates a LUT and returns a Grader for it.
func NewGrader(data []float32, size int) (*Grader, error) {
	if size < 2 {
		return nil, fmt.Errorf("colorgrade: LUT size %d < 2", size)
	}
	if len(data) != size*size*size*3 {
		return nil, fmt.Errorf("colorgrade: LUT size %d needs %d floats, got %d",
			size, size*size*size*3, len(data))
	}
	luma := make([]float32, size*size*size)
	for i := range luma {
		luma[i] = lumaOf(data[i*3], data[i*3+1], data[i*3+2])
	}
	return &Grader{size: size, data: data, luma: luma}, nil
}

// cell returns the trilinear cell and weights of an RGB sample.
func (g *Grader) cell(r, gr, b float32) (i000 int, dr, dg, db float32) {
	s := g.size
	fr := r * float32(s-1)
	fg := gr * float32(s-1)
	fb := b * float32(s-1)
	r0 := clampIdx(int(fr), s-2)
	g0 := clampIdx(int(fg), s-2)
	b0 := clampIdx(int(fb), s-2)
	return b0*s*s + g0*s + r0, fr - float32(r0), fg - float32(g0), fb - float32(b0)
}

// lookupLuma interpolates the luma table, the Y of lookup's result.
func (g *Grader) lookupLuma(r, gr, b float32) float32 {
	s := g.size
	i000, dr, dg, db := g.cell(r, gr, b)
	i010 := i000 + s
	i001 := i000 + s*s
	i011 := i001 + s
	d := g.luma
	_ = d[i011+1]
	c00 := d[i000] + (d[i000+1]-d[i000])*dr
	c10 := d[i010] + (d[i010+1]-d[i010])*dr
	c01 := d[i001] + (d[i001+1]-d[i001])*dr
	c11 := d[i011] + (d[i011+1]-d[i011])*dr
	c0 := c00 + (c10-c00)*dg
	c1 := c01 + (c11-c01)*dg
	return c0 + (c1-c0)*db
}

// gradeLuma returns the graded Y of one normalized YCbCr sample. It equals
// lumaOf(gradePixel(...)) up to float rounding.
func (g *Grader) gradeLuma(yn, cbn, crn, intensity float32) float32 {
	r := clamp01f(yn + 1.5748*crn)
	gr := clamp01f(yn - 0.1873*cbn - 0.4681*crn)
	b := clamp01f(yn + 1.8556*cbn)
	y := lumaOf(r, gr, b)
	return y + (g.lookupLuma(r, gr, b)-y)*intensity
}

// lookup performs the trilinear lookup of the GPU lut3d_lookup function.
func (g *Grader) lookup(r, gr, b float32) (float32, float32, float32) {
	s := g.size
	c, dr, dg, db := g.cell(r, gr, b)
	s2 := s * s
	i000 := c * 3
	i100 := i000 + 3
	i010 := i000 + s*3
	i110 := i010 + 3
	i001 := i000 + s2*3
	i101 := i001 + 3
	i011 := i001 + s*3
	i111 := i011 + 3
	d := g.data[:len(g.data):len(g.data)]
	_ = d[i111+2]

	var out [3]float32
	for ch := 0; ch < 3; ch++ {
		c000 := d[i000+ch]
		c100 := d[i100+ch]
		c010 := d[i010+ch]
		c110 := d[i110+ch]
		c001 := d[i001+ch]
		c101 := d[i101+ch]
		c011 := d[i011+ch]
		c111 := d[i111+ch]
		c00 := c000 + (c100-c000)*dr
		c10 := c010 + (c110-c010)*dr
		c01 := c001 + (c101-c001)*dr
		c11 := c011 + (c111-c011)*dr
		c0 := c00 + (c10-c00)*dg
		c1 := c01 + (c11-c01)*dg
		out[ch] = c0 + (c1-c0)*db
	}
	return out[0], out[1], out[2]
}

func clampIdx(v, hi int) int {
	if v < 0 {
		return 0
	}
	if v > hi {
		return hi
	}
	return v
}

func clamp01f(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// gradePixel grades one normalized YCbCr sample and returns the blended RGB.
func (g *Grader) gradePixel(yn, cbn, crn, intensity float32) (float32, float32, float32) {
	r := clamp01f(yn + 1.5748*crn)
	gr := clamp01f(yn - 0.1873*cbn - 0.4681*crn)
	b := clamp01f(yn + 1.8556*cbn)
	lr, lg, lb := g.lookup(r, gr, b)
	return r + (lr-r)*intensity, gr + (lg-gr)*intensity, b + (lb-b)*intensity
}

func lumaOf(r, g, b float32) float32 { return lumaR*r + lumaG*g + lumaB*b }
func cbOf(r, g, b float32) float32   { return -0.1146*r - 0.3854*g + 0.5*b }
func crOf(r, g, b float32) float32   { return 0.5*r - 0.4542*g - 0.0458*b }

func quantize(v, lo, hi float32) int {
	v += 0.5
	if v < lo {
		v = lo
	} else if v > hi {
		v = hi
	}
	return int(v)
}

// ApplyYUV420 grades an 8-bit planar YUV 4:2:0 frame (Y, then Cb, then Cr)
// in place.
//
// With 8-bit input the graded result of a sample depends on its Y, Cb and Cr
// codes alone, so each code triple is graded once and the result kept until
// the LUT or the intensity changes. Camera content repeats code triples from
// frame to frame, so after the first frames most samples cost a table read
// instead of a trilinear lookup. The cache takes 1 KiB per (Cb, Cr) pair
// that occurs, 64 MiB at most.
func (g *Grader) ApplyYUV420(frame []byte, w, h int, intensity float32) error {
	if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 {
		return fmt.Errorf("colorgrade: invalid 4:2:0 dimensions %dx%d", w, h)
	}
	ySize := w * h
	cSize := ySize / 4
	if len(frame) < ySize+2*cSize {
		return fmt.Errorf("colorgrade: 4:2:0 frame %d bytes, need %d", len(frame), ySize+2*cSize)
	}
	m := g.memo.Load()
	if m == nil || m.intensity != intensity {
		m = &memo8{g: g, intensity: intensity}
		g.memo.Store(m)
	}
	yp := frame[:ySize]
	cbp := frame[ySize : ySize+cSize]
	crp := frame[ySize+cSize : ySize+2*cSize]
	cw := w / 2
	parallelRows(h/2, func(c0, c1 int) {
		for cy := c0; cy < c1; cy++ {
			row0 := yp[cy*2*w : cy*2*w+w]
			row1 := yp[(cy*2+1)*w : (cy*2+1)*w+w]
			cbr := cbp[cy*cw : cy*cw+cw]
			crr := crp[cy*cw : cy*cw+cw]
			for cx := range cbr {
				cb, cr := cbr[cx], crr[cx]
				sl := m.slabs[int(cb)<<8|int(cr)].Load()
				if sl == nil {
					sl = m.newSlab(cb, cr)
				}
				x := cx * 2
				var v [4]uint32
				for i, y := range [4]byte{row0[x], row0[x+1], row1[x], row1[x+1]} {
					if v[i] = sl[y].Load(); v[i] == 0 {
						v[i] = m.compute(&sl[y], y, cb, cr)
					}
				}
				// Chroma comes from the top-left sample, as on the GPU.
				row0[x], cbr[cx], crr[cx] = byte(v[0]), byte(v[0]>>8), byte(v[0]>>16)
				row0[x+1], row1[x], row1[x+1] = byte(v[1]), byte(v[2]), byte(v[3])
			}
		}
	})
	return nil
}

// memo8 caches graded 8-bit samples for one Grader and intensity. A slab
// covers one (Cb, Cr) pair; an entry packs Y', Cb' and Cr' in its low 24
// bits and sets bit 24 once computed, so zero means not yet computed.
type memo8 struct {
	g         *Grader
	intensity float32
	slabs     [65536]atomic.Pointer[[256]atomic.Uint32]
}

const memoValid = 1 << 24

func (m *memo8) newSlab(cb, cr byte) *[256]atomic.Uint32 {
	p := &m.slabs[int(cb)<<8|int(cr)]
	p.CompareAndSwap(nil, new([256]atomic.Uint32))
	return p.Load()
}

// compute grades (y, cb, cr) and stores the packed result in e. Workers
// racing on one entry compute and store the same value.
func (m *memo8) compute(e *atomic.Uint32, y, cb, cr byte) uint32 {
	fr, fg, fb := m.g.gradePixel((float32(y)-16)/219, (float32(cb)-128)/224, (float32(cr)-128)/224, m.intensity)
	v := uint32(quantize(16+219*lumaOf(fr, fg, fb), 16, 235)) |
		uint32(quantize(128+224*cbOf(fr, fg, fb), 16, 240))<<8 |
		uint32(quantize(128+224*crOf(fr, fg, fb), 16, 240))<<16 | memoValid
	e.Store(v)
	return v
}

// ApplyYUV422P10 grades a 10-bit planar YUV 4:2:2 frame (little-endian
// uint16 Y, then Cb, then Cr, chroma at half width and full height) in
// place.
func (g *Grader) ApplyYUV422P10(frame []byte, w, h int, intensity float32) error {
	if w <= 0 || h <= 0 || w%2 != 0 {
		return fmt.Errorf("colorgrade: invalid 4:2:2 dimensions %dx%d", w, h)
	}
	ySize := w * h * 2
	cSize := ySize / 2
	if len(frame) < ySize+2*cSize {
		return fmt.Errorf("colorgrade: 4:2:2 10-bit frame %d bytes, need %d", len(frame), ySize+2*cSize)
	}
	yp := frame[:ySize]
	cbp := frame[ySize : ySize+cSize]
	crp := frame[ySize+cSize : ySize+2*cSize]
	cw := w / 2
	le := binary.LittleEndian
	parallelRows(h, func(r0, r1 int) {
		for y := r0; y < r1; y++ {
			for cx := 0; cx < cw; cx++ {
				ci := (y*cw + cx) * 2
				cbn := (float32(le.Uint16(cbp[ci:])) - 512) * (1.0 / 896)
				crn := (float32(le.Uint16(crp[ci:])) - 512) * (1.0 / 896)
				yi := (y*w + cx*2) * 2
				fr, fg, fb := g.gradePixel((float32(le.Uint16(yp[yi:]))-64)*(1.0/876), cbn, crn, intensity)
				le.PutUint16(yp[yi:], uint16(quantize(64+876*lumaOf(fr, fg, fb), 64, 940)))
				le.PutUint16(cbp[ci:], uint16(quantize(512+896*cbOf(fr, fg, fb), 64, 960)))
				le.PutUint16(crp[ci:], uint16(quantize(512+896*crOf(fr, fg, fb), 64, 960)))
				yi += 2
				yn := (float32(le.Uint16(yp[yi:])) - 64) * (1.0 / 876)
				le.PutUint16(yp[yi:], uint16(quantize(64+876*g.gradeLuma(yn, cbn, crn, intensity), 64, 940)))
			}
		}
	})
	return nil
}

// parallelRows splits [0, rows) into contiguous bands, one per worker, and
// waits for all of them. A 1080p grade takes tens of milliseconds on one
// core, more than a 50 fps frame period.
func parallelRows(rows int, fn func(r0, r1 int)) {
	workers := gradeWorkers
	if workers > rows {
		workers = rows
	}
	if workers <= 1 {
		fn(0, rows)
		return
	}
	var wg sync.WaitGroup
	band := (rows + workers - 1) / workers
	for r0 := 0; r0 < rows; r0 += band {
		r1 := min(r0+band, rows)
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(r0, r1)
		}()
	}
	wg.Wait()
}

// gradeWorkers bounds the goroutines one frame is graded with.
var gradeWorkers = min(runtime.GOMAXPROCS(0), 4)
