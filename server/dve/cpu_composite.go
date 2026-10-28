package dve

import (
	"image"
	"math"
	"runtime"
	"sync"
	"unsafe"

	"github.com/zsiec/switchframe/server/internal/video"
)

// CompositeCPU composites src onto dst through a 2D DVE transform on the
// CPU. It is the counterpart of the GPU DVE composite kernels in
// gpu/cuda/dve.cu and follows them step for step: an inverse projective
// map from output to source pixels, mirror and mosaic in source space,
// bilinear sampling, brightness and contrast on luma and saturation on
// chroma, a rounded-rect SDF for crop, border and drop shadow, and an alpha
// blend in 8.8 fixed point.
//
// Frames are planar: 8-bit 4:2:0 (video.YUV420_8bit) or little-endian
// 10-bit 4:2:2 (video.YUV422_10bit), both in format f. inv is
// ComputeInverseTransform(t, srcW, srcH) and aabb is ComputeAABB(t, srcW,
// srcH, dstW, dstH).
func CompositeCPU(dst []byte, dstW, dstH int, src []byte, srcW, srcH int,
	f video.Format, t Transform, inv Matrix3x3, aabb image.Rectangle) {
	if f == video.YUV422_10bit {
		if len(dst) < f.FrameSize(dstW, dstH) || len(src) < f.FrameSize(srcW, srcH) {
			return
		}
		d := unsafe.Slice((*uint16)(unsafe.Pointer(&dst[0])), len(dst)/2)
		s := unsafe.Slice((*uint16)(unsafe.Pointer(&src[0])), len(src)/2)
		composite(d, dstW, dstH, s, srcW, srcH, &depth10, t, inv, aabb)
		return
	}
	if len(dst) < f.FrameSize(dstW, dstH) || len(src) < f.FrameSize(srcW, srcH) {
		return
	}
	composite(dst, dstW, dstH, src, srcW, srcH, &depth8, t, inv, aabb)
}

type sample interface{ ~uint8 | ~uint16 }

// sampleDepth holds what the 8-bit and 10-bit kernels differ in.
type sampleDepth struct {
	max     float32 // largest code
	neutral float32 // neutral chroma
	shift   uint    // left shift from an 8-bit colour to this depth
	vsub    int     // chroma rows per luma row: 2 for 4:2:0, 1 for 4:2:2
}

var (
	depth8  = sampleDepth{max: 255, neutral: 128, shift: 0, vsub: 2}
	depth10 = sampleDepth{max: 1023, neutral: 512, shift: 2, vsub: 1}
)

// pixelParams are the per-plane inputs of computeDVEPixel.
type pixelParams struct {
	srcW, srcH                                   float32
	cropTop, cropBottom, cropLeft, cropRight     float32
	cropSoftness                                 float32
	borderWidth, borderColor, borderCornerRadius float32
	borderSoftness                               float32
	shadowEnabled                                bool
	shadowOffsetX, shadowOffsetY, shadowBlur     float32
	shadowAlpha, shadowColor                     float32
	slotAlpha                                    float32
}

func saturate(x float32) float32 { return min(max(x, 0), 1) }

func roundedRectSDF(px, py, halfW, halfH, cr float32) float32 {
	dx := absf(px) - (halfW - cr)
	dy := absf(py) - (halfH - cr)
	dxc := max(dx, 0)
	dyc := max(dy, 0)
	return float32(math.Sqrt(float64(dxc*dxc+dyc*dyc))) + min(max(dx, dy), 0) - cr
}

