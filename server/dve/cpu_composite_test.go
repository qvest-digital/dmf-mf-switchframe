package dve

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"testing"

	"github.com/zsiec/switchframe/server/internal/video"
)

// refComposite transcribes the GPU DVE kernels of gpu/cuda/dve.cu one
// output sample at a time, without the CPU path's shortcuts: every sample
// in the expanded slot bounds is projected, sampled and run through
// computeDVEPixel, including the soft-edge branch the CPU path proves is
// zero. Planes are float64-free float32, as on the GPU.
func refComposite(dst []byte, w, h int, src []byte, f video.Format, t Transform) {
	inv := ComputeInverseTransform(t, w, h)
	aabb := ComputeAABB(t, w, h, w, h)
	ten := f == video.YUV422_10bit
	maxV, neutral, shift, vsub := float32(255), float32(128), uint(0), 2
	if ten {
		maxV, neutral, shift, vsub = 1023, 512, 2, 1
	}
	get := func(buf []byte, i int) float32 {
		if ten {
			return float32(binary.LittleEndian.Uint16(buf[i*2:]))
		}
		return float32(buf[i])
	}
	set := func(buf []byte, i, v int) {
		if ten {
			binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
		} else {
			buf[i] = byte(v)
		}
	}
	sat := func(x float32) float32 { return float32(math.Min(math.Max(float64(x), 0), 1)) }
	sdf := func(px, py, hw, hh, cr float32) float32 {
		dx := float32(math.Abs(float64(px))) - (hw - cr)
		dy := float32(math.Abs(float64(py))) - (hh - cr)
		dxc, dyc := float32(math.Max(float64(dx), 0)), float32(math.Max(float64(dy), 0))
		return float32(math.Sqrt(float64(dxc*dxc+dyc*dyc))) + float32(math.Min(math.Max(float64(dx), float64(dy)), 0)) - cr
	}
	falloff := func(d, sigma float32) float32 {
		if sigma <= 0 {
			return 0
		}
		tt := d / sigma
		t2 := tt * tt
		return 1 / (1 + 0.5*t2 + 0.125*t2*t2)
	}
	pixel := func(srcX, srcY, srcW, srcH, borderColor, shadowColor, sampled, slotAlpha, shadowAlpha float32) (float32, float32) {
		visL, visR := float32(t.CropLeft), srcW-float32(t.CropRight)
		visT, visB := float32(t.CropTop), srcH-float32(t.CropBottom)
		hw, hh := (visR-visL)*0.5, (visB-visT)*0.5
		if hw <= 0 || hh <= 0 {
			return 0, 0
		}
		cx, cy := visL+hw, visT+hh
		cr := float32(math.Min(t.Border.CornerRadius, math.Min(float64(hw), float64(hh))))
		dist := sdf(srcX-cx, srcY-cy, hw, hh, cr)
		bw, bs := float32(t.Border.Width), float32(t.Border.Softness)
		if t.Shadow.Enabled && dist > 0 {
			sd := sdf(srcX-float32(t.Shadow.OffsetX)-cx, srcY-float32(t.Shadow.OffsetY)-cy, hw, hh, cr)
			if sd > 0 {
				sigma := float32(math.Max(float64(float32(t.Shadow.Blur)*0.5), 0.5))
				return shadowColor, shadowAlpha * falloff(sd, sigma) * slotAlpha
			}
			return shadowColor, shadowAlpha * slotAlpha
		}
		if bw > 0 && dist > -bw && dist <= 0 {
			a := float32(1)
			if bs > 0 {
				a = sat(-dist/float32(math.Max(float64(bs), 0.001))) * sat((dist+bw)/float32(math.Max(float64(bs), 0.001)))
			}
			return borderColor, a * slotAlpha
		}
		if dist > 0 {
			if bs > 0 {
				if ea := sat(-dist / float32(math.Max(float64(bs), 0.001))); ea > 0.001 {
					return sampled, ea * slotAlpha
				}
			}
			return 0, 0
		}
		va := float32(1)
		if cs := float32(t.CropSoftness); cs > 0 {
			va = float32(math.Min(math.Min(float64(sat((srcX-visL)/cs)), float64(sat((visR-srcX)/cs))),
				math.Min(float64(sat((srcY-visT)/cs)), float64(sat((visB-srcY)/cs)))))
		}
		if bw > 0 && bs > 0 && dist > -(bw+bs) {
			va *= sat((-dist - bw) / float32(math.Max(float64(bs), 0.001)))
		}
		return sampled, va * slotAlpha
	}
	proj := func(x, y float32) (float32, float32) {
		ww := inv[6]*x + inv[7]*y + inv[8]
		var iw float32
		if math.Abs(float64(ww)) > 1e-6 {
			iw = 1 / ww
		}
		return (inv[0]*x + inv[1]*y + inv[2]) * iw, (inv[3]*x + inv[4]*y + inv[5]) * iw
	}
	bil := func(plane []byte, off int, x, y float32, pw, ph int) float32 {
		x = float32(math.Max(0, math.Min(float64(x), float64(pw-1))))
		y = float32(math.Max(0, math.Min(float64(y), float64(ph-1))))
		sx, sy := int(x), int(y)
		fx, fy := x-float32(sx), y-float32(sy)
		sx1, sy1 := min(sx+1, pw-1), min(sy+1, ph-1)
		v00, v10 := get(plane, off+sy*pw+sx), get(plane, off+sy*pw+sx1)
		v01, v11 := get(plane, off+sy1*pw+sx), get(plane, off+sy1*pw+sx1)
		top := v00 + (v10-v00)*fx
		bot := v01 + (v11-v01)*fx
		return top + (bot-top)*fy
	}
	blend := func(buf []byte, i int, v, a float32) {
		a256 := min(int(a*256+0.5), 256)
		b := (int(get(buf, i))*(256-a256) + int(v+0.5)*a256 + 128) >> 8
		set(buf, i, min(max(b, 0), int(maxV)))
	}
	mirMos := func(x, y, hh float32) (float32, float32) {
		if t.MirrorH {
			x = float32(w) - 1 - x
		}
		if t.MirrorV {
			y = hh - 1 - y
		}
		if ms := float32(t.MosaicSize); ms > 0 {
			x = float32(math.Floor(float64(x/ms)))*ms + ms*0.5
			y = float32(math.Floor(float64(y/ms)))*ms + ms*0.5
		}
		return x, y
	}

	a256 := min(max(int(t.EffectiveOpacity()*256), 0), 256)
	slotAlpha := float32(a256) / 256
	shA := float32(0)
	if t.Shadow.Enabled {
		shA = float32(min(max(int(t.Shadow.Opacity*256), 0), 256)) / 256
	}
	contrast, saturation := float32(t.Contrast), float32(t.Saturation)
	if contrast == 0 {
		contrast = 1
	}
	if saturation == 0 {
		saturation = 1
	}
	expand := float32(t.Border.Width + t.Border.Softness)
	if t.Shadow.Enabled {
		expand = float32(math.Max(float64(expand), math.Abs(t.Shadow.OffsetX)+t.Shadow.Blur*2))
		expand = float32(math.Max(float64(expand), math.Abs(t.Shadow.OffsetY)+t.Shadow.Blur*2))
	}
	e := int(expand + 1)
	for oy := 0; oy < h; oy++ {
		for ox := 0; ox < w; ox++ {
			if ox < aabb.Min.X-e || ox > aabb.Max.X+e || oy < aabb.Min.Y-e || oy > aabb.Max.Y+e {
				continue
			}
			sx, sy := proj(float32(ox), float32(oy))
			sx, sy = mirMos(sx, sy, float32(h))
			v := bil(src, 0, sx, sy, w, h)
			if contrast != 1 || t.Brightness != 0 {
				n := v / maxV
				n = float32(math.Min(math.Max(float64((n-0.5)*contrast+0.5+float32(t.Brightness)), 0), 1))
				v = n * maxV
			}
			val, a := pixel(sx, sy, float32(w), float32(h), float32(int(t.Border.ColorY)<<shift), float32(int(t.Shadow.ColorY)<<shift), v, slotAlpha, shA)
			if a < 0.002 {
				continue
			}
			blend(dst, oy*w+ox, val, a)
		}
	}
	cw, ch := w/2, h/vsub
	minCX, maxCX := max(aabb.Min.X/2-1, 0), min(aabb.Max.X/2+1, cw)
	minCY, maxCY := max(aabb.Min.Y/vsub-1, 0), min(aabb.Max.Y/vsub+1, ch)
	ec := int(expand*0.5 + 1)
	hl := float32(ch * vsub)
	for cy := 0; cy < ch; cy++ {
		for cx := 0; cx < cw; cx++ {
			if cx < minCX-ec || cx > maxCX+ec || cy < minCY-ec || cy > maxCY+ec {
				continue
			}
			var sx, sy float32
			for j := 0; j < vsub; j++ {
				for i := 0; i < 2; i++ {
					px, py := proj(float32(cx*2+i), float32(cy*vsub+j))
					sx += px
					sy += py
				}
			}
			if vsub == 2 {
				sx, sy = sx*0.25, sy*0.25
			} else {
				sx, sy = sx*0.5, sy*0.5
			}
			sx, sy = mirMos(sx, sy, hl)
			cbOff, crOff := w*h, w*h+cw*ch
			cb := bil(src, cbOff, sx*0.5, sy/float32(vsub), cw, ch)
			cr := bil(src, crOff, sx*0.5, sy/float32(vsub), cw, ch)
			if saturation != 1 {
				cb = float32(math.Min(math.Max(float64(neutral+(cb-neutral)*saturation), 0), float64(maxV)))
				cr = float32(math.Min(math.Max(float64(neutral+(cr-neutral)*saturation), 0), float64(maxV)))
			}
			vb, ab := pixel(sx, sy, float32(w), hl, float32(int(t.Border.ColorCb)<<shift), neutral, cb, slotAlpha, shA)
			vr, ar := pixel(sx, sy, float32(w), hl, float32(int(t.Border.ColorCr)<<shift), neutral, cr, slotAlpha, shA)
			if ab < 0.002 && ar < 0.002 {
				continue
			}
			blend(dst, cbOff+cy*cw+cx, vb, ab)
			blend(dst, crOff+cy*cw+cx, vr, ar)
		}
	}
}

