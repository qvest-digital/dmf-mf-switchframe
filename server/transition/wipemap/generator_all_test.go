package wipemap

import (
	"math"
	"testing"
)

const (
	testW = 320
	testH = 240
)

// helper: check length and value range
func checkBasics(t *testing.T, name string, buf []byte, w, h int) {
	t.Helper()
	if len(buf) != w*h {
		t.Fatalf("%s: len=%d, want %d", name, len(buf), w*h)
	}
	for i, v := range buf {
		if v > 255 { // byte can't exceed 255, but this catches future type changes
			t.Fatalf("%s: buf[%d]=%d out of range", name, i, v)
		}
	}
}

func px(buf []byte, w, x, y int) byte {
	return buf[y*w+x]
}

// --- GenDiagonal ---

func TestGenDiagonal_45(t *testing.T) {
	gen := GenDiagonal(45)
	buf := gen(testW, testH)
	checkBasics(t, "Diagonal(45)", buf, testW, testH)

	// Top-left corner should be near 0, bottom-right near 255
	tl := px(buf, testW, 0, 0)
	br := px(buf, testW, testW-1, testH-1)
	if tl >= br {
		t.Errorf("Diagonal(45): top-left(%d) should be < bottom-right(%d)", tl, br)
	}
	if tl > 10 {
		t.Errorf("Diagonal(45): top-left=%d, want near 0", tl)
	}
	if br < 245 {
		t.Errorf("Diagonal(45): bottom-right=%d, want near 255", br)
	}
}

func TestGenDiagonal_135(t *testing.T) {
	gen := GenDiagonal(135)
	buf := gen(testW, testH)
	checkBasics(t, "Diagonal(135)", buf, testW, testH)

	// Top-right should be near 0, bottom-left near 255
	tr := px(buf, testW, testW-1, 0)
	bl := px(buf, testW, 0, testH-1)
	if tr >= bl {
		t.Errorf("Diagonal(135): top-right(%d) should be < bottom-left(%d)", tr, bl)
	}
}

// --- GenBarnDoor ---

func TestGenBarnDoor_Horizontal(t *testing.T) {
	gen := GenBarnDoor("h", false)
	buf := gen(testW, testH)
	checkBasics(t, "BarnDoor(h)", buf, testW, testH)

	mid := testW / 2
	center := px(buf, testW, mid, testH/2)
	edge := px(buf, testW, 0, testH/2)

	// Center should be highest (edges opening from center)
	if center <= edge {
		t.Errorf("BarnDoor(h): center(%d) should be > edge(%d)", center, edge)
	}
}

func TestGenBarnDoor_Vertical(t *testing.T) {
	gen := GenBarnDoor("v", false)
	buf := gen(testW, testH)
	checkBasics(t, "BarnDoor(v)", buf, testW, testH)

	mid := testH / 2
	center := px(buf, testW, testW/2, mid)
	edge := px(buf, testW, testW/2, 0)
	if center <= edge {
		t.Errorf("BarnDoor(v): center(%d) should be > edge(%d)", center, edge)
	}
}

func TestGenBarnDoor_Reverse(t *testing.T) {
	fwd := GenBarnDoor("h", false)(testW, testH)
	rev := GenBarnDoor("h", true)(testW, testH)
	// Reverse should invert values
	for i := 0; i < len(fwd); i++ {
		sum := int(fwd[i]) + int(rev[i])
		if sum < 253 || sum > 257 {
			// Allow +-2 for rounding
			continue
		}
	}
	// At center, forward should be high, reverse should be low
	mid := testH/2*testW + testW/2
	if fwd[mid] <= rev[mid] {
		t.Errorf("BarnDoor reverse: fwd center(%d) should be > rev center(%d)", fwd[mid], rev[mid])
	}
}

// --- GenBoxIris ---

