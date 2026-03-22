package colorgrade

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseCube_Valid3x3(t *testing.T) {
	input := `# Comment line
TITLE "Test LUT"
LUT_3D_SIZE 3
0.0 0.0 0.0
0.5 0.0 0.0
1.0 0.0 0.0
0.0 0.5 0.0
0.5 0.5 0.0
1.0 0.5 0.0
0.0 1.0 0.0
0.5 1.0 0.0
1.0 1.0 0.0
0.0 0.0 0.5
0.5 0.0 0.5
1.0 0.0 0.5
0.0 0.5 0.5
0.5 0.5 0.5
1.0 0.5 0.5
0.0 1.0 0.5
0.5 1.0 0.5
1.0 1.0 0.5
0.0 0.0 1.0
0.5 0.0 1.0
1.0 0.0 1.0
0.0 0.5 1.0
0.5 0.5 1.0
1.0 0.5 1.0
0.0 1.0 1.0
0.5 1.0 1.0
1.0 1.0 1.0
`
	lut, err := ParseCube(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lut.Title != "Test LUT" {
		t.Errorf("title = %q, want %q", lut.Title, "Test LUT")
	}
	if lut.Size != 3 {
		t.Errorf("size = %d, want 3", lut.Size)
	}
	// 3^3 = 27 triplets = 81 floats
	if len(lut.Data) != 81 {
		t.Fatalf("data length = %d, want 81", len(lut.Data))
	}
	// First triplet: 0.0, 0.0, 0.0
	if lut.Data[0] != 0.0 || lut.Data[1] != 0.0 || lut.Data[2] != 0.0 {
		t.Errorf("first triplet = [%f, %f, %f], want [0, 0, 0]",
			lut.Data[0], lut.Data[1], lut.Data[2])
	}
	// Last triplet: 1.0, 1.0, 1.0
	if lut.Data[78] != 1.0 || lut.Data[79] != 1.0 || lut.Data[80] != 1.0 {
		t.Errorf("last triplet = [%f, %f, %f], want [1, 1, 1]",
			lut.Data[78], lut.Data[79], lut.Data[80])
	}
}

func TestParseCube_MissingSize(t *testing.T) {
	input := "0.0 0.0 0.0\n"
	_, err := ParseCube(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for missing LUT_3D_SIZE")
	}
}

func TestParseCube_WrongDataCount(t *testing.T) {
	input := `LUT_3D_SIZE 3
0.0 0.0 0.0
0.5 0.0 0.0
`
	_, err := ParseCube(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for wrong data count")
	}
}

func TestParseCube_CommentsAndTitleSkipped(t *testing.T) {
	input := `# This is a comment
TITLE "My Look"
# Another comment
DOMAIN_MIN 0.0 0.0 0.0
DOMAIN_MAX 1.0 1.0 1.0

LUT_3D_SIZE 2
0.0 0.0 0.0
1.0 0.0 0.0
0.0 1.0 0.0
1.0 1.0 0.0
0.0 0.0 1.0
1.0 0.0 1.0
0.0 1.0 1.0
1.0 1.0 1.0
`
	lut, err := ParseCube(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lut.Title != "My Look" {
		t.Errorf("title = %q, want %q", lut.Title, "My Look")
	}
	if lut.Size != 2 {
		t.Errorf("size = %d, want 2", lut.Size)
	}
	if len(lut.Data) != 24 {
		t.Errorf("data length = %d, want 24", len(lut.Data))
	}
}

func TestParseCube_33x33x33(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("LUT_3D_SIZE 33\n")
	for b := 0; b < 33; b++ {
		for g := 0; g < 33; g++ {
			for r := 0; r < 33; r++ {
				rf := float64(r) / 32.0
				gf := float64(g) / 32.0
				bf := float64(b) / 32.0
				fmt.Fprintf(&sb, "%.6f %.6f %.6f\n", rf, gf, bf)
			}
		}
	}

	lut, err := ParseCube(strings.NewReader(sb.String()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lut.Size != 33 {
		t.Errorf("size = %d, want 33", lut.Size)
	}
	expected := 33 * 33 * 33 * 3
	if len(lut.Data) != expected {
		t.Errorf("data length = %d, want %d", len(lut.Data), expected)
	}
}

func TestParseCube_InvalidFloat(t *testing.T) {
	input := `LUT_3D_SIZE 2
0.0 0.0 0.0
1.0 0.0 abc
0.0 1.0 0.0
1.0 1.0 0.0
0.0 0.0 1.0
1.0 0.0 1.0
0.0 1.0 1.0
1.0 1.0 1.0
`
	_, err := ParseCube(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for invalid float")
	}
}

func TestParseCube_InvalidSize(t *testing.T) {
	input := "LUT_3D_SIZE 0\n"
	_, err := ParseCube(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for size 0")
	}

	input2 := "LUT_3D_SIZE 300\n"
	_, err = ParseCube(strings.NewReader(input2))
	if err == nil {
		t.Fatal("expected error for size 300")
	}
}