// textured returns a frame of format f whose samples all differ from their
// neighbours, so any wrong coordinate shows.
func textured(f video.Format, w, h int, seed int) []byte {
	buf := make([]byte, f.FrameSize(w, h))
	if f == video.YUV422_10bit {
		for i := 0; i < len(buf)/2; i++ {
			binary.LittleEndian.PutUint16(buf[i*2:], uint16((i*37+seed*101)%877+64))
		}
		return buf
	}
	for i := range buf {
		buf[i] = byte((i*37+seed*101)%220 + 16)
	}
	return buf
}

func testTransforms(w, h int) map[string]Transform {
	fw, fh := float64(w), float64(h)
	out := map[string]Transform{}
	add := func(name string, mod func(*Transform)) {
		t := DefaultTransform(0, 0, fw, fh)
		mod(&t)
		out[name] = t
	}
	add("identity", func(t *Transform) {})
	add("shift-int", func(t *Transform) { t.PositionX = -10 })
	add("shift-frac", func(t *Transform) { t.PositionX, t.PositionY = 7.3, -3.6 })
	add("scale", func(t *Transform) { t.Width, t.Height = fw*0.55, fh*0.4; t.PositionX = 9 })
	add("rotate", func(t *Transform) { t.RotationDeg = 23; t.Width, t.Height = fw*0.7, fh*0.7 })
	add("perspective", func(t *Transform) { t.PerspectiveX, t.PerspectiveY = 0.0008, -0.0006; t.Width = fw * 0.8 })
	add("crop-soft", func(t *Transform) { t.CropLeft, t.CropRight, t.CropTop, t.CropSoftness = 11, 7, 5, 4 })
	add("border", func(t *Transform) {
		t.Width, t.Height = fw*0.6, fh*0.6
		t.PositionX, t.PositionY = 8, 6
		t.Border = BorderConfig{Width: 3, ColorY: 200, ColorCb: 90, ColorCr: 170, CornerRadius: 6, Softness: 1.5}
	})
	add("shadow", func(t *Transform) {
		t.Width, t.Height = fw*0.5, fh*0.5
		t.PositionX, t.PositionY = 10, 8
		t.RotationDeg = -12
		t.Shadow = ShadowConfig{Enabled: true, OffsetX: -4, OffsetY: 5, Blur: 6, Opacity: 0.6, ColorY: 20}
		t.Opacity = 0.8
	})
	add("mirror-mosaic", func(t *Transform) { t.MirrorH, t.MirrorV, t.MosaicSize = true, true, 4 })
	add("color", func(t *Transform) { t.Brightness, t.Contrast, t.Saturation = 0.1, 1.4, 0.3 })
	add("opacity", func(t *Transform) { t.Opacity = 0.45; t.PositionY = 4.5 })
	return out
}