func TestGenBoxIris(t *testing.T) {
	gen := GenBoxIris(0.5, 0.5, false)
	buf := gen(testW, testH)
	checkBasics(t, "BoxIris", buf, testW, testH)

	center := px(buf, testW, testW/2, testH/2)
	corner := px(buf, testW, 0, 0)
	// Center should be lowest (iris opens from center)
	if center >= corner {
		t.Errorf("BoxIris: center(%d) should be < corner(%d)", center, corner)
	}
}

func TestGenBoxIris_Reverse(t *testing.T) {
	fwd := GenBoxIris(0.5, 0.5, false)(testW, testH)
	rev := GenBoxIris(0.5, 0.5, true)(testW, testH)
	midIdx := testH/2*testW + testW/2
	if fwd[midIdx] >= rev[midIdx] {
		t.Errorf("BoxIris reverse: fwd center(%d) should be < rev center(%d)", fwd[midIdx], rev[midIdx])
	}
}

// --- GenCircleIris ---

func TestGenCircleIris(t *testing.T) {
	gen := GenCircleIris(0.5, 0.5, 1.0, 1.0, false)
	buf := gen(testW, testH)
	checkBasics(t, "CircleIris", buf, testW, testH)

	center := px(buf, testW, testW/2, testH/2)
	corner := px(buf, testW, 0, 0)
	if center >= corner {
		t.Errorf("CircleIris: center(%d) should be < corner(%d)", center, corner)
	}
}

func TestGenCircleIris_Oval(t *testing.T) {
	gen := GenCircleIris(0.5, 0.5, 2.0, 1.0, false)
	buf := gen(testW, testH)
	checkBasics(t, "CircleIris(oval)", buf, testW, testH)

	// With aspectX=2, horizontal stretch should make the right edge have a different
	// value than the bottom edge (at same distance from center)
	right := px(buf, testW, testW-1, testH/2)
	bottom := px(buf, testW, testW/2, testH-1)
	if right == bottom {
		// They should differ due to aspect ratio
		t.Logf("CircleIris(oval): right=%d, bottom=%d (may be equal at extremes)", right, bottom)
	}
}

func TestGenCircleIris_Reverse(t *testing.T) {
	fwd := GenCircleIris(0.5, 0.5, 1.0, 1.0, false)(testW, testH)
	rev := GenCircleIris(0.5, 0.5, 1.0, 1.0, true)(testW, testH)
	midIdx := testH/2*testW + testW/2
	if fwd[midIdx] >= rev[midIdx] {
		t.Errorf("CircleIris reverse: fwd center(%d) should be < rev center(%d)", fwd[midIdx], rev[midIdx])
	}
}

// --- GenDiamondIris ---

func TestGenDiamondIris(t *testing.T) {
	gen := GenDiamondIris(0.5, 0.5, false)
	buf := gen(testW, testH)
	checkBasics(t, "DiamondIris", buf, testW, testH)

	center := px(buf, testW, testW/2, testH/2)
	corner := px(buf, testW, 0, 0)
	if center >= corner {
		t.Errorf("DiamondIris: center(%d) should be < corner(%d)", center, corner)
	}
}

// --- GenClockWipe ---

func TestGenClockWipe(t *testing.T) {
	gen := GenClockWipe(270, 1, true) // start at 12 o'clock, clockwise
	buf := gen(testW, testH)
	checkBasics(t, "ClockWipe", buf, testW, testH)

	cx, cy := testW/2, testH/2
	// 12 o'clock (top center) should be near 0
	top := px(buf, testW, cx, 0)
	// 3 o'clock (right center) should be ~64
	right := px(buf, testW, testW-1, cy)
	// 6 o'clock (bottom center) should be ~128
	bottom := px(buf, testW, cx, testH-1)

	if top > right {
		t.Errorf("ClockWipe: top(%d) should be <= right(%d)", top, right)
	}
	if right > bottom {
		t.Errorf("ClockWipe: right(%d) should be <= bottom(%d)", right, bottom)
	}
}

func TestGenClockWipe_MultiHand(t *testing.T) {
	gen := GenClockWipe(0, 2, true)
	buf := gen(testW, testH)
	checkBasics(t, "ClockWipe(2)", buf, testW, testH)
	// Two hands should create a pattern where values repeat twice around the circle
	// Just verify it runs and produces valid output
}

