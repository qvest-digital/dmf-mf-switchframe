package wipemap

import (
	"testing"
)

func TestGenHorizontal(t *testing.T) {
	gen := GenHorizontal(false)
	buf := gen(100, 100)

	if got := buf[50*100+0]; got != 0 {
		t.Errorf("pixel(0,50) = %d, want 0", got)
	}
	if got := buf[50*100+99]; got != 255 {
		t.Errorf("pixel(99,50) = %d, want 255", got)
	}
	mid := buf[50*100+50]
	if mid < 125 || mid > 130 {
		t.Errorf("pixel(50,50) = %d, want ~128", mid)
	}

	// All rows must be identical.
	row0 := buf[0:100]
	for y := 1; y < 100; y++ {
		row := buf[y*100 : y*100+100]
		for x := 0; x < 100; x++ {
			if row[x] != row0[x] {
				t.Fatalf("row %d differs from row 0 at col %d: %d != %d", y, x, row[x], row0[x])
			}
		}
	}
}

func TestGenHorizontalReverse(t *testing.T) {
	gen := GenHorizontal(true)
	buf := gen(100, 100)

	if got := buf[50*100+0]; got != 255 {
		t.Errorf("pixel(0,50) = %d, want 255", got)
	}
	if got := buf[50*100+99]; got != 0 {
		t.Errorf("pixel(99,50) = %d, want 0", got)
	}
}

func TestGenVertical(t *testing.T) {
	gen := GenVertical(false)
	buf := gen(100, 100)

	if got := buf[0*100+50]; got != 0 {
		t.Errorf("pixel(50,0) = %d, want 0", got)
	}
	if got := buf[99*100+50]; got != 255 {
		t.Errorf("pixel(50,99) = %d, want 255", got)
	}

	// All columns must be identical for a given row.
	for y := 0; y < 100; y++ {
		v := buf[y*100]
		for x := 1; x < 100; x++ {
			if buf[y*100+x] != v {
				t.Fatalf("row %d col %d = %d, want %d (same as col 0)", y, x, buf[y*100+x], v)
			}
		}
	}
}

func TestGenVerticalReverse(t *testing.T) {
	gen := GenVertical(true)
	buf := gen(100, 100)

	if got := buf[0*100+50]; got != 255 {
		t.Errorf("pixel(50,0) = %d, want 255", got)
	}
	if got := buf[99*100+50]; got != 0 {
		t.Errorf("pixel(50,99) = %d, want 0", got)
	}
}

func TestGenDimensions(t *testing.T) {
	sizes := [][2]int{{1920, 1080}, {100, 100}, {2, 2}}
	gens := []struct {
		name string
		fn   GeneratorFunc
	}{
		{"horizontal", GenHorizontal(false)},
		{"vertical", GenVertical(false)},
	}

	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		for _, g := range gens {
			buf := g.fn(w, h)
			if len(buf) != w*h {
				t.Errorf("%s(%dx%d): len=%d, want %d", g.name, w, h, len(buf), w*h)
			}
		}
	}
}

func TestGenMonotonic_Horizontal(t *testing.T) {
	// Forward: non-decreasing along X.
	buf := GenHorizontal(false)(256, 1)
	for x := 1; x < 256; x++ {
		if buf[x] < buf[x-1] {
			t.Fatalf("forward: value decreased at x=%d: %d < %d", x, buf[x], buf[x-1])
		}
	}

	// Reverse: non-increasing along X.
	buf = GenHorizontal(true)(256, 1)
	for x := 1; x < 256; x++ {
		if buf[x] > buf[x-1] {
			t.Fatalf("reverse: value increased at x=%d: %d > %d", x, buf[x], buf[x-1])
		}
	}
}

func TestGenMonotonic_Vertical(t *testing.T) {
	// Forward: non-decreasing along Y.
	buf := GenVertical(false)(1, 256)
	for y := 1; y < 256; y++ {
		if buf[y] < buf[y-1] {
			t.Fatalf("forward: value decreased at y=%d: %d < %d", y, buf[y], buf[y-1])
		}
	}

	// Reverse: non-increasing along Y.
	buf = GenVertical(true)(1, 256)
	for y := 1; y < 256; y++ {
		if buf[y] > buf[y-1] {
			t.Fatalf("reverse: value increased at y=%d: %d > %d", y, buf[y], buf[y-1])
		}
	}
}