// TestCompositeCPUMatchesGPUKernel checks every shortcut of the CPU path
// (axis rows, the inner and reach rectangles, deferred sampling, integer
// blending) against the plain transcription, for both pixel formats.
func TestCompositeCPUMatchesGPUKernel(t *testing.T) {
	const w, h = 64, 36
	for _, f := range []video.Format{video.YUV420_8bit, video.YUV422_10bit} {
		for name, tr := range testTransforms(w, h) {
			t.Run(fmt.Sprintf("%v/%s", f, name), func(t *testing.T) {
				src := textured(f, w, h, 1)
				want := textured(f, w, h, 2)
				got := append([]byte(nil), want...)
				refComposite(want, w, h, src, f, tr)
				CompositeCPU(got, w, h, src, w, h, f, tr, ComputeInverseTransform(tr, w, h), ComputeAABB(tr, w, h, w, h))
				tol := 0
				if runtime.GOARCH != "amd64" {
					// Other targets may fuse multiply-adds differently in
					// the two code shapes.
					tol = 1
				}
				step, n := 1, len(got)
				if f == video.YUV422_10bit {
					step, n = 2, len(got)/2
				}
				bad := 0
				for i := 0; i < n; i++ {
					var g, wv int
					if step == 2 {
						g, wv = int(binary.LittleEndian.Uint16(got[i*2:])), int(binary.LittleEndian.Uint16(want[i*2:]))
					} else {
						g, wv = int(got[i]), int(want[i])
					}
					if d := g - wv; d < -tol || d > tol {
						if bad < 5 {
							t.Errorf("sample %d: got %d, GPU kernel gives %d", i, g, wv)
						}
						bad++
					}
				}
				if bad > 0 {
					t.Fatalf("%d of %d samples differ", bad, n)
				}
			})
		}
	}
}