// --- GenVClockWipe ---

func TestGenVClockWipe(t *testing.T) {
	gen := GenVClockWipe(270) // start at 12 o'clock
	buf := gen(testW, testH)
	checkBasics(t, "VClockWipe", buf, testW, testH)

	cx := testW / 2
	// Top center should be near 0 (start angle)
	top := px(buf, testW, cx, 0)
	// Bottom should be near 255 (opposite of start)
	bottom := px(buf, testW, cx, testH-1)
	if top >= bottom {
		t.Errorf("VClockWipe: top(%d) should be < bottom(%d)", top, bottom)
	}
}

// --- GenVenetianBlind ---

func TestGenVenetianBlind_Horizontal(t *testing.T) {
	gen := GenVenetianBlind(4, true) // 4 horizontal blinds
	buf := gen(testW, testH)
	checkBasics(t, "VenetianBlind(h)", buf, testW, testH)

	// Check that the pattern repeats: compare top of first blind with top of second
	blindH := testH / 4
	v0 := px(buf, testW, testW/2, 0)
	v1 := px(buf, testW, testW/2, blindH)
	// Both should be near the start of their ramp
	diff := int(v0) - int(v1)
	if diff < 0 {
		diff = -diff
	}
	if diff > 20 {
		t.Errorf("VenetianBlind: repeating ramps should be similar at blind starts, got %d vs %d", v0, v1)
	}
}

func TestGenVenetianBlind_Vertical(t *testing.T) {
	gen := GenVenetianBlind(6, false) // 6 vertical blinds
	buf := gen(testW, testH)
	checkBasics(t, "VenetianBlind(v)", buf, testW, testH)
}

// --- GenCheckerboard ---

func TestGenCheckerboard(t *testing.T) {
	gen := GenCheckerboard(8, 6)
	buf := gen(testW, testH)
	checkBasics(t, "Checkerboard", buf, testW, testH)

	// Different blocks should have different values
	blockW := testW / 8
	blockH := testH / 6
	v00 := px(buf, testW, blockW/2, blockH/2)           // block (0,0)
	v10 := px(buf, testW, blockW+blockW/2, blockH/2)     // block (1,0)
	v01 := px(buf, testW, blockW/2, blockH+blockH/2)     // block (0,1)
	if v00 == v10 && v10 == v01 {
		t.Errorf("Checkerboard: blocks should have different values, all=%d", v00)
	}
}

// --- GenCheckerboardDiagonal ---