func absf(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func gaussianFalloff(dist, sigma float32) float32 {
	if sigma <= 0 {
		return 0
	}
	t := dist / sigma
	t2 := t * t
	return 1 / (1 + 0.5*t2 + 0.125*t2*t2)
}

// computeDVEPixel returns the value and alpha one output sample takes from
// a slot whose source coordinate is (srcX, srcY), as the GPU kernels'
// computeDVEPixel does. When sampled is true the value is the video sample
// at that coordinate, which the caller fetches only then: shadow, border and
// the outside of the slot need none.
func (p *pixelParams) computeDVEPixel(srcX, srcY float32) (value, alpha float32, sampled bool) {
	visLeft := p.cropLeft
	visRight := p.srcW - p.cropRight
	visTop := p.cropTop
	visBottom := p.srcH - p.cropBottom
	halfW := (visRight - visLeft) * 0.5
	halfH := (visBottom - visTop) * 0.5
	if halfW <= 0 || halfH <= 0 {
		return 0, 0, false
	}
	centerX := visLeft + halfW
	centerY := visTop + halfH
	cr := min(p.borderCornerRadius, min(halfW, halfH))
	dist := roundedRectSDF(srcX-centerX, srcY-centerY, halfW, halfH, cr)

	if p.shadowEnabled && dist > 0 {
		sd := roundedRectSDF(srcX-p.shadowOffsetX-centerX, srcY-p.shadowOffsetY-centerY, halfW, halfH, cr)
		if sd > 0 {
			sigma := max(p.shadowBlur*0.5, 0.5)
			return p.shadowColor, p.shadowAlpha * gaussianFalloff(sd, sigma) * p.slotAlpha, false
		}
		return p.shadowColor, p.shadowAlpha * p.slotAlpha, false
	}

	if p.borderWidth > 0 && dist > -p.borderWidth && dist <= 0 {
		a := float32(1)
		if p.borderSoftness > 0 {
			a = saturate(-dist / max(p.borderSoftness, 0.001))
			a *= saturate((dist + p.borderWidth) / max(p.borderSoftness, 0.001))
		}
		return p.borderColor, a * p.slotAlpha, false
	}

	// Outside the slot with no shadow. The GPU kernel's soft-edge branch
	// here computes saturate(-dist/softness) with dist > 0, which is 0.
	if dist > 0 {
		return 0, 0, false
	}

	videoAlpha := float32(1)
	if p.cropSoftness > 0 {
		fl := saturate((srcX - visLeft) / p.cropSoftness)
		fr := saturate((visRight - srcX) / p.cropSoftness)
		ft := saturate((srcY - visTop) / p.cropSoftness)
		fb := saturate((visBottom - srcY) / p.cropSoftness)
		videoAlpha = min(min(fl, fr), min(ft, fb))
	}
	if p.borderWidth > 0 && p.borderSoftness > 0 && dist > -(p.borderWidth+p.borderSoftness) {
		videoAlpha *= saturate((-dist - p.borderWidth) / max(p.borderSoftness, 0.001))
	}
	return 0, videoAlpha * p.slotAlpha, true
}

func bilinear[T sample](plane []T, x, y float32, w, h, stride int) float32 {
	if x < 0 {
		x = 0
	} else if fw := float32(w - 1); x > fw {
		x = fw
	}
	if y < 0 {
		y = 0
	} else if fh := float32(h - 1); y > fh {
		y = fh
	}
	sx, sy := int(x), int(y)
	fx, fy := x-float32(sx), y-float32(sy)
	sx1 := min(sx+1, w-1)
	sy1 := min(sy+1, h-1)
	r0 := plane[sy*stride : sy*stride+w]
	r1 := plane[sy1*stride : sy1*stride+w]
	v00, v10 := float32(r0[sx]), float32(r0[sx1])
	v01, v11 := float32(r1[sx]), float32(r1[sx1])
	top := v00 + (v10-v00)*fx
	bot := v01 + (v11-v01)*fx
	return top + (bot-top)*fy
}

// rowSampler is bilinear with the source row fixed: an axis transform maps
// a whole output row to one source y.
type rowSampler[T sample] struct {
	r0, r1 []T
	fy     float32
	w      int
	fw     float32
}

func newRowSampler[T sample](plane []T, y float32, w, h, stride int) rowSampler[T] {
	y = max(0, min(y, float32(h-1)))
	sy := int(y)
	sy1 := min(sy+1, h-1)
	return rowSampler[T]{
		r0: plane[sy*stride : sy*stride+w],
		r1: plane[sy1*stride : sy1*stride+w],
		fy: y - float32(sy),
		w:  w,
		fw: float32(w - 1),
	}
}

func (r *rowSampler[T]) at(x float32) float32 {
	if x < 0 {
		x = 0
	} else if x > r.fw {
		x = r.fw
	}
	sx := int(x)
	fx := x - float32(sx)
	sx1 := min(sx+1, r.w-1)
	v00, v10 := float32(r.r0[sx]), float32(r.r0[sx1])
	v01, v11 := float32(r.r1[sx]), float32(r.r1[sx1])
	top := v00 + (v10-v00)*fx
	bot := v01 + (v11-v01)*fx
	return top + (bot-top)*r.fy
}

// alpha256 is the kernels' conversion of an alpha to 8.8 fixed point.
func alpha256(a float32) int { return min(int(a*256+0.5), 256) }

// project maps output (x, y) through the inverse matrix.
func project(m *Matrix3x3, x, y float32) (float32, float32) {
	w := m[6]*x + m[7]*y + m[8]
	var invW float32
	if absf(w) > 1e-6 {
		invW = 1 / w
	}
	return (m[0]*x + m[1]*y + m[2]) * invW, (m[3]*x + m[4]*y + m[5]) * invW
}

func composite[T sample](dst []T, dstW, dstH int, src []T, srcW, srcH int,
	d *sampleDepth, t Transform, inv Matrix3x3, aabb image.Rectangle) {
	slotA256 := min(max(int(t.EffectiveOpacity()*256), 0), 256)
	shadowAlpha256 := 0
	if t.Shadow.Enabled {
		shadowAlpha256 = min(max(int(t.Shadow.Opacity*256), 0), 256)
	}
	contrast := float32(t.Contrast)
	if contrast == 0 {
		contrast = 1
	}
	saturation := float32(t.Saturation)
	if saturation == 0 {
		saturation = 1
	}
	brightness := float32(t.Brightness)

	base := pixelParams{
		cropTop: float32(t.CropTop), cropBottom: float32(t.CropBottom),
		cropLeft: float32(t.CropLeft), cropRight: float32(t.CropRight),
		cropSoftness:       float32(t.CropSoftness),
		borderWidth:        float32(t.Border.Width),
		borderCornerRadius: float32(t.Border.CornerRadius),
		borderSoftness:     float32(t.Border.Softness),
		shadowEnabled:      t.Shadow.Enabled,
		shadowOffsetX:      float32(t.Shadow.OffsetX),
		shadowOffsetY:      float32(t.Shadow.OffsetY),
		shadowBlur:         float32(t.Shadow.Blur),
		shadowAlpha:        float32(shadowAlpha256) / 256,
		slotAlpha:          float32(slotA256) / 256,
	}
	expand := base.borderWidth + base.borderSoftness
	if base.shadowEnabled {
		expand = max(expand, absf(base.shadowOffsetX)+base.shadowBlur*2)
		expand = max(expand, absf(base.shadowOffsetY)+base.shadowBlur*2)
	}
	k := kernel{
		inv:        inv,
		affine:     inv[6] == 0 && inv[7] == 0,
		mirrorH:    t.MirrorH,
		mirrorV:    t.MirrorV,
		mosaic:     float32(t.MosaicSize),
		srcW:       float32(srcW),
		contrast:   contrast,
		brightness: brightness,
		saturation: saturation,
		maxV:       d.max,
		neutral:    d.neutral,
		slotA256:   slotA256,
	}
	if k.affine && absf(inv[8]) > 1e-6 {
		k.invW = 1 / inv[8]
	}
	k.axis = k.affine && inv[1] == 0 && inv[3] == 0

	// --- Y plane ---
	yp := base
	yp.srcW, yp.srcH = float32(srcW), float32(srcH)
	yp.borderColor = float32(int(t.Border.ColorY) << d.shift)
	yp.shadowColor = float32(int(t.Shadow.ColorY) << d.shift)
	ky := k
	ky.px = &yp
	ky.inner = innerRect(&yp)
	ky.reach = reachRect(&yp)
	ky.srcH = float32(srcH)
	e := int(expand + 1)
	dY := dst[:dstW*dstH]
	sY := src[:srcW*srcH]
	y0, y1 := max(aabb.Min.Y-e, 0), min(aabb.Max.Y+e, dstH-1)
	x0, x1 := max(aabb.Min.X-e, 0), min(aabb.Max.X+e, dstW-1)
	rows(y0, y1+1, func(r0, r1 int) {
		for oy := r0; oy < r1; oy++ {
			lumaRow(&ky, dY[oy*dstW:oy*dstW+dstW], sY, srcW, srcH, oy, x0, x1)
		}
	})

	// --- Chroma planes ---
	dcw, dch := dstW/2, dstH/d.vsub
	scw, sch := srcW/2, srcH/d.vsub
	srcHL := float32(sch * d.vsub) // source height in luma rows
	cbp, crp := base, base
	cbp.srcW, cbp.srcH = float32(srcW), srcHL
	crp.srcW, crp.srcH = float32(srcW), srcHL
	cbp.borderColor = float32(int(t.Border.ColorCb) << d.shift)
	crp.borderColor = float32(int(t.Border.ColorCr) << d.shift)
	cbp.shadowColor, crp.shadowColor = d.neutral, d.neutral
	kc := k
	kc.px, kc.px2 = &cbp, &crp
	kc.inner = innerRect(&cbp)
	kc.reach = reachRect(&cbp)
	kc.srcH = srcHL
	kc.vsub = d.vsub

	dCb := dst[dstW*dstH : dstW*dstH+dcw*dch]
	dCr := dst[dstW*dstH+dcw*dch : dstW*dstH+2*dcw*dch]
	sCb := src[srcW*srcH : srcW*srcH+scw*sch]
	sCr := src[srcW*srcH+scw*sch : srcW*srcH+2*scw*sch]

	minCX := max(aabb.Min.X/2-1, 0)
	maxCX := min(aabb.Max.X/2+1, dcw)
	minCY := max(aabb.Min.Y/d.vsub-1, 0)
	maxCY := min(aabb.Max.Y/d.vsub+1, dch)
	// Both kernels expand the chroma bounds by half the luma expansion,
	// 4:2:2 included.
	ec := int(expand*0.5 + 1)
	cy0, cy1 := max(minCY-ec, 0), min(maxCY+ec, dch-1)
	cx0, cx1 := max(minCX-ec, 0), min(maxCX+ec, dcw-1)
	rows(cy0, cy1+1, func(r0, r1 int) {
		for cy := r0; cy < r1; cy++ {
			chromaRow(&kc, dCb[cy*dcw:cy*dcw+dcw], dCr[cy*dcw:cy*dcw+dcw], sCb, sCr, scw, sch, cy, cx0, cx1)
		}
	})
}

// kernel holds the per-plane state of one composite.
type kernel struct {
	inv        Matrix3x3
	affine     bool    // inv has no projective row, so w is constant
	axis       bool    // affine with no rotation: source y depends on output y alone
	invW       float32 // 1/w when affine
	mirrorH    bool
	mirrorV    bool
	mosaic     float32
	srcW, srcH float32 // source size in luma samples
	vsub       int
	contrast   float32
	brightness float32
	saturation float32
	maxV       float32
	neutral    float32
	slotA256   int // alpha256 of the slot alpha
	px, px2    *pixelParams
	inner      [4]float32 // see innerRect
	reach      [4]float32 // see reachRect
}

// reachRect returns the source-space rectangle outside which
// computeDVEPixel's alpha is below the kernels' 0.002 draw threshold, so a
// sample there is skipped without evaluating it. With no shadow that is the
// visible rectangle: the rounded rect lies inside it and nothing outside the
// rounded rect is drawn. A shadow reaches further, by its offset and by the
// distance at which its falloff drops alpha under the threshold:
// 1/(1+t^2/2+t^4/8) < 8/t^4, which is at most 0.002/alpha from
// t = (8*alpha/0.002)^(1/4) on.
//
// The margin of one sample covers the SDF's float32 rounding: srcX-centerX
// rounds a coordinate just outside an edge onto it, and the kernel draws it.
func reachRect(p *pixelParams) [4]float32 {
	m := float32(1)
	if p.shadowEnabled {
		sigma := max(p.shadowBlur*0.5, 0.5)
		a := float64(p.shadowAlpha * p.slotAlpha)
		m += sigma*float32(math.Pow(8*a/0.002, 0.25)) + max(absf(p.shadowOffsetX), absf(p.shadowOffsetY))
	}
	return [4]float32{p.cropLeft - m, p.srcW - p.cropRight + m, p.cropTop - m, p.srcH - p.cropBottom + m}
}

func (k *kernel) outOfReach(x, y float32) bool {
	return x < k.reach[0] || x > k.reach[1] || y < k.reach[2] || y > k.reach[3]
}

// innerRect returns the source-space rectangle in which computeDVEPixel
// gives (sample, slotAlpha) for certain: at least the corner radius, the
// border with its softness and the crop softness away from every visible
// edge. Inside it the SDF needs no evaluating. A rectangle with min > max
// is empty.
func innerRect(p *pixelParams) [4]float32 {
	m := max(p.borderCornerRadius, p.borderWidth+p.borderSoftness, p.cropSoftness) + 1
	return [4]float32{p.cropLeft + m, p.srcW - p.cropRight - m, p.cropTop + m, p.srcH - p.cropBottom - m}
}

func (k *kernel) inInner(x, y float32) bool {
	return x >= k.inner[0] && x <= k.inner[1] && y >= k.inner[2] && y <= k.inner[3]
}

// project maps output (x, y) to source space, mirror and mosaic included.
func (k *kernel) project(x, y float32) (float32, float32) {
	var sx, sy float32
	if k.affine {
		sx = (k.inv[0]*x + k.inv[1]*y + k.inv[2]) * k.invW
		sy = (k.inv[3]*x + k.inv[4]*y + k.inv[5]) * k.invW
	} else {
		sx, sy = project(&k.inv, x, y)
	}
	return sx, sy
}

// projectX and projectY are project's two halves for an axis transform,
// where m01 and m10 are zero and drop out of the GPU kernel's sums exactly.
func (k *kernel) projectX(x float32) float32 { return (k.inv[0]*x + k.inv[2]) * k.invW }
func (k *kernel) projectY(y float32) float32 { return (k.inv[4]*y + k.inv[5]) * k.invW }

func (k *kernel) mirrorMosaicX(sx float32) float32 {
	if k.mirrorH {
		sx = k.srcW - 1 - sx
	}
	if k.mosaic > 0 {
		sx = float32(math.Floor(float64(sx/k.mosaic)))*k.mosaic + k.mosaic*0.5
	}
	return sx
}

func (k *kernel) mirrorMosaicY(sy float32) float32 {
	if k.mirrorV {
		sy = k.srcH - 1 - sy
	}
	if k.mosaic > 0 {
		sy = float32(math.Floor(float64(sy/k.mosaic)))*k.mosaic + k.mosaic*0.5
	}
	return sy
}

func (k *kernel) mirrorMosaic(sx, sy float32) (float32, float32) {
	if k.mirrorH {
		sx = k.srcW - 1 - sx
	}
	if k.mirrorV {
		sy = k.srcH - 1 - sy
	}
	if k.mosaic > 0 {
		ms := k.mosaic
		sx = float32(math.Floor(float64(sx/ms)))*ms + ms*0.5
		sy = float32(math.Floor(float64(sy/ms)))*ms + ms*0.5
	}
	return sx, sy
}

// put blends value v into *p at 8.8 fixed-point alpha a, as the kernels
// do: (dst*(256-a) + round(v)*a + 128) >> 8. Values never exceed the
// largest code, so neither does the blend, and the kernels' clamp is a
// no-op.
func put[T sample](p *T, v float32, a int) {
	if a == 256 {
		*p = T(int(v + 0.5))
		return
	}
	*p = T((int(*p)*(256-a) + int(v+0.5)*a + 128) >> 8)
}

func lumaRow[T sample](k *kernel, dst, src []T, srcW, srcH, oy, x0, x1 int) {
	if k.axis {
		lumaRowAxis(k, dst, src, srcW, srcH, oy, x0, x1)
		return
	}
	fy := float32(oy)
	p := k.px
	for ox := x0; ox <= x1; ox++ {
		sx, sy := k.project(float32(ox), fy)
		sx, sy = k.mirrorMosaic(sx, sy)
		a := k.slotA256
		if !k.inInner(sx, sy) {
			if k.outOfReach(sx, sy) {
				continue
			}
			val, af, sampled := p.computeDVEPixel(sx, sy)
			if af < 0.002 {
				continue
			}
			if a = alpha256(af); !sampled {
				put(&dst[ox], val, a)
				continue
			}
		}
		v := bilinear(src, sx, sy, srcW, srcH, srcW)
		if k.contrast != 1 || k.brightness != 0 {
			n := v / k.maxV
			n = min(max((n-0.5)*k.contrast+0.5+k.brightness, 0), 1)
			v = n * k.maxV
		}
		put(&dst[ox], v, a)
	}
}

func lumaRowAxis[T sample](k *kernel, dst, src []T, srcW, srcH, oy, x0, x1 int) {
	p := k.px
	sy := k.mirrorMosaicY(k.projectY(float32(oy)))
	rs := newRowSampler(src, sy, srcW, srcH, srcW)
	if sy < k.reach[2] || sy > k.reach[3] {
		return
	}
	rowInner := sy >= k.inner[2] && sy <= k.inner[3]
	for ox := x0; ox <= x1; ox++ {
		sx := k.mirrorMosaicX(k.projectX(float32(ox)))
		a := k.slotA256
		if !rowInner || sx < k.inner[0] || sx > k.inner[1] {
			if sx < k.reach[0] || sx > k.reach[1] {
				continue
			}
			val, af, sampled := p.computeDVEPixel(sx, sy)
			if af < 0.002 {
				continue
			}
			if a = alpha256(af); !sampled {
				put(&dst[ox], val, a)
				continue
			}
		}
		v := rs.at(sx)
		if k.contrast != 1 || k.brightness != 0 {
			n := v / k.maxV
			n = min(max((n-0.5)*k.contrast+0.5+k.brightness, 0), 1)
			v = n * k.maxV
		}
		put(&dst[ox], v, a)
	}
}

// chromaRowAxis is chromaRow for an axis transform. It sums the same four
// (4:2:0) or two (4:2:2) projected coordinates in the same order as the GPU
// kernel, so the averages are bit for bit the kernel's.
func chromaRowAxis[T sample](k *kernel, dCb, dCr, sCb, sCr []T, scw, sch, cy, x0, x1 int) {
	var sy float32
	if k.vsub == 2 {
		ya := k.projectY(float32(cy * 2))
		yb := k.projectY(float32(cy*2 + 1))
		sy = (ya + ya + yb + yb) * 0.25
	} else {
		ya := k.projectY(float32(cy))
		sy = (ya + ya) * 0.5
	}
	sy = k.mirrorMosaicY(sy)
	scy := sy / float32(k.vsub)
	rb := newRowSampler(sCb, scy, scw, sch, scw)
	rr := newRowSampler(sCr, scy, scw, sch, scw)
	if sy < k.reach[2] || sy > k.reach[3] {
		return
	}
	rowInner := sy >= k.inner[2] && sy <= k.inner[3]
	for cx := x0; cx <= x1; cx++ {
		xl := k.projectX(float32(cx * 2))
		xr := k.projectX(float32(cx*2 + 1))
		var sx float32
		if k.vsub == 2 {
			sx = (xl + xr + xl + xr) * 0.25
		} else {
			sx = (xl + xr) * 0.5
		}
		sx = k.mirrorMosaicX(sx)
		ab, ar := k.slotA256, k.slotA256
		var vb, vr float32
		sampled := true
		if !rowInner || sx < k.inner[0] || sx > k.inner[1] {
			if sx < k.reach[0] || sx > k.reach[1] {
				continue
			}
			var abf, arf float32
			vb, abf, sampled = k.px.computeDVEPixel(sx, sy)
			vr, arf, _ = k.px2.computeDVEPixel(sx, sy)
			if abf < 0.002 && arf < 0.002 {
				continue
			}
			ab, ar = alpha256(abf), alpha256(arf)
		}
		if sampled {
			scx := sx * 0.5
			vb = rb.at(scx)
			vr = rr.at(scx)
			if k.saturation != 1 {
				vb = min(max(k.neutral+(vb-k.neutral)*k.saturation, 0), k.maxV)
				vr = min(max(k.neutral+(vr-k.neutral)*k.saturation, 0), k.maxV)
			}
		}
		put(&dCb[cx], vb, ab)
		put(&dCr[cx], vr, ar)
	}
}

func chromaRow[T sample](k *kernel, dCb, dCr, sCb, sCr []T, scw, sch, cy, x0, x1 int) {
	if k.axis {
		chromaRowAxis(k, dCb, dCr, sCb, sCr, scw, sch, cy, x0, x1)
		return
	}
	for cx := x0; cx <= x1; cx++ {
		// Average the inverse map over the luma samples this chroma sample
		// covers: 2x2 for 4:2:0, 2x1 for 4:2:2.
		var sx, sy float32
		lx0 := float32(cx * 2)
		for j := 0; j < k.vsub; j++ {
			ly := float32(cy*k.vsub + j)
			for i := 0; i < 2; i++ {
				px, py := k.project(lx0+float32(i), ly)
				sx += px
				sy += py
			}
		}
		if k.vsub == 2 {
			sx *= 0.25
			sy *= 0.25
		} else {
			sx *= 0.5
			sy *= 0.5
		}
		sx, sy = k.mirrorMosaic(sx, sy)
		ab, ar := k.slotA256, k.slotA256
		var vb, vr float32
		sampled := true
		if !k.inInner(sx, sy) {
			if k.outOfReach(sx, sy) {
				continue
			}
			// Cb and Cr differ only in their border colour, so the
			// geometry, and with it whether to sample, is the same.
			var abf, arf float32
			vb, abf, sampled = k.px.computeDVEPixel(sx, sy)
			vr, arf, _ = k.px2.computeDVEPixel(sx, sy)
			if abf < 0.002 && arf < 0.002 {
				continue
			}
			ab, ar = alpha256(abf), alpha256(arf)
		}
		if sampled {
			scx := sx * 0.5
			scy := sy / float32(k.vsub)
			vb = bilinear(sCb, scx, scy, scw, sch, scw)
			vr = bilinear(sCr, scx, scy, scw, sch, scw)
			if k.saturation != 1 {
				vb = min(max(k.neutral+(vb-k.neutral)*k.saturation, 0), k.maxV)
				vr = min(max(k.neutral+(vr-k.neutral)*k.saturation, 0), k.maxV)
			}
		}
		put(&dCb[cx], vb, ab)
		put(&dCr[cx], vr, ar)
	}
}

// rows runs fn over [r0, r1) in contiguous bands on up to cpuWorkers
// goroutines.
func rows(r0, r1 int, fn func(a, b int)) {
	n := r1 - r0
	if n <= 0 {
		return
	}
	workers := min(cpuWorkers, n)
	if workers <= 1 {
		fn(r0, r1)
		return
	}
	var wg sync.WaitGroup
	band := (n + workers - 1) / workers
	for a := r0; a < r1; a += band {
		b := min(a+band, r1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(a, b)
		}()
	}
	wg.Wait()
}

// cpuWorkers bounds the goroutines one CPU composite runs on.
var cpuWorkers = min(runtime.GOMAXPROCS(0), 8)