// TestCPUTransitionPushIsAShift pins the geometry independently of the
// kernel code: an integer push moves the outgoing frame left and brings the
// incoming one in from the right, sample for sample.
func TestCPUTransitionPushIsAShift(t *testing.T) {
	const w, h = 64, 16
	f := video.YUV420_8bit
	from := textured(f, w, h, 3)
	to := textured(f, w, h, 4)
	// Flat chroma per source: the kernel samples chroma a quarter sample
	// right of the luma grid, which a flat plane hides.
	for i := w * h; i < len(from); i++ {
		from[i], to[i] = 100, 60
	}
	dst := make([]byte, f.FrameSize(w, h))
	mustRender(t, CPUTransitionForType("dve-push-left")(dst, from, to, w, h, f, 0.25))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var want byte
			switch {
			case x < 48:
				want = from[y*w+x+16]
			case x == 48:
				// The slot's right edge is inclusive, as on the GPU: the
				// outgoing frame's last column, clamped, covers it.
				want = from[y*w+w-1]
			default:
				want = to[y*w+x-48]
			}
			if dst[y*w+x] != want {
				t.Fatalf("Y(%d,%d) = %d, want %d", x, y, dst[y*w+x], want)
			}
		}
	}
	cw := w / 2
	for y := 0; y < h/2; y++ {
		for x := 0; x < cw; x++ {
			cb := dst[w*h+y*cw+x]
			if x < 23 && cb != 100 || x > 24 && cb != 60 {
				t.Fatalf("Cb(%d,%d) = %d", x, y, cb)
			}
		}
	}
}