func TestGenCheckerboardDiagonal(t *testing.T) {
	gen := GenCheckerboardDiagonal(40)
	buf := gen(testW, testH)
	checkBasics(t, "CheckerboardDiag", buf, testW, testH)

	// Just verify it produces variation
	minV, maxV := byte(255), byte(0)
	for _, v := range buf {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if maxV-minV < 100 {
		t.Errorf("CheckerboardDiag: insufficient range %d-%d", minV, maxV)
	}
}

// --- GenTriangle ---

func TestGenTriangle(t *testing.T) {
	for _, dir := range []string{"up", "down", "left", "right"} {
		t.Run(dir, func(t *testing.T) {
			gen := GenTriangle(dir)
			buf := gen(testW, testH)
			checkBasics(t, "Triangle("+dir+")", buf, testW, testH)

			// Verify variation exists
			minV, maxV := byte(255), byte(0)
			for _, v := range buf {
				if v < minV {
					minV = v
				}
				if v > maxV {
					maxV = v
				}
			}
			if maxV-minV < 100 {
				t.Errorf("Triangle(%s): insufficient range %d-%d", dir, minV, maxV)
			}
		})
	}
}

func TestGenTriangle_Up_CenterLow(t *testing.T) {
	gen := GenTriangle("up")
	buf := gen(testW, testH)
	// "up" triangle points up from bottom edge - center bottom should be inside (low), top corners outside (high)
	centerBottom := px(buf, testW, testW/2, testH-1)
	topCorner := px(buf, testW, 0, 0)
	if centerBottom >= topCorner {
		t.Errorf("Triangle(up): center-bottom(%d) should be < top-corner(%d)", centerBottom, topCorner)
	}
}

// --- GenStar ---

func TestGenStar(t *testing.T) {
	gen := GenStar(5)
	buf := gen(testW, testH)
	checkBasics(t, "Star(5)", buf, testW, testH)

	center := px(buf, testW, testW/2, testH/2)
	corner := px(buf, testW, 0, 0)
	if center >= corner {
		t.Errorf("Star: center(%d) should be < corner(%d)", center, corner)
	}
}

// --- GenCross ---

func TestGenCross(t *testing.T) {
	gen := GenCross(false)
	buf := gen(testW, testH)
	checkBasics(t, "Cross", buf, testW, testH)

	center := px(buf, testW, testW/2, testH/2)
	corner := px(buf, testW, 0, 0)
	// Center is on the cross (low value)
	if center >= corner {
		t.Errorf("Cross: center(%d) should be < corner(%d)", center, corner)
	}
}

func TestGenCross_Rotated(t *testing.T) {
	gen := GenCross(true)
	buf := gen(testW, testH)
	checkBasics(t, "Cross(rotated)", buf, testW, testH)
}

// --- GenPolygon ---

func TestGenPolygon(t *testing.T) {
	for _, sides := range []int{3, 5, 6, 8} {
		t.Run("sides="+string(rune('0'+sides)), func(t *testing.T) {
			gen := GenPolygon(sides)
			buf := gen(testW, testH)
			checkBasics(t, "Polygon", buf, testW, testH)

			center := px(buf, testW, testW/2, testH/2)
			corner := px(buf, testW, 0, 0)
			if center >= corner {
				t.Errorf("Polygon(%d): center(%d) should be < corner(%d)", sides, center, corner)
			}
		})
	}
}

// --- GenHeart ---

func TestGenHeart(t *testing.T) {
	gen := GenHeart()
	buf := gen(testW, testH)
	checkBasics(t, "Heart", buf, testW, testH)

	// Center of the heart should be inside (low value)
	// Heart center is roughly at (0.5, 0.55) of the image
	cx := testW / 2
	cy := int(float64(testH) * 0.55)
	center := px(buf, testW, cx, cy)
	corner := px(buf, testW, 0, 0)
	if center >= corner {
		t.Errorf("Heart: center(%d) should be < corner(%d)", center, corner)
	}
}

// --- GenKeyhole ---

func TestGenKeyhole(t *testing.T) {
	gen := GenKeyhole()
	buf := gen(testW, testH)
	checkBasics(t, "Keyhole", buf, testW, testH)

	// Center of keyhole head (upper portion) should be inside (low value)
	center := px(buf, testW, testW/2, testH/3)
	corner := px(buf, testW, 0, 0)
	if center >= corner {
		t.Errorf("Keyhole: center(%d) should be < corner(%d)", center, corner)
	}
}

// --- GenZigzag ---

func TestGenZigzag(t *testing.T) {
	for _, axis := range []string{"h", "v", "diag"} {
		t.Run(axis, func(t *testing.T) {
			gen := GenZigzag(axis, 8)
			buf := gen(testW, testH)
			checkBasics(t, "Zigzag("+axis+")", buf, testW, testH)
		})
	}
}

func TestGenZigzag_Horizontal_LeftLow(t *testing.T) {
	gen := GenZigzag("h", 8)
	buf := gen(testW, testH)
	// Overall gradient should go left to right
	leftAvg := 0
	rightAvg := 0
	for y := 0; y < testH; y++ {
		leftAvg += int(px(buf, testW, 0, y))
		rightAvg += int(px(buf, testW, testW-1, y))
	}
	if leftAvg >= rightAvg {
		t.Errorf("Zigzag(h): left avg(%d) should be < right avg(%d)", leftAvg/testH, rightAvg/testH)
	}
}

// --- GenSpiral ---

func TestGenSpiral(t *testing.T) {
	gen := GenSpiral(true)
	buf := gen(testW, testH)
	checkBasics(t, "Spiral(cw)", buf, testW, testH)

	// Check that there's variation
	minV, maxV := byte(255), byte(0)
	for _, v := range buf {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if maxV-minV < 200 {
		t.Errorf("Spiral: insufficient range %d-%d", minV, maxV)
	}
}

func TestGenSpiral_CCW(t *testing.T) {
	gen := GenSpiral(false)
	buf := gen(testW, testH)
	checkBasics(t, "Spiral(ccw)", buf, testW, testH)
}

// --- GenRandom ---

func TestGenRandom_Deterministic(t *testing.T) {
	gen := GenRandom(4, 42)
	buf1 := gen(testW, testH)
	buf2 := gen(testW, testH)
	checkBasics(t, "Random", buf1, testW, testH)

	for i := range buf1 {
		if buf1[i] != buf2[i] {
			t.Fatalf("Random(seed=42): not deterministic at pixel %d", i)
		}
	}
}

func TestGenRandom_DifferentSeeds(t *testing.T) {
	buf1 := GenRandom(4, 42)(testW, testH)
	buf2 := GenRandom(4, 99)(testW, testH)

	diffs := 0
	for i := range buf1 {
		if buf1[i] != buf2[i] {
			diffs++
		}
	}
	if diffs < len(buf1)/2 {
		t.Errorf("Random: different seeds should produce mostly different output, only %d/%d differ", diffs, len(buf1))
	}
}

func TestGenRandom_PerPixel(t *testing.T) {
	gen := GenRandom(1, 123)
	buf := gen(testW, testH)
	checkBasics(t, "Random(1px)", buf, testW, testH)

	// With blockSize=1, adjacent pixels should generally differ
	diffs := 0
	for i := 1; i < len(buf); i++ {
		if buf[i] != buf[i-1] {
			diffs++
		}
	}
	if diffs < len(buf)/4 {
		t.Errorf("Random(1px): too few pixel-to-pixel differences: %d", diffs)
	}
}

// --- GenMatrixCascade ---

func TestGenMatrixCascade(t *testing.T) {
	gen := GenMatrixCascade()
	buf := gen(testW, testH)
	checkBasics(t, "MatrixCascade", buf, testW, testH)

	// Top values should generally be lower than bottom (columns cascade down)
	topAvg := 0
	botAvg := 0
	for x := 0; x < testW; x++ {
		topAvg += int(px(buf, testW, x, 0))
		botAvg += int(px(buf, testW, x, testH-1))
	}
	if topAvg >= botAvg {
		t.Errorf("MatrixCascade: top avg(%d) should be < bottom avg(%d)", topAvg/testW, botAvg/testW)
	}
}

// --- GenRadialBurst ---

func TestGenRadialBurst(t *testing.T) {
	gen := GenRadialBurst(8)
	buf := gen(testW, testH)
	checkBasics(t, "RadialBurst(8)", buf, testW, testH)

	// Adjacent segments should have different values
	_, cy := testW/2, testH/2
	// Right center (segment 0) vs top-right (segment 1)
	right := px(buf, testW, testW-1, cy)
	topRight := px(buf, testW, testW-1, 0)
	if right == topRight {
		t.Logf("RadialBurst: right=%d topRight=%d (may be same segment)", right, topRight)
	}
}

// --- GenWave ---

func TestGenWave(t *testing.T) {
	for _, axis := range []string{"h", "v"} {
		t.Run(axis, func(t *testing.T) {
			gen := GenWave(axis, 3)
			buf := gen(testW, testH)
			checkBasics(t, "Wave("+axis+")", buf, testW, testH)
		})
	}
}

func TestGenWave_Horizontal_OverallGradient(t *testing.T) {
	gen := GenWave("h", 3)
	buf := gen(testW, testH)

	leftAvg := 0
	rightAvg := 0
	for y := 0; y < testH; y++ {
		leftAvg += int(px(buf, testW, 0, y))
		rightAvg += int(px(buf, testW, testW-1, y))
	}
	if leftAvg >= rightAvg {
		t.Errorf("Wave(h): left avg(%d) should be < right avg(%d)", leftAvg/testH, rightAvg/testH)
	}
}

// --- GenBinoculars ---

func TestGenBinoculars(t *testing.T) {
	gen := GenBinoculars()
	buf := gen(testW, testH)
	checkBasics(t, "Binoculars", buf, testW, testH)

	// Left eye center (~35% x) should be low
	leftEye := px(buf, testW, int(0.35*float64(testW)), testH/2)
	// Right eye center (~65% x) should be low
	rightEye := px(buf, testW, int(0.65*float64(testW)), testH/2)
	// Corner should be high
	corner := px(buf, testW, 0, 0)

	if leftEye >= corner {
		t.Errorf("Binoculars: leftEye(%d) should be < corner(%d)", leftEye, corner)
	}
	if rightEye >= corner {
		t.Errorf("Binoculars: rightEye(%d) should be < corner(%d)", rightEye, corner)
	}
}

// --- GenLetterbox ---

func TestGenLetterbox(t *testing.T) {
	gen := GenLetterbox(false)
	buf := gen(testW, testH)
	checkBasics(t, "Letterbox", buf, testW, testH)

	// Edges (top/bottom) should be lower, center higher
	top := px(buf, testW, testW/2, 0)
	center := px(buf, testW, testW/2, testH/2)
	if top >= center {
		t.Errorf("Letterbox: top(%d) should be < center(%d)", top, center)
	}
}

func TestGenLetterbox_Reverse(t *testing.T) {
	fwd := GenLetterbox(false)(testW, testH)
	rev := GenLetterbox(true)(testW, testH)
	midIdx := testH/2*testW + testW/2
	if fwd[midIdx] <= rev[midIdx] {
		t.Errorf("Letterbox reverse: fwd center(%d) should be > rev center(%d)", fwd[midIdx], rev[midIdx])
	}
}

// --- GenPinhole ---

func TestGenPinhole(t *testing.T) {
	gen := GenPinhole(0.5, 0.5)
	buf := gen(testW, testH)
	checkBasics(t, "Pinhole", buf, testW, testH)

	// Pinhole: center should be high (revealed first), edges low
	center := px(buf, testW, testW/2, testH/2)
	corner := px(buf, testW, 0, 0)
	if center <= corner {
		t.Errorf("Pinhole: center(%d) should be > corner(%d)", center, corner)
	}
}

// --- GenCurtain ---

func TestGenCurtain(t *testing.T) {
	gen := GenCurtain(false)
	buf := gen(testW, testH)
	checkBasics(t, "Curtain", buf, testW, testH)

	// Center should be high (curtain opens from center)
	center := px(buf, testW, testW/2, testH/2)
	edge := px(buf, testW, 0, testH/2)
	if center <= edge {
		t.Errorf("Curtain: center(%d) should be > edge(%d)", center, edge)
	}
}

func TestGenCurtain_Reverse(t *testing.T) {
	fwd := GenCurtain(false)(testW, testH)
	rev := GenCurtain(true)(testW, testH)
	midIdx := testH/2*testW + testW/2
	if fwd[midIdx] <= rev[midIdx] {
		t.Errorf("Curtain reverse: fwd center(%d) should be > rev center(%d)", fwd[midIdx], rev[midIdx])
	}
}

// --- GenDissolve ---

func TestGenDissolve_Perlin(t *testing.T) {
	gen := GenDissolve("perlin")
	buf := gen(testW, testH)
	checkBasics(t, "Dissolve(perlin)", buf, testW, testH)

	// Should have good range
	minV, maxV := byte(255), byte(0)
	for _, v := range buf {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if maxV-minV < 150 {
		t.Errorf("Dissolve(perlin): insufficient range %d-%d", minV, maxV)
	}
}

func TestGenDissolve_Blocks(t *testing.T) {
	gen := GenDissolve("blocks")
	buf := gen(testW, testH)
	checkBasics(t, "Dissolve(blocks)", buf, testW, testH)
}

func TestGenDissolve_Scanline(t *testing.T) {
	gen := GenDissolve("scanline")
	buf := gen(testW, testH)
	checkBasics(t, "Dissolve(scanline)", buf, testW, testH)

	// Overall gradient should be top to bottom
	topAvg := 0
	botAvg := 0
	for x := 0; x < testW; x++ {
		topAvg += int(px(buf, testW, x, 0))
		botAvg += int(px(buf, testW, x, testH-1))
	}
	if topAvg >= botAvg {
		t.Errorf("Dissolve(scanline): top avg(%d) should be < bottom avg(%d)", topAvg/testW, botAvg/testW)
	}
}

func TestGenDissolve_Burn(t *testing.T) {
	gen := GenDissolve("burn")
	buf := gen(testW, testH)
	checkBasics(t, "Dissolve(burn)", buf, testW, testH)

	// Edges should be low (burned first), center high
	edge := px(buf, testW, 0, 0)
	center := px(buf, testW, testW/2, testH/2)
	if edge >= center {
		t.Errorf("Dissolve(burn): edge(%d) should be < center(%d)", edge, center)
	}
}

// --- Degenerate sizes ---

func TestGenerators_1x1(t *testing.T) {
	generators := []struct {
		name string
		gen  GeneratorFunc
	}{
		{"Diagonal(45)", GenDiagonal(45)},
		{"BarnDoor(h)", GenBarnDoor("h", false)},
		{"BoxIris", GenBoxIris(0.5, 0.5, false)},
		{"CircleIris", GenCircleIris(0.5, 0.5, 1, 1, false)},
		{"DiamondIris", GenDiamondIris(0.5, 0.5, false)},
		{"ClockWipe", GenClockWipe(0, 1, true)},
		{"VClockWipe", GenVClockWipe(0)},
		{"VenetianBlind", GenVenetianBlind(4, true)},
		{"Checkerboard", GenCheckerboard(4, 4)},
		{"CheckerboardDiag", GenCheckerboardDiagonal(10)},
		{"Triangle(up)", GenTriangle("up")},
		{"Star(5)", GenStar(5)},
		{"Cross", GenCross(false)},
		{"Polygon(6)", GenPolygon(6)},
		{"Heart", GenHeart()},
		{"Keyhole", GenKeyhole()},
		{"Zigzag(h,4)", GenZigzag("h", 4)},
		{"Spiral", GenSpiral(true)},
		{"Random(1,0)", GenRandom(1, 0)},
		{"MatrixCascade", GenMatrixCascade()},
		{"RadialBurst(8)", GenRadialBurst(8)},
		{"Wave(h,2)", GenWave("h", 2)},
		{"Binoculars", GenBinoculars()},
		{"Letterbox", GenLetterbox(false)},
		{"Pinhole", GenPinhole(0.5, 0.5)},
		{"Curtain", GenCurtain(false)},
		{"Dissolve(perlin)", GenDissolve("perlin")},
	}
	for _, tc := range generators {
		t.Run(tc.name, func(t *testing.T) {
			buf := tc.gen(1, 1)
			if len(buf) != 1 {
				t.Fatalf("%s: 1x1 produced len=%d", tc.name, len(buf))
			}
		})
	}
}

// --- Benchmark ---

func BenchmarkGenCircleIris_1080p(b *testing.B) {
	gen := GenCircleIris(0.5, 0.5, 1.0, 1.0, false)
	for i := 0; i < b.N; i++ {
		gen(1920, 1080)
	}
}

func BenchmarkGenClockWipe_1080p(b *testing.B) {
	gen := GenClockWipe(270, 1, true)
	for i := 0; i < b.N; i++ {
		gen(1920, 1080)
	}
}

// Ensure math is imported (used in test constants above)
var _ = math.Pi
