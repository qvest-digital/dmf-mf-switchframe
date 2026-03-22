package colorgrade

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateIdentityLUT(t *testing.T) {
	lut := GenerateIdentityLUT()

	if lut.Size != 33 {
		t.Fatalf("size = %d, want 33", lut.Size)
	}

	expected := 33 * 33 * 33 * 3
	if len(lut.Data) != expected {
		t.Fatalf("data length = %d, want %d", len(lut.Data), expected)
	}

	// Check corners: identity LUT maps input to itself.
	// Index (0,0,0) → (0,0,0)
	if lut.Data[0] != 0 || lut.Data[1] != 0 || lut.Data[2] != 0 {
		t.Errorf("(0,0,0) = [%f,%f,%f], want [0,0,0]", lut.Data[0], lut.Data[1], lut.Data[2])
	}
	// Index (32,32,32) → (1,1,1)
	last := (32*33*33 + 32*33 + 32) * 3
	if lut.Data[last] != 1 || lut.Data[last+1] != 1 || lut.Data[last+2] != 1 {
		t.Errorf("(32,32,32) = [%f,%f,%f], want [1,1,1]", lut.Data[last], lut.Data[last+1], lut.Data[last+2])
	}
	// Midpoint (16,16,16) → (0.5, 0.5, 0.5)
	mid := (16*33*33 + 16*33 + 16) * 3
	if lut.Data[mid] != 0.5 || lut.Data[mid+1] != 0.5 || lut.Data[mid+2] != 0.5 {
		t.Errorf("(16,16,16) = [%f,%f,%f], want [0.5,0.5,0.5]", lut.Data[mid], lut.Data[mid+1], lut.Data[mid+2])
	}
}

func TestWriteCubeRoundtrip(t *testing.T) {
	lut := GenerateIdentityLUT()
	lut.Title = "Roundtrip Test"

	var buf bytes.Buffer
	if err := WriteCube(&buf, lut); err != nil {
		t.Fatalf("WriteCube error: %v", err)
	}

	parsed, err := ParseCube(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("ParseCube error: %v", err)
	}

	if parsed.Title != lut.Title {
		t.Errorf("title = %q, want %q", parsed.Title, lut.Title)
	}
	if parsed.Size != lut.Size {
		t.Errorf("size = %d, want %d", parsed.Size, lut.Size)
	}
	if len(parsed.Data) != len(lut.Data) {
		t.Fatalf("data length = %d, want %d", len(parsed.Data), len(lut.Data))
	}

	for i := range lut.Data {
		if math.Abs(float64(parsed.Data[i]-lut.Data[i])) > 1e-5 {
			t.Errorf("data[%d] = %f, want %f", i, parsed.Data[i], lut.Data[i])
			break
		}
	}
}

func TestGenerateLook_Cinematic(t *testing.T) {
	identity := GenerateIdentityLUT()
	cinematic := GenerateLook(BuiltinLooks()[1]) // cinematic

	// The cinematic look should differ from identity.
	differences := 0
	for i := range identity.Data {
		if math.Abs(float64(cinematic.Data[i]-identity.Data[i])) > 1e-5 {
			differences++
		}
	}

	if differences == 0 {
		t.Error("cinematic look is identical to identity")
	}

	// All values should remain in [0, 1].
	for i, v := range cinematic.Data {
		if v < 0 || v > 1 {
			t.Errorf("data[%d] = %f, out of [0,1] range", i, v)
			break
		}
	}
}

func TestAllBuiltinLooks(t *testing.T) {
	looks := BuiltinLooks()
	if len(looks) != 6 {
		t.Fatalf("expected 6 built-in looks, got %d", len(looks))
	}

	for _, params := range looks {
		t.Run(params.Name, func(t *testing.T) {
			lut := GenerateLook(params)

			if lut.Title != params.Name {
				t.Errorf("title = %q, want %q", lut.Title, params.Name)
			}
			if lut.Size != 33 {
				t.Errorf("size = %d, want 33", lut.Size)
			}

			expected := 33 * 33 * 33 * 3
			if len(lut.Data) != expected {
				t.Fatalf("data length = %d, want %d", len(lut.Data), expected)
			}

			// All values should be in [0, 1].
			for i, v := range lut.Data {
				if v < 0 || v > 1 {
					t.Errorf("data[%d] = %f, out of [0,1] range", i, v)
					break
				}
			}

			// Should roundtrip through .cube format.
			var buf bytes.Buffer
			if err := WriteCube(&buf, lut); err != nil {
				t.Fatalf("WriteCube error: %v", err)
			}
			parsed, err := ParseCube(strings.NewReader(buf.String()))
			if err != nil {
				t.Fatalf("ParseCube error: %v", err)
			}
			if parsed.Size != lut.Size {
				t.Errorf("roundtrip size mismatch: %d vs %d", parsed.Size, lut.Size)
			}
		})
	}
}

func TestApplyCDL_Identity(t *testing.T) {
	lut := GenerateIdentityLUT()
	original := make([]float32, len(lut.Data))
	copy(original, lut.Data)

	// Identity CDL: slope=1, offset=0, power=1
	ApplyCDL(lut, 1, 1, 1, 0, 0, 0, 1, 1, 1)

	for i := range lut.Data {
		if math.Abs(float64(lut.Data[i]-original[i])) > 1e-6 {
			t.Errorf("identity CDL changed data[%d]: %f → %f", i, original[i], lut.Data[i])
			break
		}
	}
}

func TestApplySaturation_Zero(t *testing.T) {
	lut := GenerateIdentityLUT()
	ApplySaturation(lut, 0) // Should produce grayscale.

	// For each triplet, R=G=B (luminance only).
	for i := 0; i < len(lut.Data); i += 3 {
		r, g, b := lut.Data[i], lut.Data[i+1], lut.Data[i+2]
		if math.Abs(float64(r-g)) > 1e-5 || math.Abs(float64(g-b)) > 1e-5 {
			t.Errorf("not grayscale at %d: [%f, %f, %f]", i/3, r, g, b)
			break
		}
	}
}

func TestGenerateLookFiles(t *testing.T) {
	dir := t.TempDir()

	for _, params := range BuiltinLooks() {
		lut := GenerateLook(params)
		path := filepath.Join(dir, params.Name+".cube")

		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		if err := WriteCube(f, lut); err != nil {
			f.Close()
			t.Fatalf("write %s: %v", path, err)
		}
		f.Close()

		// Verify the file parses back.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		parsed, err := ParseCube(strings.NewReader(string(data)))
		if err != nil {
			t.Fatalf("parse %s: %v", params.Name, err)
		}
		if parsed.Size != 33 {
			t.Errorf("%s: size = %d, want 33", params.Name, parsed.Size)
		}
	}
}