func TestCPUTransitionEndpoints(t *testing.T) {
	const w, h = 64, 36
	for _, f := range []video.Format{video.YUV420_8bit, video.YUV422_10bit} {
		from := textured(f, w, h, 5)
		to := textured(f, w, h, 6)
		for _, ty := range []string{"dve-push-left", "dve-push-up", "dve-reveal-right", "dve-squeeze"} {
			fn := CPUTransitionForType(ty)
			dst := make([]byte, f.FrameSize(w, h))
			mustRender(t, fn(dst, from, to, w, h, f, 0))
			if string(dst[:w*h*f.BytesPerSample()]) != string(from[:w*h*f.BytesPerSample()]) {
				t.Errorf("%s %v at 0: luma is not the outgoing frame", ty, f)
			}
		}
	}
}

func TestCPUTransitionRefuses3D(t *testing.T) {
	const w, h = 64, 36
	f := video.YUV420_8bit
	dst := make([]byte, f.FrameSize(w, h))
	for _, ty := range []string{"dve-cube", "dve-flip", "dve-globe", "dve-page-turn"} {
		if CPUTransitionForType(ty)(dst, textured(f, w, h, 1), textured(f, w, h, 2), w, h, f, 0.5) {
			t.Errorf("%s: 3D transition rendered on the 2D CPU path", ty)
		}
	}
	if CPUTransitionForType("dve-nope") != nil {
		t.Error("unknown type has a renderer")
	}
}

func mustRender(t *testing.T, ok bool) {
	t.Helper()
	if !ok {
		t.Fatal("renderer refused the frame")
	}
}

// TestCompositeCPURandomTransforms widens the comparison to random
// transforms, where slot edges fall anywhere relative to the sample grid.
func TestCompositeCPURandomTransforms(t *testing.T) {
	const w, h = 48, 28
	rng := rand.New(rand.NewPCG(1, 2))
	r := func(lo, hi float64) float64 { return lo + rng.Float64()*(hi-lo) }
	for n := 0; n < 300; n++ {
		f := video.YUV420_8bit
		if n%2 == 1 {
			f = video.YUV422_10bit
		}
		tr := DefaultTransform(r(-20, 20), r(-12, 12), r(8, 70), r(6, 40))
		if rng.IntN(2) == 0 {
			tr.RotationDeg = r(-180, 180)
		}
		if rng.IntN(4) == 0 {
			tr.PerspectiveX, tr.PerspectiveY = r(-0.004, 0.004), r(-0.004, 0.004)
		}
		if rng.IntN(3) == 0 {
			tr.CropLeft, tr.CropRight, tr.CropTop, tr.CropBottom = r(0, 15), r(0, 15), r(0, 9), r(0, 9)
			tr.CropSoftness = r(0, 5)
		}
		if rng.IntN(3) == 0 {
			tr.Border = BorderConfig{Width: r(0, 4), ColorY: 210, ColorCb: 40, ColorCr: 220, CornerRadius: r(0, 8), Softness: r(0, 2)}
		}
		if rng.IntN(3) == 0 {
			tr.Shadow = ShadowConfig{Enabled: true, OffsetX: r(-6, 6), OffsetY: r(-6, 6), Blur: r(0, 8), Opacity: r(0, 1), ColorY: 16}
		}
		if rng.IntN(4) == 0 {
			tr.MirrorH, tr.MirrorV = rng.IntN(2) == 0, rng.IntN(2) == 0
		}
		if rng.IntN(5) == 0 {
			tr.MosaicSize = 1 + rng.IntN(6)
		}
		tr.Opacity = []float64{1, 1, r(0.05, 1)}[rng.IntN(3)]
		src := textured(f, w, h, n)
		want := textured(f, w, h, n+1)
		got := append([]byte(nil), want...)
		refComposite(want, w, h, src, f, tr)
		CompositeCPU(got, w, h, src, w, h, f, tr, ComputeInverseTransform(tr, w, h), ComputeAABB(tr, w, h, w, h))
		if d := maxSampleDiff(got, want, f); d > tolerance() {
			t.Fatalf("case %d %v %+v: CPU composite differs from the GPU kernel by %d", n, f, tr, d)
		}
	}
}

// TestCPURefusalMatchesTheUI pins the split the control surface shows: its
// 3D group is 3D, its 2D group is not.
func TestCPURefusalMatchesTheUI(t *testing.T) {
	threeD := []string{"dve-cube", "dve-cube-roll", "dve-flip", "dve-door", "dve-swing-door",
		"dve-fold", "dve-carousel", "dve-tumble", "dve-globe", "dve-page-turn"}
	twoD := []string{"dve-push-left", "dve-push-right", "dve-push-up", "dve-push-down",
		"dve-reveal-left", "dve-reveal-right", "dve-reveal-up", "dve-reveal-down",
		"dve-barn-h", "dve-barn-v", "dve-squeeze", "dve-zoom", "dve-cross-zoom",
		"dve-shrink-grow", "dve-spin", "dve-scale-rotate", "dve-swing-left",
		"dve-swing-right", "dve-corner-peel", "dve-fly", "dve-mosaic", "dve-bounce",
		"dve-blur-dissolve"}
	for _, ty := range threeD {
		if !Is3DTransition(ty) {
			t.Errorf("%s: not 3D", ty)
		}
	}
	for _, ty := range twoD {
		if Is3DTransition(ty) {
			t.Errorf("%s: 3D", ty)
		}
	}
	if Is3DTransition("dve-nope") || Is3DTransition("mix") {
		t.Error("an unknown type reads as 3D")
	}

	// The CPU refuses every 3D type and, of the 2D ones, corner-peel only.
	refused := CPURefusedTransitions()
	for _, ty := range threeD {
		if refused[ty] != reason3D {
			t.Errorf("%s: refusal %q", ty, refused[ty])
		}
	}
	for _, ty := range twoD {
		if _, ok := refused[ty]; ok != (ty == "dve-corner-peel") {
			t.Errorf("%s: refused=%v", ty, ok)
		}
	}
	if len(refused) != len(threeD)+1 {
		t.Errorf("refuses %d types", len(refused))
	}
	refused["dve-push-left"] = "x"
	if _, ok := CPURefusedTransitions()["dve-push-left"]; ok {
		t.Error("callers can change the refusal list")
	}
}

// tolerance is the largest sample difference accepted against the kernel
// transcription: none on amd64, one code elsewhere, where the compiler may
// fuse multiply-adds differently in the two code shapes.
func tolerance() int {
	if runtime.GOARCH == "amd64" {
		return 0
	}
	return 1
}

func maxSampleDiff(a, b []byte, f video.Format) int {
	worst := 0
	if f == video.YUV422_10bit {
		for i := 0; i+1 < len(a); i += 2 {
			d := int(binary.LittleEndian.Uint16(a[i:])) - int(binary.LittleEndian.Uint16(b[i:]))
			worst = max(worst, d, -d)
		}
		return worst
	}
	for i := range a {
		d := int(a[i]) - int(b[i])
		worst = max(worst, d, -d)
	}
	return worst
}

// Is3DTransition reports whether a DVE transition type moves either source
// through a 3D transform at any of a few positions.
func Is3DTransition(transitionType string) bool {
	fn := DVETransitionForType(transitionType)
	if fn == nil {
		return false
	}
	for _, pos := range []float64{0.25, 0.5, 0.75} {
		prog, prev := fn(pos, 1920, 1080)
		if prog.Is3D() || prev.Is3D() {
			return true
		}
	}
	return false
}
